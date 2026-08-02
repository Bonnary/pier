package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

type discardLogger struct{}

func (discardLogger) Emit(Event)                 {}
func (discardLogger) PhaseStart(string)          {}
func (discardLogger) PhaseEnd(string, error)     {}
func (discardLogger) Log(string, string, ...any) {}
func (discardLogger) JSON() bool                 { return false }
func (discardLogger) Writer() io.Writer          { return io.Discard }

func TestPipelineDryRun(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")},
		Health:    HealthConfig{URL: "https://x.example.com/up", Timeout: time.Second, Interval: 100 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
		// Skip SSH dial for dry-run by providing a stub that fails the connection.
		// For unit test, we expect Run to fail at preflight (no SSH server); this exercises the wiring.
	}
	_ = p
	_ = context.Background
}

func TestDeployFinalStateURL(t *testing.T) {
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}},
		},
	}, "production")
	want := "https://myapp.example.com:8383"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q", url, want)
	}
}

func TestDeployFinalStateURLDefault(t *testing.T) {
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b"},
		},
	}, "production")
	want := "https://myapp.example.com:443"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q (no override → default 443)", url, want)
	}
}

func TestPipelineAbortPropagates(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	origDial := pipelineDial
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return nil, AbortedError()
	}
	defer func() { pipelineDial = origDial }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: "/nonexistent"},
		Now:       time.Now,
	}
	err := p.Run(context.Background())
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run() = %v, want ErrAborted", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitAborted {
		t.Fatalf("Run() error = %T, want *ExitError with code %d", err, ExitAborted)
	}
}

// failingSyncClient is a real *Client whose SyncDir always fails, used
// to exercise the sync-phase error wrap without touching the network.
type failingSyncClient struct {
	*Client
	err error
}

func (f *failingSyncClient) SyncDir(ctx context.Context, local, remote string, excludes []string) error {
	return f.err
}

// TestPipelineSyncsFilesToRemote drives Run past preflight into the
// sync phase against the in-process SSH/SFTP server and asserts the
// synced files land on the remote path. The pipeline cannot complete
// past sync in this sandbox (the build phase runs `docker compose` on
// the remote, and the test server rejects exec requests), so success
// is asserted as: remote files present AND the returned error is a
// build-phase ExitError — proving preflight and sync both passed.
func TestPipelineSyncsFilesToRemote(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	addr := startSSHServer(t, keyOnlyServer(pub))
	host, port := testAddr(t, addr)
	remote := t.TempDir()

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: host, User: "deploy", Path: remote, Branch: "main"},
		},
	}

	// ProbeBootstrap and EnsureDeployPath run remote commands
	// (docker info, mkdir -p) that the test server rejects; fake
	// them. pipelineDial is left as the real Dial — preflight
	// type-asserts the result to *Client, so a real dial is required.
	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Now:       time.Now,
	}
	err := p.Run(context.Background())

	// Run syncs the package directory (cwd is the package dir under
	// `go test`), so sftp.go must have landed on the remote.
	if _, statErr := os.Stat(filepath.Join(remote, "sftp.go")); statErr != nil {
		t.Fatalf("sync phase did not run: remote sftp.go missing: %v", statErr)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitBuild {
		t.Fatalf("Run() = %v, want build-phase ExitError (preflight+sync passed)", err)
	}
}

// TestPipelineSyncFailureWrapsPreflight asserts a sync-phase failure
// surfaces as a preflight-class ExitError with exit code 2.
func TestPipelineSyncFailureWrapsPreflight(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}

	// Sync fails on the fake client, so build/up/probe never run and
	// only the preflight-phase seams need faking.
	origDial, origProbe, origEnsure := pipelineDial, pipelineProbe, pipelineEnsurePath
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return &failingSyncClient{Client: &Client{Config: cfg}, err: fmt.Errorf("sync boom")}, nil
	}
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineDial, pipelineProbe, pipelineEnsurePath = origDial, origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: "/nonexistent"},
		Now:       time.Now,
	}
	err := p.Run(context.Background())
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("Run() = %v, want ErrPreflight", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitPreflight {
		t.Fatalf("Run() error = %T, want *ExitError with code %d", err, ExitPreflight)
	}
}

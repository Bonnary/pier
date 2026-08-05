package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// pinLookup makes the DNS seam deterministic for a test: resolves
// returns the resolver behavior ResolvedURL should observe.
func pinLookup(t *testing.T, resolves bool) {
	t.Helper()
	old := lookupHost
	lookupHost = func(string) ([]string, error) {
		if resolves {
			return []string{"127.0.0.1"}, nil
		}
		return nil, fmt.Errorf("no such host")
	}
	t.Cleanup(func() { lookupHost = old })
}

func TestDeployFinalStateURL(t *testing.T) {
	pinLookup(t, true)
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}},
		},
	}, "production")
	want := "http://myapp.example.com:8383"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q", url, want)
	}
}

func TestDeployFinalStateURLDefault(t *testing.T) {
	pinLookup(t, true)
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b"},
		},
	}, "production")
	want := "http://myapp.example.com:80"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q (no override → default 80 over plain HTTP)", url, want)
	}
}

func TestDeployFinalStateURLTLS(t *testing.T) {
	pinLookup(t, true)
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"tls on, no override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", TLS: true}, "https://myapp.example.com:443"},
		{"tls on, override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", TLS: true, Ports: map[string]int{"laravel": 8443}}, "https://myapp.example.com:8443"},
		{"tls on, laravel=0 falls back to 443", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", TLS: true, Ports: map[string]int{"laravel": 0}}, "https://myapp.example.com:443"},
	}
	for _, c := range cases {
		url := ResolvedURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: ResolvedURL = %q, want %q", c.name, url, c.want)
		}
	}
}

func TestHealthURL(t *testing.T) {
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"plain http default", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b"}, "http://192.168.1.10:80/up"},
		{"laravel override", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.1.10:8383/up"},
		{"tls on", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", TLS: true}, "https://192.168.1.10:443/up"},
	}
	for _, c := range cases {
		url := HealthURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: HealthURL = %q, want %q", c.name, url, c.want)
		}
	}
}

func TestResolvedURLFallsBackToHostIPWhenDomainDoesNotResolve(t *testing.T) {
	pinLookup(t, false)
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"plain http default", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b"}, "http://192.168.122.30:80"},
		{"laravel override", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.122.30:8383"},
		{"tls on", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b", TLS: true}, "https://192.168.122.30:443"},
	}
	for _, c := range cases {
		url := ResolvedURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: ResolvedURL = %q, want %q", c.name, url, c.want)
		}
	}
}

func TestResolvedURLKeepsDomainWithoutDeployHost(t *testing.T) {
	pinLookup(t, false)
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, "production")
	want := "http://myapp.example.com:80"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q (no deploy host to fall back to)", url, want)
	}
}

func TestResolvedURLBareIPDomain(t *testing.T) {
	pinLookup(t, true)
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "192.168.122.30"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "10.0.0.1", User: "u", Path: "p", Branch: "b"}},
	}, "production")
	want := "http://192.168.122.30:80"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q (IP domain passes through)", url, want)
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
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	// The render phase writes docker-compose.prod.yml and
	// .env.production into the cwd; an isolated temp cwd keeps that
	// output out of the repo tree. Seed a marker file so the sync
	// assertion targets content that only this test created.
	if err := os.WriteFile("marker.txt", []byte("sync me"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
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

	// Run syncs the temp cwd (chdir'd above), so the marker file
	// must have landed on the remote.
	if _, statErr := os.Stat(filepath.Join(remote, "marker.txt")); statErr != nil {
		t.Fatalf("sync phase did not run: remote marker.txt missing: %v", statErr)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitBuild {
		t.Fatalf("Run() = %v, want build-phase ExitError (preflight+sync passed)", err)
	}
}

// TestPipelineRenderAbortsWithoutLocalEnvFile asserts that a deploy
// whose cwd has no local .env.production still renders the fresh
// template file locally but aborts before sync: shipping placeholders
// to the deploy host would overwrite a real .env.production holding
// secrets there, so the pipeline stops with an actionable error.
func TestPipelineRenderAbortsWithoutLocalEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: host, User: "deploy", Path: remote, Branch: "main"},
		},
	}

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
	if err == nil || !strings.Contains(err.Error(), "no local .env.production") {
		t.Fatalf("Run() = %v, want render abort (no local .env.production)", err)
	}
	// renderProdFiles must have run: the fresh template exists locally.
	if _, statErr := os.Stat(".env.production"); statErr != nil {
		t.Fatalf("renderProdFiles did not create .env.production: %v", statErr)
	}
	// The deploy must stop before sync: no remote commands recorded.
	if len(fs.cmds) != 0 {
		t.Errorf("recorded commands = %q, want none (abort before sync)", fs.cmds)
	}
}

// TestPipelineSyncFailureWrapsPreflight asserts a sync-phase failure
// surfaces as a preflight-class ExitError with exit code 2.
func TestPipelineSyncFailureWrapsPreflight(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
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

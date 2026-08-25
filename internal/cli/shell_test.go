package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

const shellTestToml = "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n"

func writeShellTestConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(shellTestToml+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestShellRemoteCallsDeploy(t *testing.T) {
	dir := writeShellTestConfig(t, "[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n")
	var gotCfg deploy.SSHConfig
	var gotDir string
	orig := remoteShellFn
	remoteShellFn = func(ctx context.Context, cfg deploy.SSHConfig, dir string) error {
		gotCfg, gotDir = cfg, dir
		return nil
	}
	defer func() { remoteShellFn = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "shell", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotCfg.Host != "h" || gotCfg.User != "u" {
		t.Errorf("ssh config = %+v, want host=h user=u", gotCfg)
	}
	if gotDir != "/srv/x" {
		t.Errorf("dir = %q, want /srv/x", gotDir)
	}
}

func TestShellRemoteNoSection(t *testing.T) {
	dir := writeShellTestConfig(t, "")
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "shell", "production"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no [deploy.production] section") {
		t.Errorf("err = %v, want no [deploy.production] section error", err)
	}
}

func TestShellRemotePreflightMappedToSSH(t *testing.T) {
	dir := writeShellTestConfig(t, "[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n")
	orig := remoteShellFn
	remoteShellFn = func(ctx context.Context, cfg deploy.SSHConfig, dir string) error {
		return fmt.Errorf("%w: handshake failed", deploy.ErrPreflight)
	}
	defer func() { remoteShellFn = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "shell", "production"})
	err := root.Execute()
	var ee *deploy.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *ExitError", err)
	}
	if ee.Kind != deploy.KindSSH {
		t.Errorf("kind = %v, want KindSSH", ee.Kind)
	}
}

func TestShellRemoteExitCodePropagates(t *testing.T) {
	dir := writeShellTestConfig(t, "[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n")
	orig := remoteShellFn
	remoteShellFn = func(ctx context.Context, cfg deploy.SSHConfig, dir string) error {
		return &deploy.ExitError{Code: 42, Kind: deploy.KindUnknown, RemoteHost: "h", Err: fmt.Errorf("boom")}
	}
	defer func() { remoteShellFn = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "shell", "production"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute = nil error, want exit error")
	}
	if got := ExitCode(err); got != 42 {
		t.Errorf("exit code = %d, want 42", got)
	}
}

func TestShellLocalUnchanged(t *testing.T) {
	dir := writeShellTestConfig(t, "")
	runner := &capturingRunner{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "shell"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(runner.calls) == 0 {
		t.Fatal("no docker calls for local shell")
	}
	last := runner.calls[len(runner.calls)-1]
	if !strings.Contains(last, "laravel.test") || !strings.HasSuffix(last, "bash") {
		t.Errorf("local shell call = %q, want docker compose exec ... laravel.test ... bash", last)
	}
}

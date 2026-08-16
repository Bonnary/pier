package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

type capturingRunner struct {
	calls []string
}

func (c *capturingRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	call := name
	for _, a := range args {
		call += " " + a
	}
	c.calls = append(c.calls, call)
	stdout.Write([]byte("name\timage\tstate\nlaravel.test\tmyapp\tUp\n"))
	return nil
}

func TestExecBuildsCommand(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "--", "php", "artisan", "--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %v", runner.calls)
	}
	last := runner.calls[len(runner.calls)-1]
	if !strings.Contains(last, "laravel.test") || !strings.HasSuffix(last, "php artisan --version") {
		t.Errorf("call = %q", last)
	}
}

func TestExecRemoteEnvDetection(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	var gotCfg deploy.SSHConfig
	var gotDir string
	var gotArgs []string
	orig := remoteExecFn
	remoteExecFn = func(ctx context.Context, cfg deploy.SSHConfig, dir string, args []string) error {
		gotCfg, gotDir, gotArgs = cfg, dir, args
		return nil
	}
	defer func() { remoteExecFn = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "production", "php", "artisan", "migrate"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotCfg.Host != "h" || gotCfg.User != "u" {
		t.Errorf("ssh config = %+v, want host=h user=u", gotCfg)
	}
	if gotDir != "/srv/x" {
		t.Errorf("dir = %q, want /srv/x", gotDir)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "php" || gotArgs[1] != "artisan" || gotArgs[2] != "migrate" {
		t.Errorf("args = %v, want [php artisan migrate]", gotArgs)
	}
}

func TestExecRemoteNoCommand(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "production"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no command given for env \"production\"") {
		t.Errorf("err = %v, want no-command error", err)
	}
}

func TestExecLocalWhenFirstArgNotEnv(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	// `--` required: cobra otherwise rejects `--version` as an unknown
	// flag before RunE runs (same pattern as TestExecBuildsCommand).
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "--", "php", "artisan", "--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(runner.calls) == 0 {
		t.Fatal("no docker calls for local exec")
	}
	last := runner.calls[len(runner.calls)-1]
	if !strings.Contains(last, "laravel.test") || !strings.HasSuffix(last, "php artisan --version") {
		t.Errorf("local exec call = %q", last)
	}
}

func TestExecRemoteExitCodePropagates(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	orig := remoteExecFn
	remoteExecFn = func(ctx context.Context, cfg deploy.SSHConfig, dir string, args []string) error {
		return &deploy.ExitError{Code: 7, Kind: deploy.KindUnknown, RemoteHost: "h", Err: fmt.Errorf("boom")}
	}
	defer func() { remoteExecFn = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	// `--` required: cobra otherwise rejects `-v` as an unknown flag
	// before RunE runs (same pattern as TestExecBuildsCommand).
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "--", "production", "php", "-v"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute = nil error, want exit error")
	}
	if got := ExitCode(err); got != 7 {
		t.Errorf("exit code = %d, want 7", got)
	}
}

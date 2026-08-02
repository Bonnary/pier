package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/tui"
)

func writeTestTOML(t *testing.T, dir string) string {
	t.Helper()
	toml := `[project]
name = "x"
domain = "x.example.com"

[stack]
type = "laravel"
php = "8.3"
node = "22"

[deploy.stage]
host = "s.example.com"
user = "deploy"
path = "/srv/x"
branch = "main"

[deploy.production]
host = "p.example.com"
user = "deploy"
path = "/srv/x"
branch = "main"
`
	p := filepath.Join(dir, "pier.toml")
	if err := os.WriteFile(p, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newBootstrapTestRoot(t *testing.T, dir string) (*bytes.Buffer, *bytes.Buffer, *config.Config) {
	t.Helper()
	p := writeTestTOML(t, dir)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	root := NewRootCmd(&out, &errOut)
	root.SetArgs([]string{"--config", p})
	_ = root
	return &out, &errOut, cfg
}

func TestResolveBootstrapEnvsArgs(t *testing.T) {
	_, _, cfg := newBootstrapTestRoot(t, t.TempDir())
	envs, err := resolveBootstrapEnvs(cfg, []string{"production"}, false)
	if err != nil {
		t.Fatalf("resolveBootstrapEnvs: %v", err)
	}
	if len(envs) != 1 || envs[0] != "production" {
		t.Errorf("envs = %v, want [production]", envs)
	}
}

func TestResolveBootstrapEnvsAllSorted(t *testing.T) {
	_, _, cfg := newBootstrapTestRoot(t, t.TempDir())
	envs, err := resolveBootstrapEnvs(cfg, nil, true)
	if err != nil {
		t.Fatalf("resolveBootstrapEnvs: %v", err)
	}
	want := []string{"production", "stage"}
	if !equalStrings(envs, want) {
		t.Errorf("envs = %v, want %v (sorted)", envs, want)
	}
}

func TestResolveBootstrapEnvsArgsAndAllConflict(t *testing.T) {
	_, _, cfg := newBootstrapTestRoot(t, t.TempDir())
	if _, err := resolveBootstrapEnvs(cfg, []string{"stage"}, true); err == nil {
		t.Error("args + --all = nil error, want error")
	}
}

func TestResolveBootstrapEnvsUnknownEnv(t *testing.T) {
	_, _, cfg := newBootstrapTestRoot(t, t.TempDir())
	_, err := resolveBootstrapEnvs(cfg, []string{"nope"}, false)
	if err == nil || !contains(err.Error(), "no [deploy.nope]") {
		t.Errorf("err = %v, want no-[deploy.nope] error", err)
	}
}

func TestResolveBootstrapEnvsNoArgsNoTTY(t *testing.T) {
	origTTY := tuiForTest
	tuiForTest = func() bool { return false }
	defer func() { tuiForTest = origTTY }()
	_, _, cfg := newBootstrapTestRoot(t, t.TempDir())
	if _, err := resolveBootstrapEnvs(cfg, nil, false); err == nil {
		t.Error("no args, no TTY = nil error, want error")
	}
}

func TestResolveBootstrapEnvsNoArgsPicker(t *testing.T) {
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	origPick := pickEnvTUI
	pickEnvTUI = func(labels []string) (int, error) {
		if len(labels) != 2 {
			t.Errorf("picker labels = %v, want 2 entries", labels)
		}
		if labels[0] != "production (p.example.com)" {
			t.Errorf("picker labels[0] = %q, want %q", labels[0], "production (p.example.com)")
		}
		return 1, nil // stage
	}
	defer func() { pickEnvTUI = origPick }()
	_, _, cfg := newBootstrapTestRoot(t, t.TempDir())
	envs, err := resolveBootstrapEnvs(cfg, nil, false)
	if err != nil {
		t.Fatalf("resolveBootstrapEnvs: %v", err)
	}
	if len(envs) != 1 || envs[0] != "stage" {
		t.Errorf("envs = %v, want [stage]", envs)
	}
}

func TestRunBootstrapSkipsBootstrapped(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	origProbe := probeEnvFn
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) { return true, nil }
	defer func() { probeEnvFn = origProbe }()
	called := false
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		called = true
		return nil
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
	origPwd := readSudoPwd
	readSudoPwd = func(prompt string) (string, error) { return "pw", nil }
	defer func() { readSudoPwd = origPwd }()

	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap", "stage"})
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if called {
		t.Error("bootstrapEnvFn called for already-bootstrapped server, want skip")
	}
	if !contains(out.String(), "already bootstrapped — skipping") {
		t.Errorf("output = %q, want skip message", out.String())
	}
}

func TestRunBootstrapProvisions(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	origProbe := probeEnvFn
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) { return false, nil }
	defer func() { probeEnvFn = origProbe }()
	var gotPW string
	var gotOpts deploy.BootstrapOpts
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		gotPW = pw
		gotOpts = opts
		return nil
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
	origPwd := readSudoPwd
	readSudoPwd = func(prompt string) (string, error) { return "sekrit", nil }
	defer func() { readSudoPwd = origPwd }()

	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap", "stage"})
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if gotPW != "sekrit" {
		t.Errorf("password = %q, want sekrit", gotPW)
	}
	if gotOpts.User != "deploy" || gotOpts.Force {
		t.Errorf("opts = %+v, want {User: deploy, Force: false}", gotOpts)
	}
	if !contains(out.String(), "stage: done") {
		t.Errorf("output = %q, want done message", out.String())
	}
}

func TestRunBootstrapRetriesWrongPasswordOnce(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	origProbe := probeEnvFn
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) { return false, nil }
	defer func() { probeEnvFn = origProbe }()
	attempts := 0
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		attempts++
		if attempts == 1 {
			return deploy.ErrSudoWrongPassword
		}
		return nil
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
	pwds := []string{"first", "second"}
	origPwd := readSudoPwd
	readSudoPwd = func(prompt string) (string, error) {
		pw := pwds[0]
		pwds = pwds[1:]
		return pw, nil
	}
	defer func() { readSudoPwd = origPwd }()

	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap", "stage"})
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if attempts != 2 {
		t.Errorf("bootstrapEnvFn attempts = %d, want 2", attempts)
	}
}

func TestRunBootstrapNoEnvGiven(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	origTTY := tuiForTest
	tuiForTest = func() bool { return false }
	defer func() { tuiForTest = origTTY }()
	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil || !contains(err.Error(), "env") {
		t.Errorf("err = %v, want no-env error", err)
	}
}

func TestRunBootstrapNotInSudoersGivesGuidance(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	origProbe := probeEnvFn
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) { return false, nil }
	defer func() { probeEnvFn = origProbe }()
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		return deploy.ErrSudoNotSudoers
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
	origPwd := readSudoPwd
	readSudoPwd = func(prompt string) (string, error) { return "pw", nil }
	defer func() { readSudoPwd = origPwd }()

	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap", "stage"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil || !contains(err.Error(), "sudoers") || !contains(err.Error(), "s.example.com") {
		t.Errorf("err = %v, want sudoers guidance naming the host", err)
	}
}

func TestRunBootstrapForceSkipsProbe(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	probed := false
	origProbe := probeEnvFn
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) {
		probed = true
		return true, nil
	}
	defer func() { probeEnvFn = origProbe }()
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		if !opts.Force {
			t.Error("opts.Force = false, want true")
		}
		return nil
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
	origPwd := readSudoPwd
	readSudoPwd = func(prompt string) (string, error) { return "pw", nil }
	defer func() { readSudoPwd = origPwd }()

	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap", "--force", "stage"})
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		t.Fatalf("bootstrap --force: %v", err)
	}
	if probed {
		t.Error("probeEnvFn called with --force, want skipped")
	}
}

func TestBootstrapAbortMapsToCleanExit(t *testing.T) {
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	origPick := pickEnvTUI
	pickEnvTUI = func(labels []string) (int, error) { return -1, tui.ErrAborted }
	defer func() { pickEnvTUI = origPick }()
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	var out bytes.Buffer
	root := NewRootCmd(&out, &out)
	root.SetArgs([]string{"--config", p, "bootstrap"})
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		t.Errorf("bootstrap cancel = %v, want nil (clean exit 0)", err)
	}
}

func TestRunBootstrapStreamsOutput(t *testing.T) {
	dir := t.TempDir()
	p := writeTestTOML(t, dir)
	origProbe := probeEnvFn
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) { return false, nil }
	defer func() { probeEnvFn = origProbe }()
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		if opts.OnStdout == nil || opts.OnStderr == nil {
			t.Fatal("OnStdout/OnStderr callbacks not wired")
		}
		opts.OnStdout("installing docker...")
		opts.OnStderr("warning: x")
		return nil
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
	origPwd := readSudoPwd
	readSudoPwd = func(prompt string) (string, error) { return "pw", nil }
	defer func() { readSudoPwd = origPwd }()

	var out, errOut bytes.Buffer
	root := NewRootCmd(&out, &errOut)
	root.SetArgs([]string{"--config", p, "bootstrap", "stage"})
	root.SilenceUsage = true
	if err := root.Execute(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !contains(out.String(), "installing docker...") {
		t.Errorf("stdout = %q, want streamed install line", out.String())
	}
	if !contains(errOut.String(), "warning: x") {
		t.Errorf("stderr = %q, want streamed warning line", errOut.String())
	}
	if contains(out.String(), "warning: x") {
		t.Errorf("stdout = %q, must not contain stderr warning line", out.String())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

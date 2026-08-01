package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
	"golang.org/x/crypto/ssh"
)

// scriptedRunner answers commands by substring match. A command that
// matches no step fails with a generic ExitError (like a missing
// binary). runErr simulates a session-level failure (non-exit).
type scriptedRunner struct {
	cmds   []string
	stdins []string
	script []scriptedStep
	runErr error
}

type scriptedStep struct {
	match  string
	ok     bool
	stdout string
	stderr string
}

func (f *scriptedRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	return f.respond(cmd, "")
}

func (f *scriptedRunner) RunStdin(ctx context.Context, cmd string, stdin string) ([]byte, []byte, error) {
	return f.respond(cmd, stdin)
}

func (f *scriptedRunner) respond(cmd, stdin string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	f.stdins = append(f.stdins, stdin)
	if f.runErr != nil {
		return nil, nil, f.runErr
	}
	for _, s := range f.script {
		if strings.Contains(cmd, s.match) {
			if !s.ok {
				return []byte(s.stdout), []byte(s.stderr), &ssh.ExitError{}
			}
			return []byte(s.stdout), []byte(s.stderr), nil
		}
	}
	return nil, nil, &ssh.ExitError{}
}

// fakeConn is a dialBootstrap result: a scriptedRunner that also
// records Close.
type fakeConn struct {
	*scriptedRunner
	closed bool
}

func (f *fakeConn) Close() error { f.closed = true; return nil }

func TestProbeBootstrapWhenBootstrapped(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "docker", ok: true}}}
	ok, err := ProbeBootstrap(context.Background(), r)
	if err != nil {
		t.Fatalf("ProbeBootstrap: %v", err)
	}
	if !ok {
		t.Error("ProbeBootstrap = false, want true")
	}
}

func TestProbeBootstrapWhenNotBootstrapped(t *testing.T) {
	r := &scriptedRunner{} // no step matches → exit 1
	ok, err := ProbeBootstrap(context.Background(), r)
	if err != nil {
		t.Fatalf("ProbeBootstrap: %v", err)
	}
	if ok {
		t.Error("ProbeBootstrap = true, want false")
	}
}

func TestProbeBootstrapSessionFailure(t *testing.T) {
	boom := errors.New("connection reset")
	r := &scriptedRunner{runErr: boom}
	_, err := ProbeBootstrap(context.Background(), r)
	if err == nil {
		t.Fatal("ProbeBootstrap(session failure) = nil error, want non-nil")
	}
}

func TestValidateSudoOK(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "sudo -S -v", ok: true}}}
	if err := ValidateSudo(context.Background(), r, "sekrit"); err != nil {
		t.Fatalf("ValidateSudo: %v", err)
	}
	if len(r.stdins) != 1 || r.stdins[0] != "sekrit\n" {
		t.Errorf("stdins = %q, want [\"sekrit\\n\"]", r.stdins)
	}
}

func TestValidateSudoWrongPassword(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{
		match: "sudo -S -v", ok: false, stderr: "Sorry, try again.",
	}}}
	err := ValidateSudo(context.Background(), r, "nope")
	if !errors.Is(err, ErrSudoWrongPassword) {
		t.Errorf("ValidateSudo = %v, want ErrSudoWrongPassword", err)
	}
}

func TestValidateSudoNotInSudoers(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{
		match: "sudo -S -v", ok: false,
		stderr: "deploy is not in the sudoers file. This incident will be reported.",
	}}}
	err := ValidateSudo(context.Background(), r, "pw")
	if !errors.Is(err, ErrSudoNotSudoers) {
		t.Errorf("ValidateSudo = %v, want ErrSudoNotSudoers", err)
	}
}

func TestProvisionRunsInstallAndUsermod(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{
		{match: "get.docker.com", ok: true},
		{match: "usermod -aG docker", ok: true},
	}}
	if err := Provision(context.Background(), r, "pw", "deploy"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	joined := strings.Join(r.cmds, "\n")
	if !strings.Contains(joined, "sudo -S sh -c") {
		t.Errorf("commands not run through sudo -S sh -c:\n%s", joined)
	}
	if !strings.Contains(joined, "curl -fsSL https://get.docker.com | sh") {
		t.Errorf("install command missing get.docker.com:\n%s", joined)
	}
	if !strings.Contains(joined, `usermod -aG docker "deploy"`) {
		t.Errorf("usermod command missing quoted user:\n%s", joined)
	}
	for _, s := range r.stdins {
		if s != "pw\n" {
			t.Errorf("stdin = %q, want %q", s, "pw\n")
		}
	}
}

func TestVerifyBootstrapChecksDaemonPluginGroup(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "getent group docker", ok: true}}}
	if err := VerifyBootstrap(context.Background(), r, "pw", "deploy"); err != nil {
		t.Fatalf("VerifyBootstrap: %v", err)
	}
	joined := strings.Join(r.cmds, "\n")
	for _, want := range []string{"docker info", "docker compose version", `grep -qw "deploy"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("verify command missing %q:\n%s", want, joined)
		}
	}
}

func TestBootstrapEnvSkipsWhenBootstrapped(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
			{match: "docker", ok: true},
		}}}, nil
	}
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw", BootstrapOpts{User: "u"})
	if !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Errorf("BootstrapEnv = %v, want ErrAlreadyBootstrapped", err)
	}
}

func TestBootstrapEnvProvisionsWhenNeeded(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true},
	}}}
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return conn, nil }
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw", BootstrapOpts{User: "u"})
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	if !conn.closed {
		t.Error("BootstrapEnv did not Close the connection")
	}
}

func TestBootstrapEnvForceReprovisions(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
			{match: "docker", ok: true}, // probe would pass, but Force skips it
			{match: "sudo -S -v", ok: true},
			{match: "get.docker.com", ok: true},
			{match: "usermod", ok: true},
			{match: "getent group docker", ok: true},
		}}}, nil
	}
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw", BootstrapOpts{User: "u", Force: true})
	if err != nil {
		t.Fatalf("BootstrapEnv(force): %v", err)
	}
}

func TestProbeEnv(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{{match: "docker", ok: true}}}}
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return conn, nil }
	ok, err := ProbeEnv(context.Background(), SSHConfig{Host: "h", User: "u"})
	if err != nil {
		t.Fatalf("ProbeEnv: %v", err)
	}
	if !ok {
		t.Error("ProbeEnv = false, want true")
	}
	if !conn.closed {
		t.Error("ProbeEnv did not Close the connection")
	}
}

func TestNotBootstrappedError(t *testing.T) {
	err := NotBootstrappedError("production")
	if !errors.Is(err, ErrNotBootstrapped) {
		t.Errorf("errors.Is(ErrNotBootstrapped) = false, want true")
	}
	if !strings.Contains(err.Error(), "pier bootstrap production") {
		t.Errorf("message %q missing hint `pier bootstrap production`", err.Error())
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitPreflight {
		t.Errorf("NotBootstrappedError is not an ExitPreflight ExitError")
	}
}

func TestPreflightRejectsUnbootstrappedServer(t *testing.T) {
	origDial := pipelineDial
	defer func() { pipelineDial = origDial }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{}} // no docker: probe fails
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return conn, nil
	}
	origProbe := pipelineProbe
	defer func() { pipelineProbe = origProbe }()
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) {
		return ProbeBootstrap(ctx, conn.scriptedRunner)
	}
	p := &Pipeline{
		Env:       "production",
		DeployEnv: config.DeployConfig{Host: "h", User: "u", Path: "/srv/x"},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")},
	}
	client, err := p.preflight(context.Background())
	if err == nil {
		t.Fatal("preflight(unbootstrapped) = nil error, want NotBootstrappedError")
	}
	if !errors.Is(err, ErrNotBootstrapped) {
		t.Errorf("preflight err = %v, want ErrNotBootstrapped", err)
	}
	if client != nil {
		t.Error("preflight returned a client despite failure, want nil")
	}
	if !conn.closed {
		t.Error("preflight did not Close the client after a failed probe")
	}
}

func TestPreflightAcceptsBootstrappedServer(t *testing.T) {
	origDial := pipelineDial
	defer func() { pipelineDial = origDial }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{{match: "docker", ok: true}}}}
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return &Client{Config: cfg}, nil
	}
	origProbe := pipelineProbe
	defer func() { pipelineProbe = origProbe }()
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) {
		return ProbeBootstrap(ctx, conn.scriptedRunner)
	}
	p := &Pipeline{
		Env:       "production",
		DeployEnv: config.DeployConfig{Host: "h", User: "u", Path: "/srv/x"},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")},
	}
	client, err := p.preflight(context.Background())
	if err != nil {
		t.Fatalf("preflight(bootstrapped): %v", err)
	}
	if client == nil {
		t.Error("preflight returned nil client, want non-nil")
	}
	client.Close()
}

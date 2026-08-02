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

func (f *scriptedRunner) RunStreamStdin(ctx context.Context, cmd, stdin string, onStdout, onStderr func(string)) ([]byte, error) {
	stdout, stderr, err := f.respond(cmd, stdin)
	for _, l := range splitLines(stdout) {
		if onStdout != nil {
			onStdout(l)
		}
	}
	for _, l := range splitLines(stderr) {
		if onStderr != nil {
			onStderr(l)
		}
	}
	return stderr, err
}

func splitLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
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
	if err := ValidateSudo(context.Background(), r, "sekrit", nil, nil); err != nil {
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
	err := ValidateSudo(context.Background(), r, "nope", nil, nil)
	if !errors.Is(err, ErrSudoWrongPassword) {
		t.Errorf("ValidateSudo = %v, want ErrSudoWrongPassword", err)
	}
}

func TestValidateSudoNotInSudoers(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{
		match: "sudo -S -v", ok: false,
		stderr: "deploy is not in the sudoers file. This incident will be reported.",
	}}}
	err := ValidateSudo(context.Background(), r, "pw", nil, nil)
	if !errors.Is(err, ErrSudoNotSudoers) {
		t.Errorf("ValidateSudo = %v, want ErrSudoNotSudoers", err)
	}
}

func TestProvisionRunsInstallAndUsermod(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{
		{match: "get.docker.com", ok: true},
		{match: "usermod -aG docker", ok: true},
	}}
	if err := Provision(context.Background(), r, "pw", "deploy", nil, nil); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	joined := strings.Join(r.cmds, "\n")
	if !strings.Contains(joined, "sudo -S -p '' sh -c") {
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

func TestRunSudoEscapesApostrophes(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "sudo -S -p '' sh -c", ok: true}}}
	err := runSudo(context.Background(), r, "pw", `usermod -aG docker "O'Brien"`, nil, nil)
	if err != nil {
		t.Fatalf("runSudo: %v", err)
	}
	if len(r.cmds) != 1 {
		t.Fatalf("cmds = %d commands, want 1", len(r.cmds))
	}
	if !strings.Contains(r.cmds[0], `O'\''Brien`) {
		t.Errorf("command %q missing POSIX apostrophe escape", r.cmds[0])
	}
	if strings.Contains(r.cmds[0], `O'Brien`) {
		t.Errorf("command %q leaks an unescaped apostrophe", r.cmds[0])
	}
}

func TestVerifyBootstrapChecksDaemonPluginGroup(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "getent group docker", ok: true}}}
	if err := VerifyBootstrap(context.Background(), r, "pw", "deploy", nil, nil); err != nil {
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
	origEnsure := pipelineEnsurePath
	defer func() { pipelineEnsurePath = origEnsure }()
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error {
		if path != "/srv/x" {
			t.Errorf("ensure path = %q, want %q", path, "/srv/x")
		}
		return nil
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

func TestPreflightEnsuresDeployPath(t *testing.T) {
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
	ensured := ""
	origEnsure := pipelineEnsurePath
	defer func() { pipelineEnsurePath = origEnsure }()
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error {
		ensured = path
		return nil
	}
	p := &Pipeline{
		Env:       "production",
		DeployEnv: config.DeployConfig{Host: "h", User: "u", Path: "/srv/x"},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")},
	}
	client, err := p.preflight(context.Background())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if ensured != "/srv/x" {
		t.Errorf("ensured path = %q, want /srv/x", ensured)
	}
	client.Close()
}

func TestPreflightRejectsUnwritableDeployPath(t *testing.T) {
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
	origEnsure := pipelineEnsurePath
	defer func() { pipelineEnsurePath = origEnsure }()
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error {
		return errors.New("mkdir /srv/x: permission denied")
	}
	p := &Pipeline{
		Env:       "production",
		DeployEnv: config.DeployConfig{Host: "h", User: "u", Path: "/srv/x"},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")},
	}
	client, err := p.preflight(context.Background())
	if err == nil {
		t.Fatal("preflight(unwritable path) = nil error, want error")
	}
	for _, want := range []string{"/srv/x", "on h is not writable for u", "sudo mkdir -p /srv/x", "sudo chown u:u /srv/x", "pier bootstrap production"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q missing %q", err.Error(), want)
		}
	}
	if client != nil {
		t.Error("preflight returned a client despite failure, want nil")
	}
}

func equalStr(a, b []string) bool {
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

func TestRunSudoStreamsOutputAndClassifies(t *testing.T) {
	var out, errOut []string
	r := &scriptedRunner{script: []scriptedStep{{
		match: "sudo -S -p '' sh -c", ok: false,
		stdout: "Downloading...\nExtracting...\n",
		stderr: "Sorry, try again.\n",
	}}}
	err := runSudo(context.Background(), r, "pw", "apt-get update",
		func(l string) { out = append(out, l) },
		func(l string) { errOut = append(errOut, l) })
	if !errors.Is(err, ErrSudoWrongPassword) {
		t.Fatalf("runSudo = %v, want ErrSudoWrongPassword", err)
	}
	if !equalStr(out, []string{"Downloading...", "Extracting..."}) {
		t.Errorf("stdout lines = %v, want [Downloading... Extracting...]", out)
	}
	if !equalStr(errOut, []string{"Sorry, try again."}) {
		t.Errorf("stderr lines = %v, want [Sorry, try again.]", errOut)
	}
	if len(r.stdins) != 1 || r.stdins[0] != "pw\n" {
		t.Errorf("stdins = %q, want [pw\n]", r.stdins)
	}
}

func TestRunSudoSuppressesPrompt(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "sudo -S -p '' sh -c", ok: true}}}
	if err := runSudo(context.Background(), r, "pw", "true", nil, nil); err != nil {
		t.Fatalf("runSudo: %v", err)
	}
	if len(r.cmds) != 1 || !strings.Contains(r.cmds[0], "sudo -S -p '' sh -c") {
		t.Errorf("command %q missing -p '' prompt suppression", r.cmds)
	}
}

func TestProvisionForwardsCallbacks(t *testing.T) {
	var lines []string
	r := &scriptedRunner{script: []scriptedStep{
		{match: "get.docker.com", ok: true, stdout: "install line\n"},
		{match: "usermod", ok: true},
	}}
	err := Provision(context.Background(), r, "pw", "deploy",
		func(l string) { lines = append(lines, l) },
		func(l string) { lines = append(lines, "ERR:"+l) })
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !equalStr(lines, []string{"install line"}) {
		t.Errorf("callback lines = %v, want [install line]", lines)
	}
}

func TestBootstrapEnvStreamsOutput(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "get.docker.com", ok: true, stdout: "installing\n"},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true, stdout: "verify ok\n"},
	}}}
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return conn, nil }
	var out, errOut []string
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw", BootstrapOpts{
		User:     "u",
		OnStdout: func(l string) { out = append(out, l) },
		OnStderr: func(l string) { errOut = append(errOut, l) },
	})
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	if !equalStr(out, []string{"installing", "verify ok"}) {
		t.Errorf("stdout lines = %v, want [installing verify ok]", out)
	}
	if len(errOut) != 0 {
		t.Errorf("stderr lines = %v, want none", errOut)
	}
}

// TestScriptedRunnerStreamsLines pins the stdinRunner.RunStreamStdin
// contract the bootstrap layer relies on: stdin is piped, stdout and
// stderr lines reach their callbacks, and stderr is returned whole
// for error classification.
func TestScriptedRunnerStreamsLines(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{
		match: "sudo -S -p ''", ok: true,
		stdout: "one\ntwo\n",
		stderr: "warn\n",
	}}}
	var out, errOut []string
	stderr, err := r.RunStreamStdin(context.Background(), "sudo -S -p '' true", "pw\n",
		func(l string) { out = append(out, l) },
		func(l string) { errOut = append(errOut, l) })
	if err != nil {
		t.Fatalf("RunStreamStdin: %v", err)
	}
	if !equalStr(out, []string{"one", "two"}) {
		t.Errorf("stdout lines = %v, want [one two]", out)
	}
	if !equalStr(errOut, []string{"warn"}) {
		t.Errorf("stderr lines = %v, want [warn]", errOut)
	}
	if string(stderr) != "warn\n" {
		t.Errorf("captured stderr = %q, want %q", stderr, "warn\n")
	}
	if len(r.stdins) != 1 || r.stdins[0] != "pw\n" {
		t.Errorf("stdins = %q, want [pw\n]", r.stdins)
	}
}

func TestQuoteShell(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/srv/x", "'/srv/x'"},
		{"O'Brien/app", `'O'\''Brien/app'`},
		{"", "''"},
	} {
		if got := quoteShell(tc.in); got != tc.want {
			t.Errorf("quoteShell(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRunSudoUsesQuoteShell(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "sh -c", ok: true}}}
	if err := runSudo(context.Background(), r, "pw", `usermod -aG docker "O'Brien"`, nil, nil); err != nil {
		t.Fatalf("runSudo: %v", err)
	}
	if !strings.Contains(r.cmds[0], `O'\''Brien`) {
		t.Errorf("command %q missing POSIX apostrophe escape", r.cmds[0])
	}
}

func TestProvisionDeployPathCommand(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "chown", ok: true}}}
	if err := ProvisionDeployPath(context.Background(), r, "pw", "deploy", "/srv/x", nil, nil); err != nil {
		t.Fatalf("ProvisionDeployPath: %v", err)
	}
	joined := strings.Join(r.cmds, "\n")
	for _, want := range []string{
		"sudo -S -p '' sh -c",
		`mkdir -p '\''/srv/x'\''`,
		`chown "deploy":"deploy" '\''/srv/x'\''`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("provision command missing %q:\n%s", want, joined)
		}
	}
	for _, s := range r.stdins {
		if s != "pw\n" {
			t.Errorf("stdin = %q, want %q", s, "pw\n")
		}
	}
}

func TestProvisionDeployPathEscapesApostrophe(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "chown", ok: true}}}
	if err := ProvisionDeployPath(context.Background(), r, "pw", "u", "/O'Brien/x", nil, nil); err != nil {
		t.Fatalf("ProvisionDeployPath: %v", err)
	}
	if !strings.Contains(r.cmds[0], `mkdir -p '\''/O'\''\'\'''\''Brien/x'\''`) {
		t.Errorf("command %q missing escaped path", r.cmds[0])
	}
}

func TestEnsureDeployPathCommand(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{match: "mkdir -p", ok: true}}}
	if err := EnsureDeployPath(context.Background(), r, "/srv/x"); err != nil {
		t.Fatalf("EnsureDeployPath: %v", err)
	}
	if len(r.cmds) != 1 || r.cmds[0] != "mkdir -p '/srv/x'" {
		t.Errorf("commands = %q, want [mkdir -p '/srv/x']", r.cmds)
	}
}

func TestProvisionDeployPathSudoFailureClassifies(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{
		match: "chown", ok: false, stderr: "Sorry, try again.",
	}}}
	err := ProvisionDeployPath(context.Background(), r, "nope", "u", "/srv/x", nil, nil)
	if !errors.Is(err, ErrSudoWrongPassword) {
		t.Errorf("ProvisionDeployPath = %v, want ErrSudoWrongPassword", err)
	}
}

func TestBootstrapEnvCreatesDeployPath(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "chown", ok: true},
		{match: "getent group docker", ok: true},
	}}}
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return conn, nil }
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw",
		BootstrapOpts{User: "u", Path: "/srv/x"})
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	var chownIdx, usermodIdx, verifyIdx int
	for i, cmd := range conn.cmds {
		switch {
		case strings.Contains(cmd, "chown"):
			chownIdx = i
		case strings.Contains(cmd, "usermod"):
			usermodIdx = i
		case strings.Contains(cmd, "getent group docker"):
			verifyIdx = i
		}
	}
	if !(usermodIdx < chownIdx && chownIdx < verifyIdx) {
		t.Errorf("step order wrong: usermod=%d chown=%d verify=%d, want usermod < chown < verify",
			usermodIdx, chownIdx, verifyIdx)
	}
}

func TestBootstrapEnvSkipsPathWhenEmpty(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true},
	}}}
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return conn, nil }
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw",
		BootstrapOpts{User: "u"})
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	for _, cmd := range conn.cmds {
		if strings.Contains(cmd, "chown") || strings.Contains(cmd, "mkdir") {
			t.Errorf("unexpected path command with empty Path: %s", cmd)
		}
	}
}

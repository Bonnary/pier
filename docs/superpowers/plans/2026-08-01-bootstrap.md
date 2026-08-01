# `pier bootstrap` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pier bootstrap [env...]` — one-time server provisioning that installs Docker + the compose plugin over SSH (handling password-protected sudo with a hidden one-time prompt) and makes every later `pier deploy` non-interactive.

**Architecture:** The `deploy` package owns all remote logic (`ProbeBootstrap`, `ValidateSudo`, `Provision`, `VerifyBootstrap`, `BootstrapEnv`) as standalone functions over the existing `runner` abstraction, plus a dial seam for tests. The `cli` package owns command wiring, env resolution (args → `--all` → TUI picker), and the hidden password prompt. `tui` gains one small single-picker helper. The deploy pipeline's preflight gains a fail-fast probe.

**Tech Stack:** Go 1.25, cobra, bubbletea, `golang.org/x/crypto/ssh` (already used), `golang.org/x/term` (already in go.mod at v0.45.0 — **no go.mod changes**).

## Global Constraints

- Module: `github.com/Bonnary/pier`; Go 1.25.0. No new dependencies.
- Every exported symbol needs a Go doc comment (repo rule, see README "Go doc").
- Boundary rules: `cli` never runs SSH commands directly — it calls `deploy` package functions. `deploy` never reads the TUI.
- Run `go test -race ./...` and `golangci-lint run` before any commit; CI runs unit tests on macOS/Linux/Windows and integration tests (tag `integration`) on Linux.
- The sudo password is NEVER stored, logged, or passed on a command line — only via `RunStdin` (session stdin pipe). Tests use seams; CI never prompts.
- Follow the spec at `docs/superpowers/specs/2026-08-01-bootstrap-design.md`.

---

### Task 1: SSH `RunStdin` + `stdinRunner` interface

**Files:**
- Modify: `internal/deploy/ssh.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Client.RunStdin(ctx, cmd string, stdin string) ([]byte, []byte, error)` — same contract as `Run` plus a stdin string. `stdinRunner` interface (unexported): `Run` + `RunStdin`. Task 2's `ValidateSudo`/`Provision`/`VerifyBootstrap` depend on it.

- [ ] **Step 1: Write the failing interface + compile assertion**

Add to `internal/deploy/ssh.go` after the existing `runner` interface (line 137):

```go
// stdinRunner is the subset of *Client that bootstrap needs: plain
// Run plus RunStdin for piping the sudo password.
type stdinRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	RunStdin(ctx context.Context, cmd string, stdin string) ([]byte, []byte, error)
}
```

- [ ] **Step 2: Run the build to verify it fails**

Run: `go build ./...`
Expected: FAIL — `cannot use *Client (type *Client) as type stdinRunner: missing method RunStdin` (the `runner`/`stdinRunner` packages compile but nothing references it yet; add the assertion below to force the error — `go vet` catches nothing here).

If the build somehow succeeds (no reference yet), add this temporary compile assertion to the same file:

```go
var _ stdinRunner = (*Client)(nil)
```

Then run `go build ./...` again — must FAIL with the missing-method error.

- [ ] **Step 3: Implement `RunStdin`**

Add `"strings"` to the imports of `internal/deploy/ssh.go` and the method after `Run` (line 100):

```go
// RunStdin executes cmd on the remote host with stdin piped from the
// given string. Used by bootstrap to feed the sudo password to
// `sudo -S` without it ever appearing in the command string (and
// thus in remote process listings or logs).
func (c *Client) RunStdin(ctx context.Context, cmd string, stdin string) ([]byte, []byte, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdin = strings.NewReader(stdin)
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if err := sess.Run(cmd); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}
```

Keep the `var _ stdinRunner = (*Client)(nil)` assertion (now the real thing — a permanent compile-time check).

- [ ] **Step 4: Run the build and full test suite**

Run: `go build ./... && go test -race ./...`
Expected: PASS (existing tests; the new method has no unit test — `Client` is concrete and the repo convention is no unit tests for it, verified at integration level in Task 7).

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/ssh.go
git commit -m "feat(deploy): add Client.RunStdin for password-piped sudo"
```

---

### Task 2: Bootstrap core — probe, validate, provision, verify, `BootstrapEnv`

**Files:**
- Create: `internal/deploy/bootstrap.go`
- Test: `internal/deploy/bootstrap_test.go`

**Interfaces:**
- Consumes: `runner` and `stdinRunner` (Task 1), `Client` (`*Client` satisfies both), `Dial(ctx, cfg) (*Client, error)` from `ssh.go`, `ErrPreflight`/`PreflightError` from `errors.go`.
- Produces:
  - `ErrNotBootstrapped`, `ErrAlreadyBootstrapped`, `ErrSudoWrongPassword`, `ErrSudoNotSudoers` (error sentinels)
  - `ProbeBootstrap(ctx, r runner) (bool, error)`
  - `ValidateSudo(ctx, r stdinRunner, password string) error`
  - `Provision(ctx, r stdinRunner, password, user string) error`
  - `VerifyBootstrap(ctx, r stdinRunner, password, user string) error`
  - `BootstrapOpts{Force bool; User string}`
  - `BootstrapEnv(ctx, cfg SSHConfig, password string, opts BootstrapOpts) error`
  - `ProbeEnv(ctx, cfg SSHConfig) (bool, error)`
  - `NotBootstrappedError(env string) error`
  - package-private `dialBootstrap` seam: `var dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return Dial(ctx, cfg) }` with `type bootstrapConn interface { stdinRunner; Close() error }`

- [ ] **Step 1: Write the failing tests**

Create `internal/deploy/bootstrap_test.go`:

```go
package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

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
				return []byte(s.stdout), []byte(s.stderr), &ssh.ExitError{ExitStatus: 1}
			}
			return []byte(s.stdout), []byte(s.stderr), nil
		}
	}
	return nil, nil, &ssh.ExitError{ExitStatus: 1}
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/deploy/ -run 'TestProbe|TestValidate|TestProvision|TestVerify|TestBootstrap|TestNotBootstrapped'`
Expected: FAIL — `undefined: ProbeBootstrap` (package compiles only after Task 1's `stdinRunner` exists; Task 1 is already merged, so the error is the missing functions).

- [ ] **Step 3: Write the implementation**

Create `internal/deploy/bootstrap.go`:

```go
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Sentinel errors for the bootstrap flow. The CLI inspects these
// with errors.Is to decide whether to skip, re-prompt, or abort.
var (
	// ErrNotBootstrapped wraps the deploy-side fail-fast: the server
	// is not provisioned and `pier bootstrap` must run first.
	ErrNotBootstrapped = errors.New("server not bootstrapped")
	// ErrAlreadyBootstrapped is returned by BootstrapEnv when the
	// probe passes; the CLI prints "already bootstrapped — skipping".
	ErrAlreadyBootstrapped = errors.New("already bootstrapped")
	// ErrSudoWrongPassword is returned by ValidateSudo when the sudo
	// password is rejected. The CLI re-prompts once.
	ErrSudoWrongPassword = errors.New("wrong sudo password")
	// ErrSudoNotSudoers is returned when the user has no sudo rights
	// at all; the CLI aborts with instructions.
	ErrSudoNotSudoers = errors.New("deploy user has no sudo rights")
)

// ProbeBootstrap reports whether the deploy user can run docker
// without sudo: `command -v docker` and `docker info` must both
// succeed. A non-zero exit counts as "not bootstrapped" (false, nil);
// only session-level failures (connection resets, etc.) return a
// non-nil error.
func ProbeBootstrap(ctx context.Context, r runner) (bool, error) {
	cmd := "command -v docker && docker info"
	_, stderr, err := r.Run(ctx, cmd)
	if err == nil {
		return true, nil
	}
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) {
		return false, fmt.Errorf("%w: probe %q: %v (stderr: %s)", ErrPreflight, cmd, err, bytes.TrimSpace(stderr))
	}
	return false, nil
}

// classifySudoErr maps a failed `sudo -S` run to its sentinel,
// distinguishing a wrong password from a user with no sudo rights.
func classifySudoErr(stderr []byte, err error) error {
	s := strings.ToLower(string(stderr))
	switch {
	case strings.Contains(s, "not in the sudoers") || strings.Contains(s, "may not run sudo"):
		return ErrSudoNotSudoers
	case strings.Contains(s, "sorry, try again") || strings.Contains(s, "incorrect password") || strings.Contains(s, "authentication failure"):
		return ErrSudoWrongPassword
	default:
		return fmt.Errorf("sudo failed: %v (stderr: %s)", err, bytes.TrimSpace(stderr))
	}
}

// runSudo executes cmd via `sudo -S sh -c '<cmd>'` with the password
// piped on the session's stdin — never on the command line.
func runSudo(ctx context.Context, r stdinRunner, password, cmd string) error {
	full := fmt.Sprintf("sudo -S sh -c %s", strconv.Quote(cmd))
	_, stderr, err := r.RunStdin(ctx, full, password+"\n")
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}

// ValidateSudo proves the password works by running `sudo -S -v`.
// Returns ErrSudoWrongPassword or ErrSudoNotSudoers on failure.
func ValidateSudo(ctx context.Context, r stdinRunner, password string) error {
	_, stderr, err := r.RunStdin(ctx, "sudo -S -v", password+"\n")
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}

// Provision installs Docker Engine + the compose plugin with the
// official get.docker.com script and adds user to the docker group,
// both under sudo. Idempotent — safe to re-run with --force.
func Provision(ctx context.Context, r stdinRunner, password, user string) error {
	if err := runSudo(ctx, r, password, "curl -fsSL https://get.docker.com | sh"); err != nil {
		return fmt.Errorf("install docker: %w", err)
	}
	if err := runSudo(ctx, r, password, "usermod -aG docker "+strconv.Quote(user)); err != nil {
		return fmt.Errorf("add user to docker group: %w", err)
	}
	return nil
}

// VerifyBootstrap confirms the daemon runs, the compose plugin is
// present, and the user is a member of the docker group. Group
// membership only applies to new SSH connections, so the group file
// is checked directly (getent) instead of re-running docker.
func VerifyBootstrap(ctx context.Context, r stdinRunner, password, user string) error {
	cmd := fmt.Sprintf("docker info && docker compose version && getent group docker | grep -qw %s", strconv.Quote(user))
	if err := runSudo(ctx, r, password, cmd); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	return nil
}

// bootstrapConn is the subset of *Client that BootstrapEnv uses.
type bootstrapConn interface {
	stdinRunner
	Close() error
}

// dialBootstrap is a seam for tests to inject a fake connection.
var dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
	return Dial(ctx, cfg)
}

// BootstrapOpts is the parameter set for BootstrapEnv.
type BootstrapOpts struct {
	// Force skips the probe and re-provisions even when the server
	// is already bootstrapped.
	Force bool
	// User is the deploy user that gets docker group membership.
	User string
}

// BootstrapEnv runs the full one-time provisioning flow for one
// server: probe (unless Force), sudo validation, provision, verify.
// Returns ErrAlreadyBootstrapped when the probe passes and Force is
// false.
func BootstrapEnv(ctx context.Context, cfg SSHConfig, password string, opts BootstrapOpts) error {
	client, err := dialBootstrap(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	if !opts.Force {
		ok, err := ProbeBootstrap(ctx, client)
		if err != nil {
			return err
		}
		if ok {
			return ErrAlreadyBootstrapped
		}
	}
	if err := ValidateSudo(ctx, client, password); err != nil {
		return err
	}
	if err := Provision(ctx, client, password, opts.User); err != nil {
		return err
	}
	return VerifyBootstrap(ctx, client, password, opts.User)
}

// ProbeEnv dials cfg and runs the bootstrap probe. Convenience for
// the CLI's skip check before prompting for the password.
func ProbeEnv(ctx context.Context, cfg SSHConfig) (bool, error) {
	client, err := dialBootstrap(ctx, cfg)
	if err != nil {
		return false, err
	}
	defer client.Close()
	return ProbeBootstrap(ctx, client)
}

// NotBootstrappedError builds the deploy-side fail-fast error: the
// server was never provisioned, so `pier bootstrap <env>` must run
// first. Wrapped as a preflight error so the CLI exits with code 2.
func NotBootstrappedError(env string) error {
	return PreflightError(fmt.Errorf("%w: %s — run `pier bootstrap %s` first", ErrNotBootstrapped, env, env))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/deploy/`
Expected: PASS (all existing deploy tests + the 13 new bootstrap tests).

- [ ] **Step 5: Run the linter**

Run: `golangci-lint run`
Expected: PASS, no new findings.

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/bootstrap.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): bootstrap core — probe, sudo validate, provision, verify"
```

---

### Task 3: Deploy preflight fail-fast

**Files:**
- Modify: `internal/deploy/deploy.go:109-117`
- Test: `internal/deploy/bootstrap_test.go` (append)

**Interfaces:**
- Consumes: `ProbeBootstrap`, `NotBootstrappedError` (Task 2), `fakeConn`/`scriptedRunner` (defined in `bootstrap_test.go`, same package).
- Produces: `pier deploy` / `pier rollback` now abort during preflight with a clear hint instead of hanging on a hidden sudo prompt. Adds the `pipelineDial` seam so the preflight probe is unit-testable.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/bootstrap_test.go`:

```go
func TestPreflightRejectsUnbootstrappedServer(t *testing.T) {
	origDial := pipelineDial
	defer func() { pipelineDial = origDial }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{}} // no docker: probe fails
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (*Client, error) {
		return &Client{Config: cfg}, nil
	}
	origProbe := pipelineProbe
	defer func() { pipelineProbe = origProbe }()
	pipelineProbe = func(ctx context.Context, r runner) (bool, error) {
		return ProbeBootstrap(ctx, conn.scriptedRunner)
	}
	p := &Pipeline{
		Env:       "production",
		DeployEnv: config.DeployConfig{Host: "h", User: "u", Path: "/srv/x"},
		SSH:       SSHConfig{Host: "h", User: "u"},
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
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (*Client, error) {
		return &Client{Config: cfg}, nil
	}
	origProbe := pipelineProbe
	defer func() { pipelineProbe = origProbe }()
	pipelineProbe = func(ctx context.Context, r runner) (bool, error) {
		return ProbeBootstrap(ctx, conn.scriptedRunner)
	}
	p := &Pipeline{
		Env:       "production",
		DeployEnv: config.DeployConfig{Host: "h", User: "u", Path: "/srv/x"},
		SSH:       SSHConfig{Host: "h", User: "u"},
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
```

Add imports to `bootstrap_test.go`: `"github.com/Bonnary/pier/internal/config"` (and `errors` if not already imported — it is, from Task 2's tests).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/deploy/ -run 'TestPreflight'`
Expected: FAIL — `undefined: pipelineDial` / `undefined: pipelineProbe`.

- [ ] **Step 3: Implement the seams and the probe**

In `internal/deploy/deploy.go`, add near the top of the file (after the imports):

```go
// pipelineDial is a seam for tests to inject a fake Dial into the
// deploy pipeline's preflight phase.
var pipelineDial = Dial

// pipelineProbe is a seam for tests to inject a fake bootstrap probe
// into the deploy pipeline's preflight phase.
var pipelineProbe = ProbeBootstrap
```

Replace `preflight` (lines 109-117) with:

```go
// preflight validates SSH config, dials the host, and probes for a
// bootstrapped server (docker accessible without sudo). Unbootstrapped
// hosts fail fast with NotBootstrappedError instead of hanging on a
// hidden sudo prompt during the build phase.
func (p *Pipeline) preflight(ctx context.Context) (*Client, error) {
	if p.SSH.Host == "" {
		return nil, fmt.Errorf("deploy.%s.host is empty", p.Env)
	}
	if p.SSH.KeyPath == "" {
		return nil, fmt.Errorf("ssh key path is empty (set --ssh-key or DEPLOY_SSH_KEY)")
	}
	client, err := pipelineDial(ctx, p.SSH)
	if err != nil {
		return nil, err
	}
	ok, err := pipelineProbe(ctx, client)
	if err != nil {
		client.Close()
		return nil, err
	}
	if !ok {
		client.Close()
		return nil, NotBootstrappedError(p.Env)
	}
	return client, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/deploy/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/deploy.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): fail fast in preflight when server not bootstrapped"
```

---

### Task 4: `tui.PickEnv`

**Files:**
- Create: `internal/tui/env.go`
- Test: `internal/tui/env_test.go`

**Interfaces:**
- Consumes: `NewSinglePicker`, `ErrAborted` (picker.go / service.go).
- Produces: `tui.PickEnv(labels []string) (int, error)` — chosen index, `-1` with nil error for an empty list, `ErrAborted` on q/Ctrl+C. Task 5's CLI uses it via the `pickEnvTUI` seam.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/env_test.go`:

```go
package tui

import "testing"

func TestPickEnvEmpty(t *testing.T) {
	idx, err := PickEnv(nil)
	if err != nil {
		t.Fatalf("PickEnv(nil) = %v, want nil", err)
	}
	if idx != -1 {
		t.Errorf("PickEnv(nil) index = %d, want -1", idx)
	}
}

func TestPickEnvBuildsSinglePicker(t *testing.T) {
	idx, err := PickEnv([]string{"stage (s.example.com)", "production (p.example.com)"})
	_ = idx
	_ = err
	// Contract lock: constructing the picker must not panic and the
	// full Run is exercised via the CLI seam test (cli/bootstrap_test.go).
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./internal/tui/ -run TestPickEnv`
Expected: FAIL — `undefined: PickEnv`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/env.go`:

```go
package tui

// PickEnv opens a single-select Picker over the given labels and
// returns the chosen index. Returns -1 with a nil error when labels
// is empty, and ErrAborted when the user hits q / Ctrl+C.
func PickEnv(labels []string) (int, error) {
	if len(labels) == 0 {
		return -1, nil
	}
	p := NewSinglePicker("Env to bootstrap", labels, 0)
	res, err := p.Run()
	if err != nil {
		return -1, err
	}
	if res.Aborted {
		return -1, ErrAborted
	}
	return res.Indices[0], nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/env.go internal/tui/env_test.go
git commit -m "feat(tui): add PickEnv single-picker helper"
```

---

### Task 5: `pier bootstrap` CLI command

**Files:**
- Create: `internal/cli/bootstrap.go`
- Modify: `internal/cli/helpers.go`
- Test: `internal/cli/bootstrap_test.go`

**Interfaces:**
- Consumes: `config.Load` + `cfg.Deploy` map, `sshKeyPath()` (deploy.go), `tuiForTest()` seam (init.go), `deploy.BootstrapEnv`/`deploy.ProbeEnv`/`deploy.BootstrapOpts`/`deploy.ErrAlreadyBootstrapped`/`deploy.ErrSudoWrongPassword`/`deploy.SSHConfig` (Task 2), `tui.PickEnv` + `tui.ErrAborted` (Task 4), `AbortedError()` (errors.go), `cliError` (helpers.go).
- Produces: `newBootstrapCmd(stdout, stderr io.Writer) *cobra.Command` with `--all` and `--force` flags; seams `pickEnvTUI`, `probeEnvFn`, `bootstrapEnvFn`, `readSudoPwd` (test-overridable). Task 6 registers it.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/bootstrap_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
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
```

Imports for `bootstrap_test.go`: `bytes`, `context`, `os`, `path/filepath`, `testing`, `github.com/Bonnary/pier/internal/config`, `github.com/Bonnary/pier/internal/deploy`, `github.com/Bonnary/pier/internal/tui`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/cli/ -run TestResolveBootstrap|TestRunBootstrap|TestBootstrapAbort`
Expected: FAIL — `undefined: newBootstrapCmd` is not reached; the direct failures are `undefined: resolveBootstrapEnvs`, `undefined: pickEnvTUI`, etc.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/bootstrap.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/tui"
)

type bootstrapFlags struct {
	all   bool
	force bool
}

// test seams — overridable from *_test.go.
var (
	pickEnvTUI     = tui.PickEnv
	probeEnvFn     = deploy.ProbeEnv
	bootstrapEnvFn = deploy.BootstrapEnv
	readSudoPwd    = readSudoPassword
)

// newBootstrapCmd returns the `pier bootstrap` command: one-time
// server provisioning (Docker install + docker group membership for
// the deploy user), with a hidden sudo-password prompt.
func newBootstrapCmd(stdout, stderr io.Writer) *cobra.Command {
	f := &bootstrapFlags{}
	cmd := &cobra.Command{
		Use:   "bootstrap [env...]",
		Short: "Provision servers: install Docker and grant the deploy user docker access",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "bootstrap every env in pier.toml")
	cmd.Flags().BoolVar(&f.force, "force", false, "re-provision even if already bootstrapped")
	return cmd
}

// runBootstrap resolves the target envs and provisions each one in
// order. Already-bootstrapped servers are skipped (unless --force);
// the sudo password is prompted for per env, with one re-prompt on a
// wrong password.
func runBootstrap(cmd *cobra.Command, args []string, f *bootstrapFlags) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	envs, err := resolveBootstrapEnvs(cfg, args, f.all)
	if err != nil {
		return err
	}
	for _, env := range envs {
		dc := cfg.Deploy[env]
		sshCfg := deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()}
		if !f.force {
			ok, err := probeEnvFn(cmd.Context(), sshCfg)
			if err != nil {
				return err
			}
			if ok {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: already bootstrapped — skipping\n", env)
				continue
			}
		}
		pw, err := readSudoPwd(fmt.Sprintf("sudo password for %s@%s: ", dc.User, dc.Host))
		if err != nil {
			return err
		}
		err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{User: dc.User, Force: f.force})
		if errors.Is(err, deploy.ErrSudoWrongPassword) {
			pw, err = readSudoPwd("wrong password — try again: ")
			if err != nil {
				return err
			}
			err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{User: dc.User, Force: f.force})
		}
		if errors.Is(err, deploy.ErrSudoNotSudoers) {
			return fmt.Errorf("%w: add %q to sudoers on %s first, or bootstrap as a different user",
				err, dc.User, dc.Host)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: done\n", env)
	}
	return nil
}

// resolveBootstrapEnvs turns the command's env arguments, --all, or
// (on a TTY) an interactive picker into the ordered list of env
// names to provision. Env names are sorted alphabetically when the
// picker or --all selects them, so the order is deterministic.
func resolveBootstrapEnvs(cfg *config.Config, args []string, all bool) ([]string, error) {
	if len(args) > 0 && all {
		return nil, fmt.Errorf("cannot combine env arguments with --all")
	}
	names := sortedEnvNames(cfg)
	if all {
		if len(names) == 0 {
			return nil, fmt.Errorf("no [deploy.<env>] sections in pier.toml")
		}
		return names, nil
	}
	if len(args) == 0 {
		if tuiForTest() {
			if len(names) == 0 {
				return nil, fmt.Errorf("no [deploy.<env>] sections in pier.toml")
			}
			labels := make([]string, len(names))
			for i, n := range names {
				labels[i] = fmt.Sprintf("%s (%s)", n, cfg.Deploy[n].Host)
			}
			idx, err := pickEnvTUI(labels)
			if err != nil {
				if errors.Is(err, tui.ErrAborted) {
					return nil, nil // clean abort: exit 0
				}
				return nil, err
			}
			return []string{names[idx]}, nil
		}
		return nil, fmt.Errorf("no env given; pass one or more env names or --all")
	}
	for _, a := range args {
		if _, ok := cfg.Deploy[a]; !ok {
			return nil, cliError("no [deploy.%s] section in pier.toml", a)
		}
	}
	return args, nil
}

// sortedEnvNames returns the env names in pier.toml sorted
// alphabetically. Go maps don't preserve TOML order; sorting keeps
// the picker and --all deterministic.
func sortedEnvNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Deploy))
	for n := range cfg.Deploy {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

Add the hidden password prompt to `internal/cli/helpers.go`:

```go
// readSudoPassword prompts on stderr (so --json stdout stays clean)
// with echo disabled and returns the entered password. The prompt
// goes to stderr because it is not part of the command's output.
func readSudoPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}
```

Add `"golang.org/x/term"` to `helpers.go`'s imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/cli/ ./internal/tui/ ./internal/deploy/`
Expected: PASS.

- [ ] **Step 5: Run the linter**

Run: `golangci-lint run`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/bootstrap.go internal/cli/bootstrap_test.go internal/cli/helpers.go
git commit -m "feat(cli): add pier bootstrap command"
```

---

### Task 6: Wire the command + README

**Files:**
- Modify: `internal/cli/root.go:45-53`
- Modify: `README.md`

**Interfaces:**
- Consumes: `newBootstrapCmd` (Task 5).
- Produces: `pier bootstrap --help` works; docs match the feature.

- [ ] **Step 1: Register the command**

In `internal/cli/root.go`, add after the deploy command (line 51):

```go
	root.AddCommand(newBootstrapCmd(stdout, stderr))
```

- [ ] **Step 2: Verify the command runs**

Run: `go build -o /tmp/opencode/pier ./cmd/pier && /tmp/opencode/pier bootstrap --help`
Expected: usage text with `--all` and `--force` flags listed.

- [ ] **Step 3: Update the README**

In `README.md`:

1. Features list — after the `pier deploy` bullet (line 68-73), add:

```markdown
- **`pier bootstrap [env...]`** — One-time server provisioning:
  installs Docker Engine + the compose plugin over SSH and grants
  the deploy user passwordless docker access (hidden one-time sudo
  password prompt; idempotent, `--all` / `--force`).
```

2. Commands table — after the `pier deploy` row (line 216), add:

```markdown
| `pier bootstrap [env...]` | Provision one or more servers: install Docker + compose plugin, grant the deploy user docker access. Interactive picker when no env is given; `--all` for every env, `--force` to re-provision. |
```

3. Prerequisites — replace the Docker bullet (line 94-95) with:

```markdown
- **Docker Engine 24+** with the `docker compose` plugin (Docker
  Desktop on macOS/Windows; Docker Engine on Linux). On remote
  servers this comes from `pier bootstrap <env>` — the deploy user
  needs password-protected sudo once for the one-time install.
```

4. Troubleshooting — add two bullets after the SSH handshake bullet (line 375-377):

```markdown
- **"server not bootstrapped"** on `pier deploy` — run
  `pier bootstrap <env>` once on the server. The deploy user needs
  password-protected sudo for the one-time Docker install.
- **"wrong sudo password"** on `pier bootstrap` — re-run
  `pier bootstrap <env>` and enter the deploy user's sudo password
  (not the SSH key passphrase).
```

- [ ] **Step 4: Verify the build and full test suite**

Run: `go build ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/root.go README.md
git commit -m "docs: wire pier bootstrap into root command and README"
```

---

### Task 7: Integration test

**Files:**
- Create: `internal/deploy/bootstrap_integration_test.go`

**Interfaces:**
- Consumes: `BootstrapEnv`, `BootstrapOpts`, `ProbeEnv`, `SSHConfig`, `ErrAlreadyBootstrapped` (Task 2); the deploy preflight probe (Task 3).
- Produces: real-server verification of the whole flow, guarded by env vars so CI (which never sets them) skips.

- [ ] **Step 1: Write the test**

Create `internal/deploy/bootstrap_integration_test.go`:

```go
//go:build integration

package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestBootstrapRealServer provisions a real host when PIER_TEST_SSH_HOST
// is set. The provision path needs PIER_TEST_SUDO_PASSWORD (the deploy
// user's sudo password); without it the test runs the probe path only.
//
// Run:
//
//	PIER_TEST_SSH_HOST=192.168.122.63 PIER_TEST_SSH_USER=deploy \
//	PIER_TEST_SSH_KEY=~/.ssh/id_ed25519 PIER_TEST_SUDO_PASSWORD='...' \
//	go test -tags=integration -run TestBootstrapRealServer ./internal/deploy/
func TestBootstrapRealServer(t *testing.T) {
	host := os.Getenv("PIER_TEST_SSH_HOST")
	if host == "" {
		t.Skip("PIER_TEST_SSH_HOST not set")
	}
	user := os.Getenv("PIER_TEST_SSH_USER")
	if user == "" {
		user = "root"
	}
	key := os.Getenv("PIER_TEST_SSH_KEY")
	if key == "" {
		key = filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
	}
	cfg := SSHConfig{Host: host, User: user, KeyPath: key}
	ctx := context.Background()

	ok, err := ProbeEnv(ctx, cfg)
	if err != nil {
		t.Fatalf("ProbeEnv: %v", err)
	}
	t.Logf("probe before: bootstrapped=%v", ok)

	pw := os.Getenv("PIER_TEST_SUDO_PASSWORD")
	if pw == "" {
		t.Log("PIER_TEST_SUDO_PASSWORD not set; probe-only run")
		return
	}
	err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user})
	if err != nil && !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user})
	if !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Fatalf("second BootstrapEnv = %v, want ErrAlreadyBootstrapped (idempotent)", err)
	}
	if err := BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Force: true}); err != nil {
		t.Fatalf("BootstrapEnv(force): %v", err)
	}
	ok, err = ProbeEnv(ctx, cfg)
	if err != nil {
		t.Fatalf("ProbeEnv after bootstrap: %v", err)
	}
	if !ok {
		t.Error("ProbeEnv after bootstrap = false, want true")
	}
}
```

- [ ] **Step 2: Verify it compiles with the integration tag**

Run: `go vet -tags=integration ./internal/deploy/`
Expected: PASS (no compile errors).

- [ ] **Step 3: Verify the unit suite is untouched**

Run: `go test -race ./internal/deploy/`
Expected: PASS (integration files excluded without the tag).

- [ ] **Step 4: Manual run against the user's server (documented, not automated)**

Run:

```bash
PIER_TEST_SSH_HOST=192.168.122.63 PIER_TEST_SSH_USER=host \
PIER_TEST_SSH_KEY=$HOME/.ssh/id_ed25519 PIER_TEST_SUDO_PASSWORD='<sudo password>' \
go test -tags=integration -v -run TestBootstrapRealServer ./internal/deploy/
```

Expected: probe run, provision (or `ErrAlreadyBootstrapped`), idempotent second run, `--force` re-provision, probe passes.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/bootstrap_integration_test.go
git commit -m "test(deploy): integration coverage for pier bootstrap against a real server"
```

---

## Manual verification checklist (final, from spec)

1. `pier bootstrap` on a fresh VPS with key-auth + password sudo → hidden prompt, docker installed, `docker info` works for the deploy user afterwards.
2. Re-run → `already bootstrapped — skipping`.
3. `pier bootstrap --force` → re-provisions.
4. `pier deploy <env>` on the bootstrapped server → no password prompt anywhere.
5. `pier deploy <env>` on an un-bootstrapped server → fails fast with the bootstrap hint (exit code 2).

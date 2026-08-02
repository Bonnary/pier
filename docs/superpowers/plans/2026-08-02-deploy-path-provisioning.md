# Deploy Path Provisioning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pier bootstrap` create the remote deploy path (with sudo) and make `pier deploy` preflight ensure it exists — replacing the cryptic rsync "exit status 11" with an actionable message — plus surface rsync stderr in sync failures.

**Architecture:** `BootstrapOpts` gains a `Path` field; `BootstrapEnv` runs a new `ProvisionDeployPath` sudo step (`mkdir -p <path> && chown <user>:<user> <path>`) between Provision and Verify. `Pipeline.preflight` runs `EnsureDeployPath` (`mkdir -p <path>`, no sudo) via a new `pipelineEnsurePath` seam (same pattern as `pipelineDial`/`pipelineProbe`), failing fast with the sudo fix commands on error. `osRunner.Run` captures combined output and wraps non-zero exits with a 4 KB excerpt.

**Tech Stack:** Go, `os/exec`, `golang.org/x/crypto/ssh`, cobra, stdlib `testing`.

## Global Constraints

- `pier deploy` must stay non-interactive — never prompt for a sudo password (spec section 3).
- Path creation is idempotent: `mkdir -p` and `chown` re-run cleanly, so `--force` is safe (spec section 1).
- Path quoting: apostrophes escaped POSIX-style (`'` → `'\''`) and wrapped in single quotes (helper `quoteShell`, same semantics as current `runSudo` escaping). The user is quoted with `strconv.Quote`, matching the existing `usermod` step (bootstrap.go:97).
- rsync stderr excerpt capped at 4096 bytes (spec section 4).
- No changes to `Kind` / `ExitError` / hints (spec section 5).
- Boundary rules: `cli` never runs SSH directly; `deploy` never reads the TUI.
- `internal/config/parse.go:82` already rejects empty `[deploy.<env>].path` — the preflight ensure step never sees an empty path.
- Config field name: `BootstrapOpts.Path` — empty string means "no path to create" (spec section 1).
- Every step ends with `gofmt -l .` clean and `go test ./...` green.

---

### Task 1: Bootstrap creates the deploy path (`internal/deploy/bootstrap.go`)

**Files:**
- Modify: `internal/deploy/bootstrap.go`
- Test: `internal/deploy/bootstrap_test.go`

**Interfaces:**
- Consumes: existing `runSudo(ctx, r, password, cmd, onStdout, onStderr)` (bootstrap.go:70-78), existing `stdinRunner` (ssh.go:219-223), existing `scriptedRunner`/`fakeConn` test fakes (bootstrap_test.go:17-86).
- Produces:
  - `quoteShell(s string) string` — single-quote wrap with POSIX apostrophe escaping.
  - `ProvisionDeployPath(ctx context.Context, r stdinRunner, password, user, path string, onStdout, onStderr func(string)) error` — runs `mkdir -p <quoted> && chown <quser>:<quser> <quoted>` under sudo via `runSudo`.
  - `cmdRunner` interface — `Run(ctx context.Context, cmd string) ([]byte, []byte, error)` (the subset of `*Client` that `EnsureDeployPath` needs; `scriptedRunner` and `*Client` both satisfy it).
  - `EnsureDeployPath(ctx context.Context, r cmdRunner, path string) error` — runs `mkdir -p <quoted>` as the deploy user (no sudo) via `r.Run`.
  - `BootstrapOpts` gains `Path string`.
  - `BootstrapEnv` runs `ProvisionDeployPath` when `opts.Path != ""`, between `Provision` and `VerifyBootstrap`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/bootstrap_test.go`:

```go
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
		"mkdir -p '/srv/x'",
		`chown "deploy":"deploy" '/srv/x'`,
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
	if !strings.Contains(r.cmds[0], `mkdir -p '/O'\''Brien/x'`) {
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
```

The `TestBootstrapEnvCreatesDeployPath` test uses `scriptedRunner` directly (it satisfies `cmdRunner` via its `Run` method).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestQuoteShell|TestRunSudoUsesQuoteShell|TestProvisionDeployPath|TestEnsureDeployPath|TestBootstrapEnvCreatesDeployPath|TestBootstrapEnvSkipsPathWhenEmpty' -v`
Expected: FAIL — `quoteShell` undefined, `ProvisionDeployPath` undefined, `EnsureDeployPath` undefined, `BootstrapOpts.Path` field missing.

- [ ] **Step 3: Implement**

In `internal/deploy/bootstrap.go`:

1. Add `quoteShell` next to `runSudo`:

```go
// quoteShell wraps s in single quotes with POSIX apostrophe escaping
// (`'` becomes `'\''`), so it can be embedded in a remote shell
// command string. runSudo applies this to its whole command body.
func quoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

2. Replace `runSudo`'s body with a `quoteShell`-based construction (same semantics as today):

```go
func runSudo(ctx context.Context, r stdinRunner, password, cmd string, onStdout, onStderr func(string)) error {
	full := fmt.Sprintf("sudo -S -p '' sh -c %s", quoteShell(cmd))
	stderr, err := r.RunStreamStdin(ctx, full, password+"\n", onStdout, onStderr)
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}
```

3. Add `cmdRunner`, `ProvisionDeployPath`, and `EnsureDeployPath` after `VerifyBootstrap`:

```go
// cmdRunner is the subset of *Client that EnsureDeployPath needs.
// Both *Client and the test scriptedRunner satisfy it.
type cmdRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
}

// ProvisionDeployPath creates the env's deploy path on the remote
// host under sudo and hands it to the deploy user: `mkdir -p <path>
// && chown <user>:<user> <path>`. Idempotent, so `--force` re-runs
// are safe. The user is quoted the same way Provision quotes it.
func ProvisionDeployPath(ctx context.Context, r stdinRunner, password, user, path string, onStdout, onStderr func(string)) error {
	cmd := fmt.Sprintf("mkdir -p %s && chown %s:%s %s",
		quoteShell(path), strconv.Quote(user), strconv.Quote(user), quoteShell(path))
	return runSudo(ctx, r, password, cmd, onStdout, onStderr)
}

// EnsureDeployPath creates the deploy path as the deploy user without
// sudo, so rsync has a writable destination. Fails when the parent
// directory is not writable; the deploy preflight turns that into an
// actionable error.
func EnsureDeployPath(ctx context.Context, r cmdRunner, path string) error {
	_, _, err := r.Run(ctx, "mkdir -p "+quoteShell(path))
	return err
}
```

4. Add `Path` to `BootstrapOpts` and the step to `BootstrapEnv`:

```go
type BootstrapOpts struct {
	// Force skips the probe and re-provisions even when the server
	// is already bootstrapped.
	Force bool
	// User is the deploy user that gets docker group membership.
	User string
	// Path is the env's deploy directory ([deploy.<env>].path),
	// created with sudo and chowned to User. Empty means no path to
	// create.
	Path string
	// OnStdout/OnStderr stream each remote output line as it
	// arrives; may be nil.
	OnStdout func(string)
	OnStderr func(string)
}
```

In `BootstrapEnv`, between the `Provision` and `VerifyBootstrap` calls:

```go
	if err := Provision(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr); err != nil {
		return fmt.Errorf("install docker: %w", err)
	}
	if opts.Path != "" {
		if err := ProvisionDeployPath(ctx, client, password, opts.User, opts.Path, opts.OnStdout, opts.OnStderr); err != nil {
			return fmt.Errorf("create deploy path: %w", err)
		}
	}
	return VerifyBootstrap(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr)
```

(Update the `BootstrapEnv` doc comment to mention the path step.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS (all existing bootstrap/ssh/probe tests plus the new ones). Note `TestRunSudoEscapesApostrophes` and `TestProvisionRunsInstallAndUsermod` must still pass unchanged — they pin the `runSudo` semantics.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/bootstrap.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): bootstrap creates the deploy path with sudo"
```

---

### Task 2: CLI passes the deploy path (`internal/cli/bootstrap.go`)

**Files:**
- Modify: `internal/cli/bootstrap.go`
- Test: `internal/cli/bootstrap_test.go`

**Interfaces:**
- Consumes: `BootstrapOpts{User, Force, Path, OnStdout, OnStderr}` from Task 1; `cfg.Deploy[env].Path` (already parsed).
- Produces: `pier bootstrap <env>` creates `[deploy.<env>].path` on the remote host.

- [ ] **Step 1: Extend the failing tests**

In `internal/cli/bootstrap_test.go`, extend `TestRunBootstrapProvisions` (line ~193) to assert the path:

```go
	if gotOpts.User != "deploy" || gotOpts.Force {
		t.Errorf("opts = %+v, want {User: deploy, Force: false}", gotOpts)
	}
	if gotOpts.Path != "/srv/x" {
		t.Errorf("opts.Path = %q, want %q", gotOpts.Path, "/srv/x")
	}
```

And extend `TestRunBootstrapRetriesWrongPasswordOnce` to capture the second call's opts. Replace its `bootstrapEnvFn` fake:

```go
	attempts := 0
	var retriedOpts deploy.BootstrapOpts
	origBootstrap := bootstrapEnvFn
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		attempts++
		if attempts == 1 {
			return deploy.ErrSudoWrongPassword
		}
		retriedOpts = opts
		return nil
	}
	defer func() { bootstrapEnvFn = origBootstrap }()
```

and after `root.Execute()` succeeds add:

```go
	if retriedOpts.Path != "/srv/x" {
		t.Errorf("retry opts.Path = %q, want %q", retriedOpts.Path, "/srv/x")
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRunBootstrapProvisions|TestRunBootstrapRetriesWrongPasswordOnce' -v`
Expected: FAIL — `opts.Path` is `""`, want `"/srv/x"`.

- [ ] **Step 3: Implement**

In `internal/cli/bootstrap.go`, both `BootstrapOpts` constructions in `runBootstrap` (initial at line 77 and wrong-password retry at line 88) gain `Path: dc.Path`:

```go
		err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{
			User:     dc.User,
			Force:    f.force,
			Path:     dc.Path,
			OnStdout: func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) },
			OnStderr: func(line string) { fmt.Fprintln(cmd.ErrOrStderr(), line) },
		})
```

(same addition in the retry block below it).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/bootstrap.go internal/cli/bootstrap_test.go
git commit -m "feat(cli): bootstrap passes the deploy path"
```

---

### Task 3: Deploy preflight ensures the path (`internal/deploy/deploy.go`)

**Files:**
- Modify: `internal/deploy/deploy.go`
- Modify: `internal/deploy/bootstrap_test.go` (preflight tests live here)
- Consumes: `EnsureDeployPath(ctx, r cmdRunner, path string)` from Task 1; `*Client` satisfies `cmdRunner`.

**Interfaces:**
- Produces: new seam `var pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return EnsureDeployPath(ctx, c, path) }` in deploy.go (next to `pipelineDial`/`pipelineProbe`, deploy.go:18-26).
- Produces: `Pipeline.preflight` fails with `fmt.Errorf` message: `deploy path <path> on <host> is not writable for <user>.\nCreate it once with:\n  sudo mkdir -p <path>\n  sudo chown <user>:<user> <path>\n(or re-run \`pier bootstrap <env>\` to create it automatically.)` — wrapped by `PreflightError` in `Run` (exit 2, KindConfig).

- [ ] **Step 1: Update the existing success-path test and write failing tests**

In `internal/deploy/bootstrap_test.go`, `TestPreflightAcceptsBootstrappedServer` dials a `&Client{Config: cfg}` with a **nil conn** — once preflight calls `EnsureDeployPath` it would panic. Override the new seam in that test, after the `pipelineProbe` override:

```go
	origEnsure := pipelineEnsurePath
	defer func() { pipelineEnsurePath = origEnsure }()
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error {
		if path != "/srv/x" {
			t.Errorf("ensure path = %q, want %q", path, "/srv/x")
		}
		return nil
	}
```

Append two new tests to `internal/deploy/bootstrap_test.go`:

```go
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
```

Note: the dialed object here is `&Client{Config: cfg}` (the `*Client` type assertion requires it), so its `Close` on a nil conn is a no-op and cannot be asserted — the fakeConn's `closed` flag is irrelevant to this test.

The `errors` package is already imported in `bootstrap_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestPreflight' -v`
Expected: FAIL — `pipelineEnsurePath` undefined; `TestPreflightRejectsUnwritableDeployPath` fails with "not writable" message missing.

- [ ] **Step 3: Implement**

In `internal/deploy/deploy.go`, next to `pipelineProbe` (line 24-26):

```go
// pipelineEnsurePath is a seam for tests to inject a fake path-ensure
// step into the deploy pipeline's preflight phase.
var pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error {
	return EnsureDeployPath(ctx, c, path)
}
```

In `preflight`, after the `conn.(*Client)` type assertion (deploy.go:144-148):

```go
	client, ok := conn.(*Client)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("internal: dial returned %T, want *Client", conn)
	}
	if err := pipelineEnsurePath(ctx, client, p.DeployEnv.Path); err != nil {
		client.Close()
		return nil, fmt.Errorf(
			"deploy path %s on %s is not writable for %s.\nCreate it once with:\n  sudo mkdir -p %s\n  sudo chown %s:%s %s\n(or re-run `pier bootstrap %s` to create it automatically.)",
			p.DeployEnv.Path, p.SSH.Host, p.SSH.User,
			p.DeployEnv.Path, p.SSH.User, p.SSH.User, p.DeployEnv.Path, p.Env)
	}
	return client, nil
```

Update `preflight`'s doc comment (deploy.go:120-123) to mention the path-ensure step.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/deploy.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): preflight ensures the deploy path with an actionable error"
```

---

### Task 4: Surface rsync stderr in sync failures (`internal/deploy/rsync.go`)

**Files:**
- Modify: `internal/deploy/rsync.go`
- Test: `internal/deploy/rsync_test.go`

**Interfaces:**
- Consumes: `CommandRunner` interface unchanged (rsync.go:11-13); `Sync` unchanged (rsync.go:46-50).
- Produces: `osRunner.Run` wraps non-zero exits as `fmt.Errorf("%w: %s", err, trimmedOutput)` where `trimmedOutput` is the command's combined output, whitespace-trimmed, capped at 4096 bytes with a `...` suffix.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/rsync_test.go`:

```go
func TestOsRunnerCapturesOutputOnFailure(t *testing.T) {
	err := (osRunner{}).Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("osRunner.Run(failing) = nil error, want non-nil")
	}
	if !contains(err.Error(), "boom") {
		t.Errorf("err %q missing captured stderr", err.Error())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("err is %T, want *exec.ExitError in chain", err)
	} else if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3", exitErr.ExitCode())
	}
}

func TestOsRunnerSuccessNoError(t *testing.T) {
	if err := (osRunner{}).Run(context.Background(), "true"); err != nil {
		t.Fatalf("osRunner.Run(true): %v", err)
	}
}

func TestOsRunnerTrimsOutput(t *testing.T) {
	long := strings.Repeat("x", 8192)
	err := (osRunner{}).Run(context.Background(), "sh", "-c", "printf '%s' '"+long+"' >&2; exit 1")
	if err == nil {
		t.Fatal("osRunner.Run(long) = nil error, want non-nil")
	}
	if len(err.Error()) > 4096+64 {
		t.Errorf("error message length %d exceeds 4KB excerpt + margin", len(err.Error()))
	}
	if !strings.HasSuffix(err.Error(), "...") {
		t.Errorf("error %q missing truncation suffix", err.Error())
	}
}
```

Add imports to `rsync_test.go`: `"errors"`, `"os/exec"`, `"strings"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestOsRunner' -v`
Expected: FAIL — `osRunner.Run` returns bare `*exec.ExitError`; the "boom" output and suffix assertions fail.

- [ ] **Step 3: Implement**

In `internal/deploy/rsync.go`, replace `osRunner`:

```go
// maxSyncOutput is the cap for the stderr excerpt included in sync
// error messages. Enough to show the failing rsync paths, never a
// wall of noise.
const maxSyncOutput = 4096

type osRunner struct{}

func (osRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if len(s) > maxSyncOutput {
		s = s[:maxSyncOutput] + "..."
	}
	if s == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, s)
}
```

Add `"fmt"` and `"strings"` to the imports (the file currently imports only `context` and `os/exec`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/rsync.go internal/deploy/rsync_test.go
git commit -m "fix(deploy): surface rsync stderr in sync failures"
```

---

### Task 5: Integration coverage + README

**Files:**
- Modify: `internal/deploy/bootstrap_integration_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `BootstrapOpts.Path`, `BootstrapEnv` from Task 1; `Dial` (ssh.go:51); `Client.Run` (ssh.go:88).
- Produces: `PIER_TEST_DEPLOY_PATH` env knob for the real-server integration test; README documents that bootstrap creates the deploy path.

- [ ] **Step 1: Extend the integration test (fails when run with the env set)**

In `internal/deploy/bootstrap_integration_test.go`, in `TestBootstrapRealServer`, read the optional env var and thread it into the `BootstrapEnv` calls:

```go
	deployPath := os.Getenv("PIER_TEST_DEPLOY_PATH")
	if deployPath != "" && pw != "" {
		err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Path: deployPath})
		if err != nil && !errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatalf("BootstrapEnv: %v", err)
		}
		err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Path: deployPath})
		if !errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatalf("second BootstrapEnv = %v, want ErrAlreadyBootstrapped (idempotent)", err)
		}
		if err := BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Force: true, Path: deployPath}); err != nil {
			t.Fatalf("BootstrapEnv(force): %v", err)
		}
		if err := assertRemotePathOwned(ctx, cfg, deployPath, user); err != nil {
			t.Fatalf("deploy path ownership: %v", err)
		}
		t.Logf("deploy path %s owned by %s", deployPath, user)
	}
```

Replace the existing three `BootstrapEnv` calls (lines 49-58) with the block above, keeping the `pw == ""` probe-only short-circuit before it. Add the helper:

```go
// assertRemotePathOwned dials host and asserts path exists and is
// owned by wantUser.
func assertRemotePathOwned(ctx context.Context, cfg SSHConfig, path, wantUser string) error {
	client, err := Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	_, _, err = client.Run(ctx, fmt.Sprintf(
		"[ -d %s ] && [ \"$(stat -c '%%U' %s)\" = %s ]",
		quoteShell(path), quoteShell(path), quoteShell(wantUser)))
	return err
}
```

Add `"fmt"` to the imports.

- [ ] **Step 2: Run the integration test locally (probe-only path stays green)**

Run: `go test -tags=integration ./internal/deploy/ -run TestBootstrapRealServer`
Expected: PASS (skipped without `PIER_TEST_SSH_HOST`).

Then against the real test server with the path set:

Run: `PIER_TEST_SSH_HOST=192.168.122.63 PIER_TEST_SSH_USER=host PIER_TEST_SSH_KEY=~/.ssh/id_ed25519 PIER_TEST_SUDO_PASSWORD='<sudo pw>' PIER_TEST_DEPLOY_PATH=/test_web go test -tags=integration -v ./internal/deploy/ -run TestBootstrapRealServer`
Expected: PASS — this actually creates `/test_web` on the server, owned by `host` (this is the manual unblock; only run when the user confirms).

- [ ] **Step 3: Update README**

In `README.md`, the bootstrap bullet (line 69-71, `pier bootstrap [env...]` — One-time server provisioning) — add a sentence at the end of that bullet:

```
   Also creates each env's deploy directory (`[deploy.<env>].path`)
   and hands it to the deploy user, so `pier deploy` never hits a
   missing-path rsync error.
```

Also check the troubleshooting section around line 384 (`server not bootstrapped`) — if a "deploy path not writable" troubleshooting entry doesn't exist, add one:

```
- **"deploy path ... is not writable"** on `pier deploy` — the
  deploy directory doesn't exist and its parent isn't writable by the
  deploy user. Re-run `pier bootstrap <env>` to create it, or run the
  `sudo mkdir -p` / `sudo chown` commands from the error message.
```

- [ ] **Step 4: Verify the full suite**

Run: `go test ./... && gofmt -l .`
Expected: PASS, no files listed by gofmt.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/bootstrap_integration_test.go README.md
git commit -m "test(deploy): integration coverage for deploy path ownership; docs"
```

---

### Post-implementation: unblock the user's deploy

After Task 5, in `/media/pcnerd/New Volume/Code/php/test_web`:

1. Rebuild: `go build -o ./pier ./cmd/pier` in the pier repo, then copy the binary to the test_web project (the checked-in `./pier` there is 16 MB, dated today — it is the freshly built binary).
2. `./pier bootstrap --force production` — prompts for sudo password, creates `/test_web` owned by `host`, verifies Docker.
3. `./pier deploy production` — preflight creates/confirms the path, sync succeeds, build/up/health complete.

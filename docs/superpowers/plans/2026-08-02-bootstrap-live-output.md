# Live Output for `pier bootstrap` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream remote stdout+stderr live during `pier bootstrap` provisioning (Coolify-style), while still piping the sudo password over SSH stdin and preserving the existing wrong-password / not-sudoers error classification.

**Architecture:** Add a new SSH transport method `RunStreamStdin` that pipes stdin from a string and streams stdout+stderr line-by-line to callbacks while capturing stderr for error classification. Thread `OnStdout`/`OnStderr` callbacks through `BootstrapOpts` → `BootstrapEnv` → `runSudo`/`ValidateSudo`/`Provision`/`VerifyBootstrap`. The CLI wires the callbacks to cobra's stdout/stderr writers. Sudo runs with `-p ''` to suppress its own prompt.

**Tech Stack:** Go 1.25, `golang.org/x/crypto/ssh`, cobra. Follows the existing `RunStream` `onLine` callback pattern (`internal/deploy/build.go:11`).

## Global Constraints

- Go 1.25.0; module `github.com/Bonnary/pier`.
- The sudo password is NEVER part of a command string — only piped via session stdin (`RunStdin`/`RunStreamStdin`).
- Sudo commands become `sudo -S -p '' sh -c '<cmd>'` (the `-p ''` suppresses sudo's own password prompt, since the password is already piped; the apostrophe escaping in `runSudo` stays).
- `Run`, `RunStdin`, and `RunStream` on `*Client` keep their exact current behavior — only `stdinRunner` gains a method.
- Error classification is unchanged: `classifySudoErr` still maps captured stderr to `ErrSudoWrongPassword` / `ErrSudoNotSudoers`.
- Callbacks may be nil — guard before invoking; never panic.
- Integration tests are build-tagged `integration`; default `go test ./...` skips them.
- Commit style: conventional commits (`feat(deploy): ...`, `feat(cli): ...`). Commit only after the task's tests pass.

---

### Task 1: `RunStreamStdin` SSH transport method

**Files:**
- Modify: `internal/deploy/ssh.go` (add method after `RunStdin` at line 121, extend `stdinRunner` at line 163)
- Modify: `internal/deploy/bootstrap_test.go` (add `RunStreamStdin` to `scriptedRunner` fake, add contract test + `equalStr` helper)
- Modify: `internal/deploy/bootstrap_integration_test.go` (append real-SSH streaming test)

**Interfaces:**
- Consumes: nothing new (uses existing `*ssh.Client` session APIs).
- Produces:
  ```go
  // on *Client:
  RunStreamStdin(ctx context.Context, cmd, stdin string, onStdout, onStderr func(string)) ([]byte, error)
  // stdinRunner gains the same method (returns captured stderr, error)
  ```
  Task 2 consumes this exact signature via `stdinRunner`.

- [ ] **Step 1: Write the failing contract test + `equalStr` helper**

Append to `internal/deploy/bootstrap_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/deploy/ -run TestScriptedRunnerStreamsLines -v`
Expected: FAIL to compile — `f.RunStreamStdin undefined (type *scriptedRunner has no field or method RunStreamStdin)`

- [ ] **Step 3: Implement the fake method**

In `internal/deploy/bootstrap_test.go`, add to `scriptedRunner` (next to `RunStdin` at line 35):

```go
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
```

(`strings` is already imported in this file.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/deploy/ -run TestScriptedRunnerStreamsLines -v`
Expected: PASS

- [ ] **Step 5: Implement `RunStreamStdin` on `*Client` and extend the interface**

In `internal/deploy/ssh.go`, after `RunStdin` (line 121), add:

```go
// RunStreamStdin executes cmd on the remote host with stdin piped
// from the given string, invoking onStdout/onStderr for each line
// as it arrives and returning the captured stderr. Used by bootstrap
// to stream provisioning output while feeding the sudo password to
// `sudo -S` without it ever appearing in the command string.
func (c *Client) RunStreamStdin(ctx context.Context, cmd, stdin string, onStdout, onStderr func(string)) ([]byte, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	sess.Stdin = strings.NewReader(stdin)
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := sess.Start(cmd); err != nil {
		return nil, err
	}
	var stderrBuf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			stderrBuf.WriteString(line)
			stderrBuf.WriteByte('\n')
			if onStderr != nil {
				onStderr(line)
			}
		}
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if onStdout != nil {
			onStdout(sc.Text())
		}
	}
	<-done
	return stderrBuf.Bytes(), sess.Wait()
}
```

Then replace the `stdinRunner` interface (line 163):

```go
type stdinRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	RunStdin(ctx context.Context, cmd string, stdin string) ([]byte, []byte, error)
	RunStreamStdin(ctx context.Context, cmd, stdin string, onStdout, onStderr func(string)) ([]byte, error)
}
```

`bytes` and `bufio` are already imported in this file (lines 4-5).

- [ ] **Step 6: Verify compile + existing suite stays green**

Run: `go build ./... && go vet ./... && go test ./internal/deploy/ ./internal/cli/`
Expected: PASS — all existing deploy and cli tests pass (the fake now satisfies `stdinRunner`; no other implementers exist).

- [ ] **Step 7: Add the real-SSH integration test**

Append to `internal/deploy/bootstrap_integration_test.go`:

```go
// TestRunStreamStdinRealServer streams stdout/stderr lines from a
// real SSH session and returns captured stderr. Run with
// PIER_TEST_SSH_HOST (see TestBootstrapRealServer).
func TestRunStreamStdinRealServer(t *testing.T) {
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
	client, err := Dial(context.Background(), SSHConfig{Host: host, User: user, KeyPath: key})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	var stdoutLines, stderrLines []string
	stderr, err := client.RunStreamStdin(context.Background(),
		"printf 'a\\nb\\n'; printf 'x\\ny\\n' >&2", "ignored\n",
		func(l string) { stdoutLines = append(stdoutLines, l) },
		func(l string) { stderrLines = append(stderrLines, l) })
	if err != nil {
		t.Fatalf("RunStreamStdin: %v", err)
	}
	if !equalStr(stdoutLines, []string{"a", "b"}) {
		t.Errorf("stdout lines = %v, want [a b]", stdoutLines)
	}
	if !equalStr(stderrLines, []string{"x", "y"}) {
		t.Errorf("stderr lines = %v, want [x y]", stderrLines)
	}
	if string(stderr) != "x\ny\n" {
		t.Errorf("captured stderr = %q, want %q", stderr, "x\ny\n")
	}
}
```

(`equalStr` lives in the non-tagged `bootstrap_test.go`, same package — visible here. `os`, `path/filepath`, `context`, `testing` are already imported.)

Run: `go test -tags=integration ./internal/deploy/ -run TestRunStreamStdinRealServer -v`
Expected: SKIP (no `PIER_TEST_SSH_HOST`) — proves the test compiles under the integration tag.

- [ ] **Step 8: Commit**

```bash
git add internal/deploy/ssh.go internal/deploy/bootstrap_test.go internal/deploy/bootstrap_integration_test.go
git commit -m "feat(deploy): add RunStreamStdin streaming runner with piped stdin"
```

---

### Task 2: Stream bootstrap provisioning output

**Files:**
- Modify: `internal/deploy/bootstrap.go` (`runSudo`, `ValidateSudo`, `Provision`, `VerifyBootstrap`, `BootstrapEnv`, `BootstrapOpts`)
- Modify: `internal/deploy/bootstrap_test.go` (update 5 existing tests for new signatures; add 4 new tests)

**Interfaces:**
- Consumes: `RunStreamStdin` from Task 1 (via `stdinRunner`).
- Produces:
  ```go
  type BootstrapOpts struct {
      Force    bool
      User     string
      OnStdout func(string) // nil-safe
      OnStderr func(string) // nil-safe
  }
  func runSudo(ctx context.Context, r stdinRunner, password, cmd string, onStdout, onStderr func(string)) error
  func ValidateSudo(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error
  func Provision(ctx context.Context, r stdinRunner, password, user string, onStdout, onStderr func(string)) error
  func VerifyBootstrap(ctx context.Context, r stdinRunner, password, user string, onStdout, onStderr func(string)) error
  ```
  Task 3 consumes `BootstrapOpts.OnStdout/OnStderr` via the existing `bootstrapEnvFn` seam.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/bootstrap_test.go`:

```go
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
```

Note: in `TestBootstrapEnvStreamsOutput` the probe (`command -v docker && docker info`) matches no scripted step, so it fails and provisioning proceeds — exactly as intended.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestRunSudoStreamsOutputAndClassifies|TestRunSudoSuppressesPrompt|TestProvisionForwardsCallbacks|TestBootstrapEnvStreamsOutput' -v`
Expected: FAIL to compile — `runSudo`/`Provision`/`BootstrapOpts` have no callback parameters/fields (`too many arguments`).

- [ ] **Step 3: Implement the callback threading**

In `internal/deploy/bootstrap.go`, replace `runSudo` (lines 63-75):

```go
// runSudo executes cmd via `sudo -S -p '' sh -c '<cmd>'` with the
// password piped on the session's stdin — never on the command
// line — streaming each output line to the callbacks. `-p ''`
// suppresses sudo's own password prompt (the password is already
// piped). Embedded apostrophes are escaped so they cannot break out
// of the single-quoted sh -c argument. On failure the captured
// stderr is classified into the bootstrap sentinels.
func runSudo(ctx context.Context, r stdinRunner, password, cmd string, onStdout, onStderr func(string)) error {
	cmd = strings.ReplaceAll(cmd, "'", `'\''`)
	full := fmt.Sprintf("sudo -S -p '' sh -c '%s'", cmd)
	stderr, err := r.RunStreamStdin(ctx, full, password+"\n", onStdout, onStderr)
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}
```

Replace `ValidateSudo` (lines 79-85):

```go
// ValidateSudo proves the password works by running `sudo -S -v`.
// Returns ErrSudoWrongPassword or ErrSudoNotSudoers on failure.
func ValidateSudo(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error {
	stderr, err := r.RunStreamStdin(ctx, "sudo -S -v", password+"\n", onStdout, onStderr)
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}
```

Replace `Provision` (lines 90-98):

```go
// Provision installs Docker Engine + the compose plugin with the
// official get.docker.com script and adds user to the docker group,
// both under sudo. Idempotent — safe to re-run with --force.
func Provision(ctx context.Context, r stdinRunner, password, user string, onStdout, onStderr func(string)) error {
	if err := runSudo(ctx, r, password, "curl -fsSL https://get.docker.com | sh", onStdout, onStderr); err != nil {
		return fmt.Errorf("install docker: %w", err)
	}
	if err := runSudo(ctx, r, password, "usermod -aG docker "+strconv.Quote(user), onStdout, onStderr); err != nil {
		return fmt.Errorf("add user to docker group: %w", err)
	}
	return nil
}
```

Replace `VerifyBootstrap` (lines 104-110):

```go
// VerifyBootstrap confirms the daemon runs, the compose plugin is
// present, and the user is a member of the docker group. Group
// membership only applies to new SSH connections, so the group file
// is checked directly (getent) instead of re-running docker.
func VerifyBootstrap(ctx context.Context, r stdinRunner, password, user string, onStdout, onStderr func(string)) error {
	cmd := fmt.Sprintf("docker info && docker compose version && getent group docker | grep -qw %s", strconv.Quote(user))
	if err := runSudo(ctx, r, password, cmd, onStdout, onStderr); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	return nil
}
```

Replace `BootstrapOpts` (lines 124-130):

```go
// BootstrapOpts is the parameter set for BootstrapEnv.
type BootstrapOpts struct {
	// Force skips the probe and re-provisions even when the server
	// is already bootstrapped.
	Force bool
	// User is the deploy user that gets docker group membership.
	User string
	// OnStdout/OnStderr stream each remote output line as it
	// arrives; may be nil.
	OnStdout func(string)
	OnStderr func(string)
}
```

Replace the body of `BootstrapEnv` (lines 151-157) so the provisioning steps forward the callbacks:

```go
	if err := ValidateSudo(ctx, client, password, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	if err := Provision(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	return VerifyBootstrap(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr)
```

- [ ] **Step 4: Update the six existing tests for the new signatures**

In `internal/deploy/bootstrap_test.go`, add `nil, nil` callback args to the direct calls:

- `TestValidateSudoOK` (line 98): `ValidateSudo(context.Background(), r, "sekrit", nil, nil)`
- `TestValidateSudoWrongPassword` (line 110): `ValidateSudo(context.Background(), r, "nope", nil, nil)`
- `TestValidateSudoNotInSudoers` (line 121): `ValidateSudo(context.Background(), r, "pw", nil, nil)`
- `TestProvisionRunsInstallAndUsermod` (line 132): `Provision(context.Background(), r, "pw", "deploy", nil, nil)` — also update the assertion at line 136 from `"sudo -S sh -c"` to `"sudo -S -p '' sh -c"`
- `TestRunSudoEscapesApostrophes` (line 154): `runSudo(context.Background(), r, "pw", `usermod -aG docker "O'Brien"`, nil, nil)`
- `TestVerifyBootstrapChecksDaemonPluginGroup` (line 171): `VerifyBootstrap(context.Background(), r, "pw", "deploy", nil, nil)`

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ -v`
Expected: PASS — the 4 new tests plus all updated/existing deploy tests.

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/bootstrap.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): stream bootstrap provisioning output live"
```

---

### Task 3: Wire live output into the CLI

**Files:**
- Modify: `internal/cli/bootstrap.go` (`runBootstrap`, both `bootstrapEnvFn` call sites at lines 77 and 83)
- Modify: `internal/cli/bootstrap_test.go` (append one test)
- Modify: `README.md` (line 72)

**Interfaces:**
- Consumes: `deploy.BootstrapOpts{OnStdout, OnStderr}` from Task 2 (via the existing `bootstrapEnvFn` seam, `internal/cli/bootstrap.go:25`).
- Produces: nothing consumed by later tasks (final user-facing wiring).

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/bootstrap_test.go`:

```go
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
}
```

(`bytes`, `context`, `deploy` are already imported in this test file.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestRunBootstrapStreamsOutput -v`
Expected: FAIL with `OnStdout/OnStderr callbacks not wired` (the seam fake sees nil callbacks).

- [ ] **Step 3: Implement the wiring**

In `internal/cli/bootstrap.go`, inside `runBootstrap`, replace the first `bootstrapEnvFn` call (lines 77-84). The password prompt and probe logic stay; only the call changes:

```go
		err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{
			User:     dc.User,
			Force:    f.force,
			OnStdout: func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) },
			OnStderr: func(line string) { fmt.Fprintln(cmd.OutOrStderr(), line) },
		})
		if errors.Is(err, deploy.ErrSudoWrongPassword) {
			pw, err = readSudoPwd("wrong password — try again: ")
			if err != nil {
				return err
			}
			err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{
				User:     dc.User,
				Force:    f.force,
				OnStdout: func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) },
				OnStderr: func(line string) { fmt.Fprintln(cmd.OutOrStderr(), line) },
			})
		}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestRunBootstrapStreamsOutput -v`
Expected: PASS.

- [ ] **Step 5: Update the README**

In `README.md`, line 72, change:

```
  password prompt; idempotent, `--all` / `--force`).
```

to:

```
  password prompt; installation output streams live; idempotent,
  `--all` / `--force`).
```

- [ ] **Step 6: Full verification**

Run: `gofmt -l internal/ cmd/` (expected: empty), then `go build ./... && go vet ./... && go test ./...`
Expected: PASS — no output from gofmt, build/vet clean, all unit tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/bootstrap.go internal/cli/bootstrap_test.go README.md
git commit -m "feat(cli): wire live bootstrap output to the terminal"
```

---

## Verification (end-to-end)

After all tasks: `go build ./... && go vet ./... && go test ./...` passes, and against a real server:

```bash
PIER_TEST_SSH_HOST=<host> PIER_TEST_SSH_USER=<user> PIER_TEST_SSH_KEY=~/.ssh/id_ed25519 \
  go test -tags=integration -run 'TestRunStreamStdinRealServer|TestBootstrapRealServer' ./internal/deploy/
```

and manually: `./pier bootstrap <env>` shows the get.docker.com script's output streaming live, a wrong password shows sudo's `Sorry, try again` before the CLI re-prompt, and `production: done` prints at the end.

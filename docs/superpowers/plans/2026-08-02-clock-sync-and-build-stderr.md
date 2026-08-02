# Bootstrap Clock Sync & Build Stderr Surfacing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pier bootstrap` correct a stale remote clock before provisioning and make `pier deploy` build failures surface the remote command's stderr tail.

**Architecture:** Two independent fixes. (1) `EnsureClockSynced` in the bootstrap layer reads the remote epoch with a plain `Run` (no sudo), compares to `time.Now().Unix()`, and when |skew| > 60s runs the existing `runSudo` helper with `date -s @<epoch>`; wired into `BootstrapEnv` between `ValidateSudo` and `Provision`. (2) `Client.RunStream` drains both stdout and stderr pipes (mirroring `RunStreamStdin`) into a mutex-guarded last-20-lines ring buffer (`outputTail`), and on non-zero exit wraps `sess.Wait()`'s error with the tail. Both keep existing interfaces (`runner`, `stdinRunner`) and error contracts unchanged.

**Tech Stack:** Go, `golang.org/x/crypto/ssh`, stdlib `bufio`/`sync`/`time` only. No new dependencies.

## Global Constraints

- Zero new dependencies; no new SSH surface (`Dial`, `SSHConfig` untouched).
- Existing interfaces stay stable: `runner` and `stdinRunner` method signatures unchanged; all fakes (`fakeSSHClient`, `fakeRollbackRunner`, `scriptedRunner`) keep compiling unchanged.
- No changes to CLI flags, exit codes, `Kind`s, or hints; both fixes ride the existing error contract (`BuildError`, bootstrap exit path).
- No clock sync in the deploy pipeline — only `BootstrapEnv` provisions, and deploy requires docker (a completed bootstrap).
- The sudo password is never stored and never appears in the remote command line — piped via session stdin only (`runSudo`).
- `Run`, `RunStdin`, `RunStreamStdin` behavior unchanged.
- Clock sync runs only after `ValidateSudo` proves the password (sudo needed only when a correction is required).
- Boundary rules: `cli` never runs SSH directly; `deploy` never reads the TUI.

---

### Task 1: EnsureClockSynced function + unit tests

**Files:**
- Modify: `internal/deploy/bootstrap.go` (add after `ValidateSudo`, line 94; add `time` to imports)
- Test: `internal/deploy/bootstrap_test.go` (add `fmt` and `time` to imports)

**Interfaces:**
- Consumes: `stdinRunner` (exists, `internal/deploy/ssh.go:219`), `runSudo(ctx, r, password, cmd, onStdout, onStderr)` (exists, `internal/deploy/bootstrap.go:77`), `classifySudoErr` (exists).
- Produces: `const ClockSyncThreshold = 60` and `func EnsureClockSynced(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error`. Task 2 wires it into `BootstrapEnv`.

- [ ] **Step 1: Add the failing unit tests**

Append to `internal/deploy/bootstrap_test.go` and add `"fmt"` and `"time"` to its import block:

```go
func TestEnsureClockSyncedInSync(t *testing.T) {
	now := time.Now().Unix()
	r := &scriptedRunner{script: []scriptedStep{{
		match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", now),
	}}}
	if err := EnsureClockSynced(context.Background(), r, "pw", nil, nil); err != nil {
		t.Fatalf("EnsureClockSynced: %v", err)
	}
	if len(r.cmds) != 1 || !strings.Contains(r.cmds[0], "date +%s") {
		t.Errorf("cmds = %q, want exactly one `date +%s` read", r.cmds)
	}
	if len(r.stdins) != 0 {
		t.Errorf("stdins = %q, want none (no sudo in the in-sync path)", r.stdins)
	}
}

func TestEnsureClockSyncedCorrectsSkew(t *testing.T) {
	now := time.Now().Unix()
	r := &scriptedRunner{script: []scriptedStep{
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", now-86400)},
		{match: "date -s", ok: true},
	}}
	var out []string
	err := EnsureClockSynced(context.Background(), r, "pw",
		func(l string) { out = append(out, l) }, nil)
	if err != nil {
		t.Fatalf("EnsureClockSynced: %v", err)
	}
	if len(r.cmds) != 3 {
		t.Fatalf("cmds = %d, want 3 (read, set, re-read)", len(r.cmds))
	}
	if !strings.Contains(r.cmds[1], "sudo -S -p '' sh -c") || !strings.Contains(r.cmds[1], "date -s @") {
		t.Errorf("set command %q missing sudo wrapper or `date -s @`", r.cmds[1])
	}
	if len(r.stdins) != 1 || r.stdins[0] != "pw\n" {
		t.Errorf("stdins = %q, want [pw\n]", r.stdins)
	}
	if len(out) != 1 || !strings.HasPrefix(out[0], "remote clock was ") || !strings.Contains(out[0], "s off; corrected to ") {
		t.Errorf("correction line = %q, want `remote clock was Ns off; corrected to <RFC3339>`", out)
	}
}

func TestEnsureClockSyncedReadFailure(t *testing.T) {
	r := &scriptedRunner{runErr: errors.New("connection reset")}
	err := EnsureClockSynced(context.Background(), r, "pw", nil, nil)
	if err == nil {
		t.Fatal("EnsureClockSynced(read failure) = nil, want error")
	}
	if !strings.Contains(err.Error(), "read remote clock") {
		t.Errorf("err %q missing wrap `read remote clock`", err.Error())
	}
}

func TestEnsureClockSyncedParseFailure(t *testing.T) {
	r := &scriptedRunner{script: []scriptedStep{{
		match: "date +%s", ok: true, stdout: "not-an-epoch\n",
	}}}
	err := EnsureClockSynced(context.Background(), r, "pw", nil, nil)
	if err == nil {
		t.Fatal("EnsureClockSynced(garbage) = nil, want error")
	}
	if !strings.Contains(err.Error(), "read remote clock") {
		t.Errorf("err %q missing wrap `read remote clock`", err.Error())
	}
}

func TestEnsureClockSyncedSetFailure(t *testing.T) {
	now := time.Now().Unix()
	r := &scriptedRunner{script: []scriptedStep{
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", now-86400)},
		{match: "date -s", ok: false, stderr: "Sorry, try again."},
	}}
	err := EnsureClockSynced(context.Background(), r, "pw", nil, nil)
	if !errors.Is(err, ErrSudoWrongPassword) {
		t.Errorf("EnsureClockSynced = %v, want ErrSudoWrongPassword", err)
	}
	if !strings.Contains(err.Error(), "sync remote clock") {
		t.Errorf("err %q missing wrap `sync remote clock`", err.Error())
	}
}
```

Note: `scriptedRunner.respond` (`bootstrap_test.go:62`) matches by first-step-wins substring; `date +%s` and `date -s` never appear inside each other's command strings, so the two reads both match the `date +%s` step.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestEnsureClockSynced`
Expected: FAIL — build error `undefined: EnsureClockSynced`.

- [ ] **Step 3: Implement EnsureClockSynced**

In `internal/deploy/bootstrap.go`, add `"time"` to the import block and insert after `ValidateSudo` (ends line 94):

```go
// ClockSyncThreshold is the max |remote - local| offset in seconds
// tolerated before pier force-sets the remote clock. Freshly-reset
// VMs boot with a stale RTC; apt rejects signed Release files whose
// dates fall outside the (wrong) guest clock, so even minutes of
// skew break provisioning.
const ClockSyncThreshold = 60

// EnsureClockSynced compares the remote clock to the local clock and,
// when they differ by more than ClockSyncThreshold seconds, force-sets
// the remote clock from the local one under sudo (`date -s @<epoch>`).
// Needs sudo only when a correction is required. On correction it
// re-reads the remote epoch and emits one line via onStdout:
// `remote clock was Ns off; corrected to <RFC3339>`.
func EnsureClockSynced(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error {
	read := func() (int64, error) {
		stdout, _, err := r.Run(ctx, "date +%s")
		if err != nil {
			return 0, fmt.Errorf("read remote clock: %w", err)
		}
		epoch, err := strconv.ParseInt(strings.TrimSpace(string(stdout)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("read remote clock: parse %q: %w", strings.TrimSpace(string(stdout)), err)
		}
		return epoch, nil
	}
	remote, err := read()
	if err != nil {
		return err
	}
	local := time.Now().Unix()
	skew := local - remote
	if skew < 0 {
		skew = -skew
	}
	if skew <= ClockSyncThreshold {
		return nil
	}
	if err := runSudo(ctx, r, password, fmt.Sprintf("date -s @%d", local), onStdout, onStderr); err != nil {
		return fmt.Errorf("sync remote clock: %w", err)
	}
	remote, err = read()
	if err != nil {
		return err
	}
	if onStdout != nil {
		onStdout(fmt.Sprintf("remote clock was %ds off; corrected to %s", skew, time.Unix(remote, 0).Format(time.RFC3339)))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/ -run TestEnsureClockSynced`
Expected: PASS (all 5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/bootstrap.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): add EnsureClockSynced to bootstrap flow"
```

---

### Task 2: Wire clock sync into BootstrapEnv

**Files:**
- Modify: `internal/deploy/bootstrap.go:174-205` (`BootstrapEnv` body + doc comment)
- Test: `internal/deploy/bootstrap_test.go` — add `TestBootstrapEnvSyncsClockBeforeProvision`; script the `date +%s` step in `TestBootstrapEnvProvisionsWhenNeeded` (line 219), `TestBootstrapEnvForceReprovisions` (line 238), `TestBootstrapEnvStreamsOutput` (line 487), `TestBootstrapEnvCreatesDeployPath` (line 625), `TestBootstrapEnvSkipsPathWhenEmpty` (line 658)

**Interfaces:**
- Consumes: `EnsureClockSynced(ctx, r, password, onStdout, onStderr) error` from Task 1.
- Produces: `BootstrapEnv` runs the clock sync between `ValidateSudo` and `Provision` — flow per env becomes: probe → validate sudo → **sync clock** → provision → deploy path → verify. Task 3/4 don't depend on it, but the verification checklist in Task 4 relies on it.

- [ ] **Step 1: Update BootstrapEnv and its doc comment**

In `internal/deploy/bootstrap.go`, change the doc comment (line 174) to:

```go
// BootstrapEnv runs the full one-time provisioning flow for one
// server: probe (unless Force), sudo validation, clock sync, provision,
// deploy path creation (unless Path is empty), verify. Returns
// ErrAlreadyBootstrapped when the probe passes and Force is false.
```

And insert the sync between `ValidateSudo` and `Provision` (line 195):

```go
	if err := ValidateSudo(ctx, client, password, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	if err := EnsureClockSynced(ctx, client, password, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	if err := Provision(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
```

- [ ] **Step 2: Update the five existing BootstrapEnv tests**

`scriptedRunner` answers unmatched commands with exit failure (`bootstrap_test.go:76`), so each existing `BootstrapEnv` test that reaches `Provision` must script the clock read with the current epoch (in-sync → no `date -s`, no output line). Add `fmt`/`time` imports if Task 1's commit didn't already (it did). In each of the five tests below, insert this step right after the `sudo -S -v` step:

```go
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix())},
```

Tests and their scripts after the edit:

`TestBootstrapEnvProvisionsWhenNeeded` (line 222):
```go
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix())},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true},
	}}}
```

`TestBootstrapEnvForceReprovisions` (line 242):
```go
		return &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
			{match: "docker", ok: true}, // probe would pass, but Force skips it
			{match: "sudo -S -v", ok: true},
			{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix())},
			{match: "get.docker.com", ok: true},
			{match: "usermod", ok: true},
			{match: "getent group docker", ok: true},
		}}}, nil
```

`TestBootstrapEnvStreamsOutput` (line 490):
```go
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix())},
		{match: "get.docker.com", ok: true, stdout: "installing\n"},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true, stdout: "verify ok\n"},
	}}}
```
Its `equalStr(out, []string{"installing", "verify ok"})` assertion still holds: an in-sync clock emits nothing.

`TestBootstrapEnvCreatesDeployPath` (line 628):
```go
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix())},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "chown", ok: true},
		{match: "getent group docker", ok: true},
	}}}
```

`TestBootstrapEnvSkipsPathWhenEmpty` (line 661):
```go
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix())},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true},
	}}}
```

`TestBootstrapEnvSkipsWhenBootstrapped` (line 205) and the `TestPreflight*` tests never reach the sync (probe short-circuits before `ValidateSudo`) — no change.

- [ ] **Step 3: Add the flow test**

Append to `internal/deploy/bootstrap_test.go`:

```go
func TestBootstrapEnvSyncsClockBeforeProvision(t *testing.T) {
	orig := dialBootstrap
	defer func() { dialBootstrap = orig }()
	conn := &fakeConn{scriptedRunner: &scriptedRunner{script: []scriptedStep{
		{match: "sudo -S -v", ok: true},
		{match: "date +%s", ok: true, stdout: fmt.Sprintf("%d\n", time.Now().Unix()-86400)},
		{match: "date -s", ok: true},
		{match: "get.docker.com", ok: true},
		{match: "usermod", ok: true},
		{match: "getent group docker", ok: true},
	}}}
	dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) { return conn, nil }
	var out []string
	err := BootstrapEnv(context.Background(), SSHConfig{Host: "h", User: "u"}, "pw", BootstrapOpts{
		User:     "u",
		OnStdout: func(l string) { out = append(out, l) },
	})
	if err != nil {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	firstRead, setIdx, installIdx := -1, -1, -1
	for i, cmd := range conn.cmds {
		switch {
		case strings.Contains(cmd, "date +%s"):
			if firstRead < 0 {
				firstRead = i
			}
		case strings.Contains(cmd, "date -s"):
			setIdx = i
		case strings.Contains(cmd, "get.docker.com"):
			installIdx = i
		}
	}
	if !(firstRead >= 0 && firstRead < setIdx && setIdx < installIdx) {
		t.Errorf("step order wrong: read=%d set=%d install=%d, want read < set < install",
			firstRead, setIdx, installIdx)
	}
	found := false
	for _, l := range out {
		if strings.HasPrefix(l, "remote clock was ") {
			found = true
		}
	}
	if !found {
		t.Errorf("correction line missing from OnStdout output %v", out)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/deploy/ -run 'TestBootstrapEnv|TestEnsureClockSynced'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/bootstrap.go internal/deploy/bootstrap_test.go
git commit -m "feat(deploy): sync remote clock before provisioning in BootstrapEnv"
```

---

### Task 3: RunStream streams stderr and surfaces the output tail

**Files:**
- Modify: `internal/deploy/ssh.go` (add `"sync"` to imports; replace `RunStream` body at lines 178-200; add `runStreamTailSize` const and `outputTail` type)
- Test: `internal/deploy/ssh_test.go` (new `outputTail` unit tests)
- Test: `internal/deploy/bootstrap_integration_test.go` (new `TestRunStreamRealServer`; add `"strings"` to imports)

**Interfaces:**
- Consumes: `runner` interface (unchanged), `equalStr` test helper (`bootstrap_test.go:424`).
- Produces: `RunStream` behavior change — stderr lines feed the same `onLine` callback as stdout; non-zero exits return an error wrapping `sess.Wait()`'s error with the last 20 streamed lines:
  `remote command failed: Process exited with status 1 (last output: <tail>)`. No signature change; `Build` (`internal/deploy/build.go:11`), `fakeSSHClient` (`build_test.go:17`), `fakeRollbackRunner` (`rollback_test.go:19`) all keep compiling.

- [ ] **Step 1: Write the failing unit tests for outputTail**

Append to `internal/deploy/ssh_test.go`:

```go
func TestOutputTailKeepsLastLines(t *testing.T) {
	tail := &outputTail{max: 3}
	for _, l := range []string{"one", "two", "three", "four", "five"} {
		tail.add(l)
	}
	if got := tail.String(); got != "three\nfour\nfive" {
		t.Errorf("tail = %q, want %q", got, "three\nfour\nfive")
	}
}

func TestOutputTailEmpty(t *testing.T) {
	tail := &outputTail{max: 3}
	if got := tail.String(); got != "" {
		t.Errorf("tail = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestOutputTail`
Expected: FAIL — build error `undefined: outputTail`.

- [ ] **Step 3: Write the failing integration test**

Append to `internal/deploy/bootstrap_integration_test.go` (after `TestRunStreamStdinRealServer`, line 129) and add `"strings"` to its imports:

```go
// TestRunStreamRealServer streams stdout and stderr lines from a real
// SSH session and, on a non-zero exit, returns an error carrying the
// last streamed lines. Run with PIER_TEST_SSH_HOST (see
// TestBootstrapRealServer).
func TestRunStreamRealServer(t *testing.T) {
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
	var lines []string
	err = client.RunStream(context.Background(),
		"printf 'out\\n'; printf 'err\\n' >&2; exit 7",
		func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatal("RunStream(exit 7) = nil error, want non-nil")
	}
	var hasOut, hasErr bool
	for _, l := range lines {
		if l == "out" {
			hasOut = true
		}
		if l == "err" {
			hasErr = true
		}
	}
	if len(lines) != 2 || !hasOut || !hasErr {
		t.Errorf("streamed lines = %v, want both [out err] in any order", lines)
	}
	for _, want := range []string{"last output:", "out", "err"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}
```

- [ ] **Step 4: Run integration test to verify it fails**

Run: `go test -tags=integration ./internal/deploy/ -run TestRunStreamRealServer`
Expected: FAIL — `RunStream(exit 7) = nil error` (current code discards stderr and returns the raw `Wait` error). It compiles, so the build of the integration-tagged package succeeds.

- [ ] **Step 5: Implement RunStream + outputTail**

In `internal/deploy/ssh.go`, add `"sync"` to the import block, and replace the `RunStream` implementation (lines 178-200) with:

```go
// runStreamTailSize is the number of most-recent streamed lines kept
// in the error message when a RunStream command exits non-zero.
const runStreamTailSize = 20

// outputTail keeps the last max streamed lines in order. Shared by
// RunStream's stdout and stderr readers, so it is mutex-guarded.
type outputTail struct {
	mu    sync.Mutex
	max   int
	lines []string
}

// add appends line, dropping the oldest line once max is reached.
func (t *outputTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == t.max {
		copy(t.lines, t.lines[1:])
		t.lines[len(t.lines)-1] = line
		return
	}
	t.lines = append(t.lines, line)
}

// String returns the kept lines joined with newlines, oldest first.
func (t *outputTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// RunStream executes cmd on the remote host and invokes onLine for
// each line of stdout or stderr as it arrives. Used by Build, which
// needs to surface `docker compose build` progress — and, on failure,
// the compose validation errors that docker writes to stderr — in the
// deploy TUI in real time. When the remote command exits non-zero,
// the returned error carries the last runStreamTailSize streamed
// lines so the failure is diagnosable without re-running.
func (c *Client) RunStream(ctx context.Context, cmd string, onLine func(string)) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(cmd); err != nil {
		return err
	}
	tail := &outputTail{max: runStreamTailSize}
	var stderrErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			tail.add(line)
			onLine(line)
		}
		stderrErr = sc.Err()
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		tail.add(line)
		onLine(line)
	}
	<-done
	if err := sc.Err(); err != nil {
		return err
	}
	if stderrErr != nil {
		return stderrErr
	}
	if err := sess.Wait(); err != nil {
		return fmt.Errorf("remote command failed: %w (last output: %s)", err, tail.String())
	}
	return nil
}
```

The `tail` is written by both the stdout loop (main goroutine) and the stderr loop (goroutine), so `outputTail`'s mutex prevents a data race; `<-done` establishes happens-before between the stderr goroutine's final `tail.add` and the main goroutine's `tail.String()`.

- [ ] **Step 6: Run unit tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS — includes the new `outputTail` tests and all existing tests (fakes compile unchanged).

- [ ] **Step 7: Run the integration test to verify it passes**

Run: `go test -tags=integration ./internal/deploy/ -run TestRunStreamRealServer`
Expected: FAIL with `Skipped: PIER_TEST_SSH_HOST not set` unless a host is configured; with `PIER_TEST_SSH_HOST=... PIER_TEST_SSH_USER=... PIER_TEST_SSH_KEY=...` set: PASS.

- [ ] **Step 8: Run the full suite**

Run: `go vet ./... && go test ./... && go test -tags=integration ./internal/deploy/`
Expected: vet clean; unit PASS; integration package compiles (tests skip without `PIER_TEST_SSH_HOST`).

- [ ] **Step 9: Commit**

```bash
git add internal/deploy/ssh.go internal/deploy/ssh_test.go internal/deploy/bootstrap_integration_test.go
git commit -m "fix(deploy): surface stderr tail in RunStream failures"
```

---

### Task 4: CHANGELOG + final verification

**Files:**
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: nothing; documents Tasks 1-3.

- [ ] **Step 1: Add the CHANGELOG entry**

In `CHANGELOG.md`, append a `### Fixed` section at the end of the `## v0.0.2-beta` section (after the `### Removed` block, line 25):

```markdown
### Fixed

- `pier bootstrap` now force-syncs the remote clock when it drifts more than 60 seconds from the local clock, so a freshly-reset VM with a stale RTC no longer fails provisioning with `Release file ... is not valid yet`.
- `pier deploy` build failures now include the tail of the remote build output, so docker compose validation errors (e.g. `refers to undefined volume`) reach the terminal instead of only an exit status.
```

- [ ] **Step 2: Run the full automated verification**

Run: `go vet ./... && go test ./... && go test -tags=integration ./internal/deploy/`
Expected: vet clean; unit PASS; integration package compiles (skips without `PIER_TEST_SSH_HOST`).

Run: `go build -o pier ./cmd/pier`
Expected: binary builds.

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog for clock sync and build stderr surfacing"
```

- [ ] **Step 4: Manual verification (requires the VM, user-performed)**

1. Against a VM with a deliberately wrong clock (`sudo date -s 'yesterday'` on the guest): `pier bootstrap production` prints the `remote clock was Ns off; corrected to ...` line, then completes.
2. `pier deploy production` against a compose file with an undeclared volume: the `build failed` phase line shows the compose validation error (e.g. `service "s3" refers to undefined volume s3_data: invalid compose project`).
3. Re-run the real deploy on `192.168.122.63` end to end with the rebuilt binary.

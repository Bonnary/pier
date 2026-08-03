# Remote Shell / Exec Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pier shell <env>` opens an interactive bash in the remote `app` container of a deploy environment, and `pier exec <env> <cmd...>` runs a one-off command there, over the same SSH transport as `pier deploy`.

**Architecture:** The deploy package gains two entry points that dial with the existing `deploy.Dial`: `RemoteExec` (non-interactive, streams output, propagates the remote exit status) and `RemoteShell` (PTY session with raw-mode local terminal, resize forwarding, and terminal restore). The CLI's `shell`/`exec` commands take an optional env position resolved against `[deploy.<env>]` in pier.toml; the no-env forms keep today's local behavior exactly. Errors reuse the existing typed contract (`ExitError`, `Kind`, sentinels).

**Tech Stack:** Go 1.25, `spf13/cobra`, `golang.org/x/crypto/ssh`, `golang.org/x/term` (all already in go.mod — no new dependencies).

## Global Constraints

- No new dependencies. `golang.org/x/term` and `golang.org/x/crypto` are already in go.mod.
- Follow the typed error contract in `internal/deploy/errors.go`: dial failures keep their `ErrPreflight` wrap (CLI maps them to `SSHError`, mirroring `internal/cli/status.go`); remote exit statuses propagate as `*ExitError` with `Code` = remote status.
- Windows builds must not break: SIGWINCH handling goes in build-tagged files.
- The prod compose file is `docker-compose.prod.yml`; the app service is named `app`. Remote commands use the container's default user (no `-u`).
- TDD: write the failing test first in every task.
- Reuse existing helpers: `quoteShell` (`internal/deploy/bootstrap.go`), `writeTestKey`, `keyOnlyServer`, `testAddr`, `passwordOnlyServer` (`internal/deploy/testssh_test.go`), `capturingRunner`, `writeFile` (`internal/cli/*_test.go`).

---

### Task 1: RemoteExec in the deploy package

Adds the non-interactive remote command runner plus the remote command builders and the typed error helpers `RemoteCommandError` / `SessionError`, with an in-process fake SSH session server used by all later tasks' tests.

**Files:**
- Create: `internal/deploy/shell.go`
- Create: `internal/deploy/shell_test.go`
- Test: `internal/deploy/shell_test.go`

**Interfaces:**
- Consumes: `deploy.Dial(ctx, SSHConfig)` from `internal/deploy/ssh.go:63`; `quoteShell` from `internal/deploy/bootstrap.go:67`; `ExitError`, `ExitGeneral`, `KindSSH`, `KindUnknown` from `internal/deploy/errors.go`; test helpers `writeTestKey`, `keyOnlyServer`, `testAddr` from `internal/deploy/testssh_test.go`.
- Produces:
  - `RemoteExec(ctx context.Context, cfg SSHConfig, dir string, args []string) error` — dials, streams stdout/stderr, returns the remote exit status as a `*ExitError` (or nil on status 0).
  - `RemoteCommandError(host string, err error) error` — `*ExitError` with `Code` = remote exit status when `err` carries `*ssh.ExitError`, else `Code` = `ExitGeneral`, `Kind` = `KindSSH`.
  - `SessionError(host string, err error) error` — `*ExitError{Code: ExitGeneral, Kind: KindSSH, RemoteHost: host}`.
  - `remoteExecCommand(dir string, args []string) string` — the exact remote shell command string (later tasks' tests assert on it).
  - `remoteShellCommand(dir string) string`
  - `remotePrefix(dir string) string`
  - Test infra: `fakeSession` struct, `startFakeSession(t, scfg, f) string`, `parsePtyReq`, `finishFakeSession` (all in `internal/deploy/shell_test.go`, used by Task 2).

- [ ] **Step 1: Write the failing tests for the command builders**

Create `internal/deploy/shell.go` as an empty placeholder for now:

```go
package deploy
```

Create `internal/deploy/shell_test.go` with the fake session server infrastructure plus the command-builder tests:

```go
package deploy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeSession records session-channel requests (exec, shell, pty-req,
// env, window-change) and answers them with canned output and a
// configurable exit status, mirroring how the deploy host responds to
// `pier shell <env>` / `pier exec <env>`.
type fakeSession struct {
	mu      sync.Mutex
	cmds    []string
	shell   bool
	ptyTerm string
	ptyRows uint32
	ptyCols uint32
	reject  bool
	output  []byte
	status  int
}

func (f *fakeSession) addCmd(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
}

func (f *fakeSession) recordPty(term string, cols, rows uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ptyTerm, f.ptyCols, f.ptyRows = term, cols, rows
}

// startFakeSession starts an in-process SSH server that answers
// session channels with f's canned behavior. Returns the listen
// address ("127.0.0.1:PORT").
func startFakeSession(t *testing.T, scfg *ssh.ServerConfig, f *fakeSession) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("host key signer: %v", err)
	}
	scfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSessionConn(nc, scfg, f)
		}
	}()
	return ln.Addr().String()
}

func serveFakeSessionConn(nc net.Conn, scfg *ssh.ServerConfig, f *fakeSession) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, scfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		go serveFakeSessionChannel(ch, f)
	}
}

func serveFakeSessionChannel(ch ssh.NewChannel, f *fakeSession) {
	if ch.ChannelType() != "session" {
		_ = ch.Reject(ssh.UnknownChannelType, "unsupported channel type")
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			term, cols, rows := parsePtyReq(req.Payload)
			f.recordPty(term, cols, rows)
			_ = req.Reply(true, nil)
		case "env", "window-change":
			_ = req.Reply(true, nil)
		case "exec":
			if f.reject {
				_ = req.Reply(false, nil)
				return
			}
			f.addCmd(string(req.Payload[4:]))
			_ = req.Reply(true, nil)
			_, _ = channel.Write(f.output)
			finishFakeSession(channel, f.status)
			return
		case "shell":
			f.mu.Lock()
			f.shell = true
			f.mu.Unlock()
			_ = req.Reply(true, nil)
			_, _ = channel.Write(f.output)
			finishFakeSession(channel, f.status)
			return
		}
	}
}

// parsePtyReq decodes a pty-req payload: string term, uint32 cols,
// uint32 rows, uint32 pixel width, uint32 pixel height, string modes.
func parsePtyReq(payload []byte) (term string, cols, rows uint32) {
	termLen := int(binary.BigEndian.Uint32(payload[0:4]))
	term = string(payload[4 : 4+termLen])
	rest := payload[4+termLen:]
	return term, binary.BigEndian.Uint32(rest[0:4]), binary.BigEndian.Uint32(rest[4:8])
}

func finishFakeSession(ch ssh.Channel, status int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
	_ = ch.Close()
}

func TestRemoteExecCommand(t *testing.T) {
	got := remoteExecCommand("/srv/x", []string{"php", "artisan", "migrate"})
	want := "cd /srv/x && docker compose -f docker-compose.prod.yml exec -T app php artisan migrate"
	if got != want {
		t.Errorf("remoteExecCommand = %q, want %q", got, want)
	}
	got = remoteExecCommand("", []string{"php", "-v"})
	want = "docker compose -f docker-compose.prod.yml exec -T app php -v"
	if got != want {
		t.Errorf("remoteExecCommand (empty dir) = %q, want %q", got, want)
	}
	got = remoteExecCommand("/srv/x", []string{"php", "artisan", "migrate --force"})
	want = "cd /srv/x && docker compose -f docker-compose.prod.yml exec -T app php artisan 'migrate --force'"
	if got != want {
		t.Errorf("remoteExecCommand (quoting) = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestRemoteExecCommand -v`
Expected: FAIL — `remoteExecCommand` is undefined.

- [ ] **Step 3: Implement RemoteExec and the helpers**

Replace the placeholder in `internal/deploy/shell.go` with:

```go
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// remoteComposeFile is the prod compose file name, matching deploy's
// render and run stages.
const remoteComposeFile = "docker-compose.prod.yml"

// remotePrefix returns the `cd <dir> && ` prefix for a remote
// command, or "" when dir is empty (the compose file then resolves
// in the login directory).
func remotePrefix(dir string) string {
	if dir == "" {
		return ""
	}
	return "cd " + quoteShell(dir) + " && "
}

// remoteExecCommand builds the remote shell command for a one-off
// exec in the app service. Each arg is POSIX-quoted so it cannot
// break out of the remote shell command string.
func remoteExecCommand(dir string, args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteShell(a)
	}
	return remotePrefix(dir) + "docker compose -f " + remoteComposeFile + " exec -T app " + strings.Join(quoted, " ")
}

// remoteShellCommand builds the remote shell command for an
// interactive bash session in the app service. The remote PTY makes
// docker compose exec allocate a TTY, so no -T is passed.
func remoteShellCommand(dir string) string {
	return remotePrefix(dir) + "docker compose -f " + remoteComposeFile + " exec app bash"
}

// RemoteCommandError wraps a failed remote command. When err carries
// an *ssh.ExitError, pier's exit code mirrors the remote exit status;
// otherwise the failure is a session-level error and gets
// ExitGeneral. RemoteHost is stamped on both so the CLI renders the
// remote-aware hint.
func RemoteCommandError(host string, err error) error {
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{
			Code:       exitErr.ExitStatus(),
			Kind:       KindUnknown,
			RemoteHost: host,
			Err:        fmt.Errorf("remote command failed with exit status %d: %w", exitErr.ExitStatus(), err),
		}
	}
	return &ExitError{
		Code:       ExitGeneral,
		Kind:       KindSSH,
		RemoteHost: host,
		Err:        fmt.Errorf("remote command failed: %w", err),
	}
}

// SessionError wraps an SSH session-level failure (session creation,
// pty or command start, stream setup) on host.
func SessionError(host string, err error) error {
	return &ExitError{
		Code:       ExitGeneral,
		Kind:       KindSSH,
		RemoteHost: host,
		Err:        err,
	}
}

// RemoteExec runs a one-off command in the app service of the prod
// compose file on the remote host, streaming stdout/stderr to
// pier's own streams as they arrive. The remote exit status becomes
// pier's exit code via RemoteCommandError; a status-0 exit returns
// nil.
func RemoteExec(ctx context.Context, cfg SSHConfig, dir string, args []string) error {
	client, err := Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.remoteExec(ctx, cfg.Host, dir, args)
}

func (c *Client) remoteExec(ctx context.Context, host, dir string, args []string) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return SessionError(host, err)
	}
	defer sess.Close()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return SessionError(host, err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return SessionError(host, err)
	}
	if err := sess.Start(remoteExecCommand(dir, args)); err != nil {
		return SessionError(host, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(os.Stdout, stdout)
	}()
	_, _ = io.Copy(os.Stderr, stderr)
	<-done
	if err := sess.Wait(); err != nil {
		return RemoteCommandError(host, err)
	}
	return nil
}
```

- [ ] **Step 4: Add the RemoteExec behavior tests**

Append to `internal/deploy/shell_test.go`:

```go
// dialTestClient dials cfg against the fake session server and
// fails the test on error.
func dialTestClient(t *testing.T, cfg SSHConfig) *Client {
	t.Helper()
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}

func TestRemoteExecStreamsAndPropagatesExit(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("migrated\n"), status: 3}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))

	oldStdout, oldStderr := os.Stdout, os.Stderr
	var out, errOut bytes.Buffer
	os.Stdout, os.Stderr = &out, &errOut
	defer func() { os.Stdout, os.Stderr = oldStdout, oldStderr }()

	err := RemoteExec(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x", []string{"php", "artisan", "migrate"})
	if err == nil {
		t.Fatal("RemoteExec = nil error, want exit-status error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("error type = %T, want *ExitError", err)
	}
	if ee.Code != 3 {
		t.Errorf("exit code = %d, want 3", ee.Code)
	}
	if ee.RemoteHost != host {
		t.Errorf("RemoteHost = %q, want %q", ee.RemoteHost, host)
	}
	if got := out.String(); got != "migrated\n" {
		t.Errorf("stdout = %q, want %q", got, "migrated\n")
	}
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %v, want 1", fs.cmds)
	}
	want := "cd /srv/x && docker compose -f docker-compose.prod.yml exec -T app php artisan migrate"
	if fs.cmds[0] != want {
		t.Errorf("command = %q, want %q", fs.cmds[0], want)
	}
}

func TestRemoteExecSuccess(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	if err := RemoteExec(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x", []string{"php", "-v"}); err != nil {
		t.Errorf("RemoteExec = %v, want nil", err)
	}
}

func TestRemoteExecSessionRejected(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{reject: true}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	err := RemoteExec(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x", []string{"php", "-v"})
	if err == nil {
		t.Fatal("RemoteExec = nil error, want session error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("error type = %T, want *ExitError", err)
	}
	if ee.Kind != KindSSH {
		t.Errorf("kind = %v, want KindSSH", ee.Kind)
	}
	if ee.Code != ExitGeneral {
		t.Errorf("code = %d, want ExitGeneral", ee.Code)
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ -run 'TestRemoteExec' -v`
Expected: PASS (all four tests).

Run: `go vet ./internal/deploy/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/shell.go internal/deploy/shell_test.go
git commit -m "feat(deploy): remote one-off exec on deploy hosts"
```

---

### Task 2: Interactive RemoteShell with PTY

Adds `RemoteShell` / `Client.InteractiveShell`: TTY guard, pty request with the local terminal size, raw-mode stdin with restore, SIGWINCH resize forwarding (unix-only via build tags), and output streaming.

**Files:**
- Modify: `internal/deploy/shell.go`
- Create: `internal/deploy/winsize_unix.go`
- Create: `internal/deploy/winsize_windows.go`
- Test: `internal/deploy/shell_test.go`

**Interfaces:**
- Consumes: `fakeSession`, `startFakeSession`, `parsePtyReq`, `dialTestClient` (Task 1); `RemoteCommandError`, `SessionError`, `remoteShellCommand` (Task 1); `ExitError`, `ExitGeneral`, `KindUser`, `UserError` from `internal/deploy/errors.go`.
- Produces:
  - `ErrShellNoTTY` — sentinel wrapped by the non-TTY error.
  - `RemoteShell(ctx context.Context, cfg SSHConfig, dir string) error` — dials then delegates to `InteractiveShell`.
  - `(c *Client) InteractiveShell(ctx context.Context, dir string) error` — the full interactive session; requires a TTY on stdin.
  - Seams (package vars, overridable in tests): `shellIsTerminal func(int) bool`, `shellGetSize func(int) (int, int, error)`, `shellMakeRaw func(int) (func() error, error)`.
  - `notifyWindowChanges(ch chan<- os.Signal)` / `stopWindowChanges(ch chan<- os.Signal)` — unix: SIGWINCH wiring; windows: no-ops.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/shell_test.go`:

```go
func TestRemoteShellRequiresTTY(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	err := RemoteShell(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x")
	if !errors.Is(err, ErrShellNoTTY) {
		t.Fatalf("err = %v, want ErrShellNoTTY", err)
	}
	if len(fs.cmds) != 0 {
		t.Errorf("cmds = %v, want none (no shell without a TTY)", fs.cmds)
	}
}

func TestInteractiveShellPTYAndStreams(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("hi there\n"), status: 0}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))

	origIsT, origSize, origRaw := shellIsTerminal, shellGetSize, shellMakeRaw
	shellIsTerminal = func(int) bool { return true }
	shellGetSize = func(int) (int, int, error) { return 80, 24, nil }
	restored := false
	shellMakeRaw = func(int) (func() error, error) {
		return func() error { restored = true; return nil }, nil
	}
	defer func() { shellIsTerminal, shellGetSize, shellMakeRaw = origIsT, origSize, origRaw }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("exit\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	var out bytes.Buffer
	os.Stdin, os.Stdout = r, &out
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout; _ = r.Close() }()

	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()
	if err := client.InteractiveShell(context.Background(), "/srv/x"); err != nil {
		t.Fatalf("InteractiveShell: %v", err)
	}
	if !restored {
		t.Error("terminal restore func not called")
	}
	if fs.ptyTerm != "xterm-256color" {
		t.Errorf("pty term = %q, want xterm-256color", fs.ptyTerm)
	}
	if fs.ptyCols != 80 || fs.ptyRows != 24 {
		t.Errorf("pty size = %dx%d, want 80x24", fs.ptyCols, fs.ptyRows)
	}
	if got := out.String(); got != "hi there\n" {
		t.Errorf("stdout = %q, want %q", got, "hi there\n")
	}
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %v, want 1", fs.cmds)
	}
	want := "cd /srv/x && docker compose -f docker-compose.prod.yml exec app bash"
	if fs.cmds[0] != want {
		t.Errorf("command = %q, want %q", fs.cmds[0], want)
	}
}

func TestInteractiveShellExitStatus(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{status: 42}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))

	origIsT, origSize, origRaw := shellIsTerminal, shellGetSize, shellMakeRaw
	shellIsTerminal = func(int) bool { return true }
	shellGetSize = func(int) (int, int, error) { return 80, 24, nil }
	shellMakeRaw = func(int) (func() error, error) {
		return func() error { return nil }, nil
	}
	defer func() { shellIsTerminal, shellGetSize, shellMakeRaw = origIsT, origSize, origRaw }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_, _ = w.WriteString("exit\n")
	_ = w.Close()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, &bytes.Buffer{}
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout; _ = r.Close() }()

	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()
	err = client.InteractiveShell(context.Background(), "/srv/x")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *ExitError", err)
	}
	if ee.Code != 42 {
		t.Errorf("exit code = %d, want 42", ee.Code)
	}
}
```

Note: `TestRemoteShellRequiresTTY` relies on `go test` stdin being a non-TTY, so no seam overrides are needed there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestRemoteShell|TestInteractiveShell' -v`
Expected: FAIL — `ErrShellNoTTY`, `RemoteShell`, `InteractiveShell` undefined.

- [ ] **Step 3: Implement RemoteShell and InteractiveShell**

First update the imports block at the top of `internal/deploy/shell.go` so it reads:

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"

	"golang.org/x/crypto/ssh"
)
```

Then append to `internal/deploy/shell.go`:

```go
// not a terminal; an interactive remote shell would be unusable.
var ErrShellNoTTY = errors.New("remote shell requires a terminal")

// terminal seams, overridable in tests.
var (
	shellIsTerminal = func(fd int) bool { return term.IsTerminal(fd) }
	shellGetSize    = func(fd int) (int, int, error) { return term.GetSize(fd) }
	shellMakeRaw    = func(fd int) (func() error, error) {
		old, err := term.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		return func() error { return term.Restore(fd, old) }, nil
	}
)

// RemoteShell opens an interactive bash in the app service of the
// prod compose file on the remote host and returns when the remote
// shell exits. The remote exit status becomes pier's exit code.
func RemoteShell(ctx context.Context, cfg SSHConfig, dir string) error {
	client, err := Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.InteractiveShell(ctx, dir)
}

// InteractiveShell runs the remote interactive bash session: local
// terminal size → pty request, raw-mode stdin with restore (also on
// error paths), SIGWINCH → WindowChange forwarding, and byte-copying
// of stdin/stdout/stderr through the session. Requires a TTY on
// local stdin.
func (c *Client) InteractiveShell(ctx context.Context, dir string) error {
	fd := int(os.Stdin.Fd())
	if !shellIsTerminal(fd) {
		return UserError(fmt.Errorf("%w: local stdin is not a terminal", ErrShellNoTTY))
	}
	w, h, err := shellGetSize(fd)
	if err != nil {
		return SessionError(c.Config.Host, fmt.Errorf("read terminal size: %w", err))
	}
	restore, err := shellMakeRaw(fd)
	if err != nil {
		return SessionError(c.Config.Host, fmt.Errorf("raw mode: %w", err))
	}
	defer func() { _ = restore() }()

	sess, err := c.conn.NewSession()
	if err != nil {
		return SessionError(c.Config.Host, err)
	}
	defer sess.Close()

	if err := sess.RequestPty("xterm-256color", h, w, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		return SessionError(c.Config.Host, err)
	}
	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	winch := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		notifyWindowChanges(winch)
		defer stopWindowChanges(winch)
		for {
			select {
			case <-winch:
				w, h, err := shellGetSize(fd)
				if err == nil {
					_ = sess.WindowChange(h, w)
				}
			case <-done:
				return
			}
		}
	}()

	if err := sess.Start(remoteShellCommand(dir)); err != nil {
		close(done)
		return SessionError(c.Config.Host, err)
	}
	err = sess.Wait()
	close(done)
	if err != nil {
		return RemoteCommandError(c.Config.Host, err)
	}
	return nil
}
```

Update the imports block at the top of `internal/deploy/shell.go` so the full list reads:

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"

	"golang.org/x/crypto/ssh"
)
```

Note: `term.GetSize` returns `(width, height)`; `RequestPty` takes `(term, height, width)` — the `w, h` unpacking above keeps the sizes in the right slots.

Note: the session is started with `sess.Start(remoteShellCommand(dir))`, which sends an `exec` request whose command string is the compose command; the fake server records it under `cmds` (there is no separate `shell` request).

- [ ] **Step 4: Create the SIGWINCH build-tagged files**

Create `internal/deploy/winsize_unix.go`:

```go
//go:build !windows

package deploy

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyWindowChanges forwards SIGWINCH to ch on unix platforms; on
// windows it is a no-op (winsize_windows.go).
func notifyWindowChanges(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}

// stopWindowChanges stops forwarding SIGWINCH to ch.
func stopWindowChanges(ch chan<- os.Signal) {
	signal.Stop(ch)
}
```

Create `internal/deploy/winsize_windows.go`:

```go
//go:build windows

package deploy

import "os"

// notifyWindowChanges is a no-op on windows: there is no SIGWINCH.
func notifyWindowChanges(ch chan<- os.Signal) {}

// stopWindowChanges is a no-op on windows.
func stopWindowChanges(ch chan<- os.Signal) {}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ -run 'TestRemoteShell|TestInteractiveShell' -v`
Expected: PASS (all three tests).

Run: `go vet ./internal/deploy/ && go build ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/shell.go internal/deploy/shell_test.go internal/deploy/winsize_unix.go internal/deploy/winsize_windows.go
git commit -m "feat(deploy): interactive remote shell with pty"
```

---

### Task 3: `pier shell [env]` targets a deploy host

Wire the CLI: `pier shell` keeps its local behavior; `pier shell <env>` resolves `[deploy.<env>]` and calls `deploy.RemoteShell`.

**Files:**
- Modify: `internal/cli/shell.go`
- Create: `internal/cli/shell_test.go`
- Test: `internal/cli/shell_test.go`

**Interfaces:**
- Consumes: `deploy.RemoteShell(ctx, SSHConfig, dir) error` (Task 2); `newSSHConfig` (`internal/cli/helpers.go:37`); `deploy.ErrPreflight`, `deploy.ErrAborted`; `SSHError` (`internal/cli/errors.go`); `capturingRunner` (`internal/cli/exec_test.go`), `writeFile` (`internal/cli/dev_test.go`).
- Produces: seam `remoteShellFn` (package var) and `runRemoteShell(cmd *cobra.Command, cfg *config.Config, env string) error` for later tasks' tests.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/shell_test.go`:

```go
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

const shellTestToml = "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n"

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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestShell' -v`
Expected: FAIL — `remoteShellFn` undefined.

- [ ] **Step 3: Implement the env-aware shell command**

Replace `internal/cli/shell.go` with:

```go
package cli

import (
	"context"
	"errors"
	"io"
	"os/user"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

// remoteShellFn is a test seam for `pier shell <env>`; production
// code dials and runs the interactive remote shell.
var remoteShellFn = deploy.RemoteShell

func newShellCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "shell [env]",
		Short: "Open an interactive bash in the app container (add <env> to target a deploy host)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd, args)
		},
	}
}

func runShell(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		return runRemoteShell(cmd, cfg, args[0])
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	tty := docker.DetectTTY()
	if err := ensureUp(cmd, c); err != nil {
		return err
	}
	return c.Exec(context.Background(), docker.ExecOpts{Service: "laravel.test", User: "0", TTY: tty}, "bash")
}

// runRemoteShell runs the interactive remote shell on the
// [deploy.<env>] host. Dial/handshake failures (ErrPreflight) map to
// the SSH error kind; an interactive abort and every typed remote
// error pass through unchanged.
func runRemoteShell(cmd *cobra.Command, cfg *config.Config, env string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	err := remoteShellFn(cmd.Context(), newSSHConfig(dc), dc.Path)
	if errors.Is(err, deploy.ErrAborted) {
		return err
	}
	if errors.Is(err, deploy.ErrPreflight) {
		return SSHError(err)
	}
	return err
}

func shellUser() string {
	u, err := user.Current()
	if err != nil {
		return "www-data"
	}
	if u.Uid == "0" {
		return "www-data"
	}
	return u.Uid
}

func ensureUp(cmd *cobra.Command, c *docker.Compose) error {
	ps, err := c.PS(context.Background())
	if err != nil {
		return err
	}
	if !containsString(string(ps), "laravel.test") {
		return ExecDownError()
	}
	return nil
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

Note: the old `runShell` had a stray `_ = errors.New("")` line in `ensureUp`; it is dropped here. `shellUser` and `containsString` are unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestShell' -v`
Expected: PASS (all five tests).

Run: `go test ./internal/cli/`
Expected: PASS (no regressions).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/shell.go internal/cli/shell_test.go
git commit -m "feat(cli): pier shell <env> targets a deploy host"
```

---

### Task 4: `pier exec [env] <cmd...>` targets a deploy host

Wire the CLI: the first positional argument is treated as an env iff it names a configured `[deploy.<env>]`; a lone env name (no command) is a clear error; anything else stays local.

**Files:**
- Modify: `internal/cli/exec.go`
- Modify: `internal/cli/exec_test.go`
- Test: `internal/cli/exec_test.go`

**Interfaces:**
- Consumes: `deploy.RemoteExec(ctx, SSHConfig, dir string, args []string) error` (Task 1); `newSSHConfig`; `deploy.ErrPreflight`, `deploy.ErrAborted`; `SSHError`; `capturingRunner`, `writeFile`.
- Produces: seam `remoteExecFn` (package var), `resolveRemoteExec(cfg *config.Config, args []string) (env string, rest []string, remote bool, err error)`, `runRemoteExec(cmd *cobra.Command, cfg *config.Config, env string, args []string) error`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/exec_test.go`:

```go
func TestExecRemoteEnvDetection(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
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
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
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
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "php", "artisan", "--version"})
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
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
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
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "production", "php", "-v"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute = nil error, want exit error")
	}
	if got := ExitCode(err); got != 7 {
		t.Errorf("exit code = %d, want 7", got)
	}
}
```

Update the imports block of `internal/cli/exec_test.go`. The current imports are:

```go
import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/docker"
)
```

so the new block is:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestExec' -v`
Expected: FAIL — `remoteExecFn` undefined.

- [ ] **Step 3: Implement the env-aware exec command**

Replace `internal/cli/exec.go` with:

```go
package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

// remoteExecFn is a test seam for `pier exec <env> <cmd...>`.
var remoteExecFn = deploy.RemoteExec

func newExecCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "exec [env] <cmd...>",
		Short: "Run a one-off command in the app container (prefix <env> to target a deploy host)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}
}

func runExec(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if env, rest, remote, cerr := resolveRemoteExec(cfg, args); cerr != nil {
		return cerr
	} else if remote {
		return runRemoteExec(cmd, cfg, env, rest)
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	tty := docker.DetectTTY()
	if err := ensureUp(cmd, c); err != nil {
		return err
	}
	return c.Exec(context.Background(), docker.ExecOpts{Service: "laravel.test", User: shellUser(), TTY: tty}, args...)
}

// resolveRemoteExec decides whether args select a remote exec: the
// first argument must name a configured deploy env and be followed by
// at least one command argument. A lone env name is a clear error
// rather than a match; a first argument that names no env is a local
// exec. Returns remote=false when nothing matches.
func resolveRemoteExec(cfg *config.Config, args []string) (env string, rest []string, remote bool, err error) {
	if len(args) == 0 {
		return "", nil, false, nil
	}
	env = args[0]
	if _, ok := cfg.Deploy[env]; !ok {
		return "", nil, false, nil
	}
	if len(args) < 2 {
		return "", nil, false, cliError("remote exec: no command given for env %q", env)
	}
	return env, args[1:], true, nil
}

// runRemoteExec runs the one-off remote command on the
// [deploy.<env>] host. Dial/handshake failures (ErrPreflight) map to
// the SSH error kind; an interactive abort and every typed remote
// error pass through unchanged.
func runRemoteExec(cmd *cobra.Command, cfg *config.Config, env string, args []string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	err := remoteExecFn(cmd.Context(), newSSHConfig(dc), dc.Path, args)
	if errors.Is(err, deploy.ErrAborted) {
		return err
	}
	if errors.Is(err, deploy.ErrPreflight) {
		return SSHError(err)
	}
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestExec' -v`
Expected: PASS (all six tests, including the pre-existing `TestExecBuildsCommand`).

Run: `go test ./internal/cli/`
Expected: PASS (no regressions).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/exec.go internal/cli/exec_test.go
git commit -m "feat(cli): pier exec <env> <cmd...> targets a deploy host"
```

---

### Task 5: Document the remote forms

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the feature bullet**

In README.md, replace the feature bullet (around line 62):

```markdown
- **`pier shell` / `pier exec`** — Interactive bash in the
  `laravel.test` container, or one-off commands in it (e.g.
  `pier exec php artisan migrate`). Add a deploy env name to target
  the remote host instead: `pier shell production` opens bash in the
  production `app` container; `pier exec production php artisan
  migrate` runs a one-off command there. Remote exit codes propagate
  to pier's exit code.
```

with:

```markdown
- **`pier shell [env]` / `pier exec [env] <cmd...>`** — Interactive
  bash in the `laravel.test` container, or one-off commands in it
  (e.g. `pier exec php artisan migrate`). Add a deploy env name to
  target the remote host instead: `pier shell production` opens an
  interactive bash in the production `app` container (PTY, resize
  forwarding); `pier exec production php artisan migrate` runs a
  one-off command there. Remote exit codes propagate to pier's exit
  code.
```

- [ ] **Step 2: Update the command table**

In README.md, replace the table rows (around lines 224-225):

```markdown
| `pier shell` | Interactive `bash` in the `laravel.test` container. |
| `pier exec <cmd...>` | Run a one-off command in `laravel.test`. |
```

with:

```markdown
| `pier shell [env]` | Interactive `bash` in the `laravel.test` container, or in the remote `app` container when `<env>` names a deploy host (PTY, resize forwarding). |
| `pier exec [env] <cmd...>` | Run a one-off command in `laravel.test`, or in the remote `app` container when the first arg names a deploy env. |
```

- [ ] **Step 3: Update the quick-start example**

In README.md, replace the example block (around lines 183-185):

```markdown
pier shell             # interactive bash in laravel.test
pier exec php artisan migrate
```

with:

```markdown
pier shell                        # interactive bash in laravel.test
pier exec php artisan migrate
pier shell production             # interactive bash in the prod app container
pier exec production php artisan migrate   # one-off command on prod
```

- [ ] **Step 4: Verify the diff**

Run: `git diff README.md`
Expected: only the three documented changes.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document remote shell/exec env forms"
```

---

### Task 6: Full-suite verification

- [ ] **Step 1: Run the full test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS, no vet output.

- [ ] **Step 2: Smoke-check the CLI surface**

Run: `go run ./cmd/pier --help`
Expected: usage lists `shell [env]` and `exec [env] <cmd...>` with the updated short descriptions.

- [ ] **Step 3: Final commit of any stragglers**

```bash
git status --short
git add -A
git commit -m "chore: final touches" || true
```

(Empty when everything was already committed; `|| true` makes the commit a no-op in that case.)
```

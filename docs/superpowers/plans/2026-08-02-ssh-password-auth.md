# SSH Password Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let pier connect to remote hosts that reject SSH keys by falling back to an interactive password prompt, and replace the rsync subprocess with an SFTP copy over pier's own connection.

**Architecture:** `Dial` (internal/deploy/ssh.go) tries the configured key first; only on an auth-class failure (`*ssh.ServerAuthError`) does it obtain a password (pre-supplied `Password` field or a `PasswordPrompt` callback) and retry with `ssh.Password` + a `ssh.KeyboardInteractive` responder. The deploy pipeline's sync phase moves from `rsync -az -e ssh` to `Client.SyncDir`, which copies files over SFTP (`github.com/pkg/sftp`) reusing the open connection and the existing exclude rules. The CLI wires a `term.ReadPassword`-based prompt into every `SSHConfig` it constructs. Password is never stored; no pier.toml schema changes.

**Tech Stack:** Go 1.25+, `golang.org/x/crypto/ssh` (client + in-process test server), `github.com/pkg/sftp`, `golang.org/x/term`.

## Global Constraints

- Go 1.25+ (go.mod `go 1.25.0`); run `go test -race ./...` before each commit.
- Password is **never stored**: only the `Password` test seam and the interactive `PasswordPrompt`. No pier.toml schema changes.
- Error contract: preflight failures are exit 2 (`ErrPreflight`, KindConfig); user aborts are exit 130 (`ErrAborted`, KindUser). `ExitError.Is` maps codes to sentinels; `cli.ExitCode` reads the outermost `*ExitError`.
- TDD: write the failing test first, verify it fails, implement, verify it passes, commit.
- Every exported type/func/method needs a Go doc comment (repo convention, enforced by review).
- Style: `gofmt`; linters per `.golangci.yml` (errcheck, goimports, govet, ineffassign, staticcheck, unused, gocyclo ≤30).
- Commit message style from `git log`: `feat(<scope>): ...`, `fix(<scope>): ...`, `docs: ...`, `test: ...`, `refactor: ...`.

---

### Task 1: Dial password fallback (Password field) + in-process SSH test server

**Files:**
- Create: `internal/deploy/testssh_test.go` (in-process SSH + SFTP test server helper, ephemeral ed25519 key helper)
- Create: `internal/deploy/auth_test.go` (Dial fallback tests)
- Modify: `internal/deploy/ssh.go:21-83` (SSHConfig fields + Dial rewrite)

**Interfaces:**
- Consumes: existing `SSHConfig{Host, User, KeyPath, Port}`, `ErrPreflight`, `Client`.
- Produces: `SSHConfig.Password string` field; `Dial` semantics: key tried first; missing key file or auth-class failure falls back to `Password`; `startSSHServer(t *testing.T, scfg *ssh.ServerConfig) string` test helper (serves the sftp subsystem); `writeTestKey(t *testing.T) (string, ssh.PublicKey)` (returns key path + pubkey, key generated at runtime — no committed fixtures, no ssh-keygen dependency); `passwordOnlyServer() *ssh.ServerConfig`; `keyOnlyServer(pub ssh.PublicKey) *ssh.ServerConfig`.

- [ ] **Step 1: Write the test server helper**

Create `internal/deploy/testssh_test.go`:

```go
package deploy

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startSSHServer starts an in-process SSH server on 127.0.0.1 that
// authenticates via scfg and serves the sftp subsystem on "session"
// channels. It returns the listen address ("127.0.0.1:PORT").
func startSSHServer(t *testing.T, scfg *ssh.ServerConfig) string {
	t.Helper()
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
			go serveTestSSHConn(nc, scfg)
		}
	}()
	return ln.Addr().String()
}

func serveTestSSHConn(nc net.Conn, scfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, scfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		go serveTestSSHChannel(ch)
	}
}

func serveTestSSHChannel(ch ssh.NewChannel) {
	if ch.ChannelType() != "session" {
		ch.Reject(ssh.UnknownChannelType, "unsupported channel type")
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			if string(req.Payload[4:]) == "sftp" {
				req.Reply(true, nil)
				sftp.NewServer(channel)
				return
			}
			req.Reply(false, nil)
		case "exec", "shell", "env", "pty-req":
			req.Reply(false, nil)
		}
	}
}

// testAddr splits addr into host and port for SSHConfig.
func testAddr(t *testing.T, addr string) (host string, port int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port %q: %v", p, err)
	}
	return h, port
}

// writeTestKey generates an ed25519 key pair at runtime, writes the
// private key as a PEM PKCS8 file (readable by ssh.ParsePrivateKey),
// and returns the file path and the public key. Nothing is committed.
func writeTestKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	return path, signer.PublicKey()
}

// passwordOnlyServer returns a ServerConfig accepting password
// "secret" for user "deploy".
func passwordOnlyServer() *ssh.ServerConfig {
	sc := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == "deploy" && string(pw) == "secret" {
				return nil, nil
			}
			return nil, errAuthFailed
		},
	}
	sc.SetDefaults()
	return sc
}

// keyOnlyServer returns a ServerConfig accepting only the given
// public key for user "deploy".
func keyOnlyServer(pub ssh.PublicKey) *ssh.ServerConfig {
	sc := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "deploy" && string(key.Marshal()) == string(pub.Marshal()) {
				return nil, nil
			}
			return nil, errAuthFailed
		},
	}
	sc.SetDefaults()
	return sc
}

var errAuthFailed = &authError{}

type authError struct{}

func (*authError) Error() string { return "authentication failed" }
```

- [ ] **Step 2: Write the failing Dial fallback tests**

Create `internal/deploy/auth_test.go`:

```go
package deploy

import (
	"context"
	"errors"
	"testing"
)

func TestDialPasswordAuthSucceeds(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  keyPath,
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial(key rejected + password) = %v, want success", err)
	}
	c.Close()
}

func TestDialPasswordWrongPasswordFails(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  keyPath,
		Password: "wrong",
	})
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("Dial(wrong password) = %v, want ErrPreflight", err)
	}
}

func TestDialNoPasswordSourceFails(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
	})
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("Dial(no password source) = %v, want ErrPreflight", err)
	}
}

func TestDialMissingKeyFileUsesPassword(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  "/nonexistent/key",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial(missing key + password) = %v, want success", err)
	}
	c.Close()
}

func TestDialKeyAuthStillWorks(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	addr := startSSHServer(t, keyOnlyServer(pub))
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("Dial(key) = %v, want success", err)
	}
	c.Close()
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestDial' -v`
Expected: FAIL — `ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]` (password fallback not implemented yet; `Password` field doesn't exist — compile error `unknown field 'Password'` in `SSHConfig`).

- [ ] **Step 4: Add the `Password` field and rewrite `Dial`**

Modify `internal/deploy/ssh.go:21-26`:

```go
// SSHConfig is everything Dial needs to open a connection: target
// host, login user, path to the private key on the local filesystem,
// and the TCP port (0 means 22). When the server rejects the key
// (or the key file does not exist), Dial falls back to a password:
// Password wins, then PasswordPrompt (which may be nil to forbid
// prompting).
type SSHConfig struct {
	Host           string
	User           string
	KeyPath        string
	Port           int
	Password       string
	PasswordPrompt func() (string, error)
}
```

Replace the body of `Dial` (internal/deploy/ssh.go:52-83) with:

```go
// Dial opens an SSH connection to cfg and returns a ready Client.
// The host key is NOT verified (InsecureIgnoreHostKey) — the
// out-of-scope v1 list explicitly defers strict host-key checking.
// Key auth is tried first; if the key file is missing or the server
// rejects every offered key (an auth-class failure), Dial falls back
// to the password from cfg.Password or cfg.PasswordPrompt and retries
// once. A password fallback is attempted only when a password source
// exists; otherwise the original handshake error is returned.
func Dial(ctx context.Context, cfg SSHConfig) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("%w: empty host", ErrPreflight)
	}
	if cfg.KeyPath == "" {
		return nil, fmt.Errorf("%w: empty key path", ErrPreflight)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.port())
	auth := []ssh.AuthMethod(nil)
	if key, err := os.ReadFile(cfg.KeyPath); err == nil {
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("%w: parse key: %v", ErrPreflight, err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	var firstErr error
	if len(auth) > 0 {
		conn, err := dialOnce(ctx, addr, cfg, auth)
		if err == nil {
			return &Client{Config: cfg, conn: conn}, nil
		}
		firstErr = err
		if !isAuthFailure(err) {
			return nil, fmt.Errorf("%w: handshake %s: %v", ErrPreflight, addr, err)
		}
	}
	pw, ok, err := passwordFor(cfg)
	if err != nil {
		return nil, err
	}
	if !ok {
		if firstErr != nil {
			return nil, fmt.Errorf("%w: handshake %s: %v", ErrPreflight, addr, firstErr)
		}
		return nil, fmt.Errorf("%w: no ssh key at %s and no password source", ErrPreflight, cfg.KeyPath)
	}
	conn, err := dialOnce(ctx, addr, cfg, []ssh.AuthMethod{
		ssh.Password(pw),
		ssh.KeyboardInteractive(keyboardResponder(pw)),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: handshake %s: %v", ErrPreflight, addr, err)
	}
	return &Client{Config: cfg, conn: conn}, nil
}

// dialOnce opens a TCP connection and performs the SSH handshake
// with the given auth methods. The TCP connection is closed on
// handshake failure.
func dialOnce(ctx context.Context, addr string, cfg SSHConfig, auth []ssh.AuthMethod) (*ssh.Client, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	tcpConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	ncc, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		tcpConn.Close()
		return nil, err
	}
	return ssh.NewClient(ncc, chans, reqs), nil
}

// isAuthFailure reports whether err is an SSH authentication-class
// failure (the server rejected the offered methods) rather than a
// network, protocol, or negotiation error.
func isAuthFailure(err error) bool {
	var sae *ssh.ServerAuthError
	return errors.As(err, &sae)
}

// passwordFor resolves the password for the fallback auth attempt:
// the pre-supplied Password field wins, then PasswordPrompt. ok is
// false when neither source exists. Prompt errors are returned as-is
// so an interactive cancel can surface as an abort.
func passwordFor(cfg SSHConfig) (pw string, ok bool, err error) {
	if cfg.Password != "" {
		return cfg.Password, true, nil
	}
	if cfg.PasswordPrompt != nil {
		pw, err := cfg.PasswordPrompt()
		if err != nil {
			return "", false, err
		}
		return pw, true, nil
	}
	return "", false, nil
}

// keyboardResponder returns an ssh.KeyboardInteractive responder that
// answers every prompt with pw. Used for servers that only advertise
// the keyboard-interactive method (typical PAM setups).
func keyboardResponder(pw string) ssh.KeyboardInteractiveChallenge {
	return func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range answers {
			answers[i] = pw
		}
		return answers, nil
	}
}
```

Add `"errors"` to the imports of `internal/deploy/ssh.go`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ -run 'TestDial|TestSSHConfig' -v`
Expected: PASS (all 5 new tests + existing `TestSSHConfigDefaults`, `TestDialRejectsEmptyHost`).

Note: `TestDialRejectsEmptyHost` (ssh_test.go:19) passes `KeyPath: "/nonexistent"` with an empty Host — it still fails on the empty-host check first, unchanged.

- [ ] **Step 6: Run the full deploy package test suite**

Run: `go test ./internal/deploy/... && go mod tidy`
Expected: PASS (no existing tests break; `go mod tidy` records `github.com/pkg/sftp` from the test helper import).

- [ ] **Step 7: Commit**

```bash
git add internal/deploy/ssh.go internal/deploy/testssh_test.go internal/deploy/auth_test.go go.mod go.sum
git commit -m "feat(deploy): fall back to SSH password auth when keys are rejected"
```

---

### Task 2: PasswordPrompt wiring, interactive abort

**Files:**
- Modify: `internal/deploy/auth_test.go`
- Modify: `internal/deploy/ssh.go` (one line: abort mapping)

**Interfaces:**
- Consumes: `SSHConfig.PasswordPrompt`, `passwordFor`, `keyboardResponder` (Task 1).
- Produces: `Dial` returns `AbortedError()` (exit 130, KindUser, `ErrAborted`) when `PasswordPrompt` errors; prompt invoked at most once; keyboard-interactive-only servers authenticate.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/auth_test.go`:

```go
func TestDialPromptFallback(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	called := 0
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
		PasswordPrompt: func() (string, error) {
			called++
			return "secret", nil
		},
	})
	if err != nil {
		t.Fatalf("Dial(prompt) = %v, want success", err)
	}
	c.Close()
	if called != 1 {
		t.Errorf("prompt called %d times, want 1", called)
	}
}

func TestDialPromptNotCalledWhenKeyWorks(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	addr := startSSHServer(t, keyOnlyServer(pub))
	host, port := testAddr(t, addr)
	called := 0
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
		PasswordPrompt: func() (string, error) {
			called++
			return "secret", nil
		},
	})
	if err != nil {
		t.Fatalf("Dial(key) = %v, want success", err)
	}
	c.Close()
	if called != 0 {
		t.Errorf("prompt called %d times, want 0", called)
	}
}

func TestDialPromptAbort(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
		PasswordPrompt: func() (string, error) {
			return "", errors.New("interrupted")
		},
	})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Dial(aborted prompt) = %v, want ErrAborted", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitAborted {
		t.Fatalf("Dial(aborted prompt) = %T, want *ExitError with code %d", err, ExitAborted)
	}
}

// keyboardOnlyServer returns a ServerConfig that only accepts
// keyboard-interactive auth for user "deploy" with password "secret".
func keyboardOnlyServer() *ssh.ServerConfig {
	sc := &ssh.ServerConfig{
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, challenges ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			if c.User() != "deploy" {
				return nil, errAuthFailed
			}
			answers, err := challenges("", "", []string{"Password: "}, []bool{false})
			if err != nil || len(answers) != 1 || answers[0] != "secret" {
				return nil, errAuthFailed
			}
			return nil, nil
		},
	}
	sc.SetDefaults()
	return sc
}

func TestDialKeyboardInteractive(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, keyboardOnlyServer())
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  keyPath,
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial(keyboard-interactive) = %v, want success", err)
	}
	c.Close()
}
```

(`errors` is already imported in `auth_test.go` from Task 1.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestDial(Prompt|Keyboard)' -v`
Expected: FAIL — `TestDialPromptAbort`: Dial returns the raw prompt error, not `ErrAborted`. `TestDialKeyboardInteractive` and `TestDialPromptFallback` pass already because Task 1's retry slice contains both `ssh.Password` and the keyboard-interactive responder (validated here before moving on).

- [ ] **Step 3: Make `Dial` map prompt errors to abort**

In `internal/deploy/ssh.go`, replace the fallback section of `Dial`:

```go
	pw, ok, err := passwordFor(cfg)
	if err != nil {
		return nil, err
	}
```

with:

```go
	pw, ok, err := passwordFor(cfg)
	if err != nil {
		// An interactive cancel surfaces as an abort so the CLI
		// exits 130 instead of a preflight error.
		return nil, AbortedError()
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ -run 'TestDial' -v`
Expected: PASS (all Dial tests, including the new prompt/abort/keyboard-interactive ones).

- [ ] **Step 5: Run the full deploy suite and commit**

```bash
go test -race ./internal/deploy/...
git add internal/deploy/ssh.go internal/deploy/auth_test.go
git commit -m "feat(deploy): interactive password prompt with abort on cancel"
```

---

### Task 3: Abort passthrough in pipeline preflight and `status`

**Files:**
- Modify: `internal/deploy/deploy.go:58-62` (pipeline preflight error wrap)
- Modify: `internal/cli/status.go:69-72` (status dial error wrap)
- Modify: `internal/deploy/deploy_unit_test.go` (pipeline abort test)
- Modify: `internal/cli/status_test.go` (status abort test)

**Interfaces:**
- Consumes: `AbortedError()`, `ErrAborted` (existing), `pipelineDial` seam (deploy.go:20), `statusDial` seam (status.go:20).
- Produces: `Pipeline.Run` returns the abort unchanged (exit 130); `runRemoteStatus` returns the abort unchanged.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deploy/deploy_unit_test.go`:

```go
func TestPipelineAbortPropagates(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	origDial := pipelineDial
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		return nil, AbortedError()
	}
	defer func() { pipelineDial = origDial }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: "/nonexistent"},
		Now:       time.Now,
	}
	err := p.Run(context.Background())
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run() = %v, want ErrAborted", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitAborted {
		t.Fatalf("Run() error = %T, want *ExitError with code %d", err, ExitAborted)
	}
}
```

Add `"errors"` to the imports of `deploy_unit_test.go` (it already imports `context`, `io`, `path/filepath`, `testing`, `time`, and `config`).

Append to `internal/cli/status_test.go`:

```go
func TestStatusRemoteAbortPropagates(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	origDial := statusDial
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		return nil, deploy.AbortedError()
	}
	defer func() { statusDial = origDial }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	err := root.Execute()
	if !errors.Is(err, deploy.ErrAborted) {
		t.Fatalf("Execute() = %v, want ErrAborted", err)
	}
}
```

Check `status_test.go` imports — add `"errors"` if missing (`os`, `path/filepath`, `bytes`, `context`, `testing`, `net/http`, `net/http/httptest`, `strings`, `github.com/Bonnary/pier/internal/config`, `github.com/Bonnary/pier/internal/deploy` are already imported).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestPipelineAbortPropagates -v && go test ./internal/cli/ -run TestStatusRemoteAbortPropagates -v`
Expected: FAIL — `TestPipelineAbortPropagates`: `Run()` wraps the abort in `PreflightError` → `errors.Is(err, ErrAborted)` false, exit code 2. `TestStatusRemoteAbortPropagates`: `runRemoteStatus` wraps the abort in `SSHError` → `errors.Is(err, deploy.ErrAborted)` false.

- [ ] **Step 3: Let the pipeline pass aborts through**

Modify `internal/deploy/deploy.go:58-62`:

```go
	client, err := p.preflight(ctx)
	if err != nil {
		p.Logger.PhaseEnd("preflight", err)
		return PreflightError(err)
	}
```

to:

```go
	client, err := p.preflight(ctx)
	if err != nil {
		p.Logger.PhaseEnd("preflight", err)
		// An interactive abort (Ctrl+C on the password prompt) is
		// not a preflight failure: it must exit 130, not 2.
		if errors.Is(err, ErrAborted) {
			return err
		}
		return PreflightError(err)
	}
```

Add `"errors"` to the imports of `internal/deploy/deploy.go` (it currently imports `context`, `fmt`, `time`, and the config/stack packages).

- [ ] **Step 4: Let `status` pass aborts through**

Modify `internal/cli/status.go:69-72`:

```go
	client, err := statusDial(cmd.Context(), deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()})
	if err != nil {
		return SSHError(err)
	}
```

to:

```go
	client, err := statusDial(cmd.Context(), deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()})
	if err != nil {
		if errors.Is(err, deploy.ErrAborted) {
			return err
		}
		return SSHError(err)
	}
```

Add `"errors"` to the imports of `internal/cli/status.go`. (The literal `deploy.SSHConfig{...}` becomes `newSSHConfig(dc)` in Task 6.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ ./internal/cli/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/deploy.go internal/deploy/deploy_unit_test.go internal/cli/status.go internal/cli/status_test.go
git commit -m "fix(deploy): surface interactive aborts as exit 130 across commands"
```

---

### Task 4: Exclude matcher for the SFTP sync

**Files:**
- Create: `internal/deploy/syncfilter.go`
- Create: `internal/deploy/syncfilter_test.go`
- Modify: `internal/deploy/rsync.go:46-58` (delete the `rsyncExcludes` var here — it moves to `syncfilter.go`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `pathExcluded(rel string, excludes []string) bool` — include rules win over exclude rules (fixes the rsync first-match bug where `.env.production` was silently dropped); anchored patterns (`storage/logs/*`) prefix-match; non-anchored patterns match any path component with `*` glob. `rsyncExcludes` moves to `syncfilter.go` unchanged.

- [ ] **Step 1: Write the failing matcher tests**

Create `internal/deploy/syncfilter_test.go`:

```go
package deploy

import "testing"

func TestPathExcluded(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"composer.json", false},
		{".git/config", true},
		{"vendor/composer/autoload.php", true},
		{"node_modules/foo/index.js", true},
		{"resources/css/app.css", false},
		{".env", true},
		{".env.staging", true},
		{".env.production", false}, // include rule overrides .env.*
		{"storage/logs/laravel.log", true},
		{"storage/logs", false}, // dir itself is kept; children pruned
		{".idea/workspace.xml", true},
		{".vscode/settings.json", true},
		{"note.swp", true},
		{".DS_Store", true},
		{"sub/dir/.env.production", false},
		{"sub/dir/.env.local", true},
	}
	for _, c := range cases {
		if got := pathExcluded(c.path, rsyncExcludes); got != c.want {
			t.Errorf("pathExcluded(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/deploy/ -run TestPathExcluded -v`
Expected: FAIL — `pathExcluded` is not defined.

- [ ] **Step 3: Implement the matcher**

Create `internal/deploy/syncfilter.go`:

```go
package deploy

import "strings"

// rsyncExcludes is the default set of paths pier skips when syncing
// the project tree to the deploy host: version control, build
// artifacts, secrets, editor state, and macOS metadata. .env.production
// is allowed through; everything else starting with .env is dropped.
// The list keeps the rsync CLI shape (--include= / --exclude=) so it
// stays recognizable; pathExcluded interprets it directly.
var rsyncExcludes = []string{
	"--exclude=.git",
	"--exclude=node_modules",
	"--exclude=vendor",
	"--exclude=.env",
	"--exclude=.env.*",
	"--include=.env.production",
	"--exclude=storage/logs/*",
	"--exclude=.idea",
	"--exclude=.vscode",
	"--exclude=*.swp",
	"--exclude=.DS_Store",
}

// pathExcluded reports whether the relative path rel should be
// skipped when syncing. Include rules are checked first, so an
// include pattern always wins over an earlier exclude pattern —
// rsync's first-match-wins would let --exclude=.env.* drop
// .env.production, so the SFTP sync fixes that by design.
func pathExcluded(rel string, excludes []string) bool {
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--include=") {
			continue
		}
		if matchPattern(rel, strings.TrimPrefix(rule, "--include=")) {
			return false
		}
	}
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--exclude=") {
			continue
		}
		if matchPattern(rel, strings.TrimPrefix(rule, "--exclude=")) {
			return true
		}
	}
	return false
}

// matchPattern matches rel against a single rsync-style pattern.
// Patterns containing a slash are anchored at the root (rsync
// semantics); patterns without a slash match any path component.
func matchPattern(rel, pattern string) bool {
	if strings.Contains(pattern, "/") {
		// Anchored: "storage/logs/*" → any path under "storage/logs/".
		return strings.HasPrefix(rel, strings.TrimSuffix(pattern, "*"))
	}
	for _, comp := range strings.Split(rel, "/") {
		if globMatch(comp, pattern) {
			return true
		}
	}
	return false
}

// globMatch reports whether s matches pattern, where '*' matches any
// run of characters (like rsync's *, which never matches '/'; the
// component split guarantees no '/' in s).
func globMatch(s, pattern string) bool {
	for len(pattern) > 0 {
		star := strings.IndexByte(pattern, '*')
		if star < 0 {
			return s == pattern
		}
		if !strings.HasPrefix(s, pattern[:star]) {
			return false
		}
		s = s[star:]
		pattern = pattern[star+1:]
		end := strings.IndexByte(pattern, '*')
		if end < 0 {
			return strings.HasSuffix(s, pattern)
		}
		mid := pattern[:end]
		idx := strings.Index(s, mid)
		if idx < 0 {
			return false
		}
		s = s[idx+len(mid):]
		pattern = pattern[end:]
	}
	return true
}
```

Delete the `rsyncExcludes` var from `internal/deploy/rsync.go:46-58` (it now lives in `syncfilter.go`; `rsync.go` is deleted entirely in Task 5, so removing the var here prevents a duplicate declaration between now and then).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/deploy/ -run TestPathExcluded -v`
Expected: PASS.

- [ ] **Step 5: Run the full deploy suite and commit**

```bash
go test ./internal/deploy/...
git add internal/deploy/syncfilter.go internal/deploy/syncfilter_test.go internal/deploy/rsync.go
git commit -m "refactor(deploy): port rsync excludes to a pure path matcher"
```

---

### Task 5: SFTP sync (`Client.SyncDir`), pipeline swap, delete rsync

**Files:**
- Create: `internal/deploy/sftp.go`
- Create: `internal/deploy/syncdir_test.go`
- Modify: `internal/deploy/deploy.go:76-82,177-179` (sync phase + remove `sshAddr`)
- Delete: `internal/deploy/rsync.go`, `internal/deploy/rsync_test.go`
- Modify: `go.mod`/`go.sum`

**Interfaces:**
- Consumes: `pathExcluded`, `rsyncExcludes` (Task 4); `*Client.conn`; `startSSHServer` + sftp subsystem (Task 1).
- Produces: `func (c *Client) SyncDir(ctx context.Context, local, remote string, excludes []string) error` — recursive copy, excluded paths skipped (dirs pruned), remote dirs auto-created, modes and mtimes preserved. Pipeline phase 3 calls `client.SyncDir(ctx, ".", p.DeployEnv.Path, rsyncExcludes)`; `sshAddr` is removed.

- [ ] **Step 1: Add the sftp dependency**

```bash
go get github.com/pkg/sftp@latest && go mod tidy
```

- [ ] **Step 2: Write the failing SyncDir test**

Create `internal/deploy/syncdir_test.go`:

```go
package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncDirUploadsTree(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()
	local := t.TempDir()

	files := map[string]struct {
		mode os.FileMode
		body string
	}{
		"composer.json":              {0o644, `{"name":"x"}`},
		"app/Http/routes.php":        {0o755, "<?php\n"},
		".env.production":            {0o600, "APP_KEY=secret\n"},
		"sub/.env.staging":           {0o600, "APP_KEY=staging\n"},
		".git/HEAD":                  {0o644, "ref: refs/heads/main\n"},
		"storage/logs/laravel.log":   {0o644, "log line\n"},
		"node_modules/pkg/index.js":  {0o644, "module.exports = 1\n"},
		"note.swp":                   {0o644, "swap\n"},
	}
	for rel, f := range files {
		p := filepath.Join(local, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
	}

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SyncDir(context.Background(), local, remote, rsyncExcludes); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}

	for _, rel := range []string{"composer.json", "app/Http/routes.php", ".env.production"} {
		if _, err := os.Stat(filepath.Join(remote, rel)); err != nil {
			t.Errorf("expected %s on remote: %v", rel, err)
		}
	}
	for _, rel := range []string{
		"sub/.env.staging", ".git/HEAD", "storage/logs/laravel.log",
		"node_modules/pkg/index.js", "note.swp",
	} {
		if _, err := os.Stat(filepath.Join(remote, rel)); err == nil {
			t.Errorf("unexpected file %s on remote", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(remote, ".git")); err == nil {
		t.Error("unexpected .git dir on remote")
	}
}

func TestSyncDirPreservesMode(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()
	local := t.TempDir()

	rel := "script.sh"
	if err := os.WriteFile(filepath.Join(local, rel), []byte("#!/bin/sh\n"), 0o754); err != nil {
		t.Fatal(err)
	}

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SyncDir(context.Background(), local, remote, rsyncExcludes); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
	info, err := os.Stat(filepath.Join(remote, rel))
	if err != nil {
		t.Fatalf("stat remote script: %v", err)
	}
	if info.Mode().Perm() != 0o754 {
		t.Errorf("mode = %v, want 0754", info.Mode().Perm())
	}
}

func TestSyncDirEmptyLocalKeepsNothing(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SyncDir(context.Background(), t.TempDir(), remote, rsyncExcludes); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
	entries, err := os.ReadDir(remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("remote has %d entries, want 0", len(entries))
	}
}
```

Note: `writeTestKeyPath(t)` is a one-line helper on `writeTestKey`: `path, _ := writeTestKey(t); return path`. Add it to `testssh_test.go`:

```go
// writeTestKeyPath is writeTestKey's convenience form for callers
// that only need the private key path.
func writeTestKeyPath(t *testing.T) string {
	t.Helper()
	path, _ := writeTestKey(t)
	return path
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/deploy/ -run TestSyncDir -v`
Expected: FAIL — `c.SyncDir` is not defined.

- [ ] **Step 4: Implement `SyncDir`**

Create `internal/deploy/sftp.go`:

```go
package deploy

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pkg/sftp"
)

// SyncDir copies the local directory tree to remote over SFTP,
// reusing the already-open SSH connection. Paths matched by excludes
// (see pathExcluded) are skipped; excluded directories are pruned.
// Remote parent directories are created on demand and file modes and
// modification times are preserved. ctx is honored between files; a
// single large file copy is not interruptible mid-stream.
func (c *Client) SyncDir(ctx context.Context, local, remote string, excludes []string) error {
	sc, err := sftp.NewClient(c.conn)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()
	return filepath.WalkDir(local, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(local, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if pathExcluded(rel, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return putSFTPFile(sc, path, filepath.ToSlash(filepath.Join(remote, rel)), info)
	})
}

// putSFTPFile writes one local file to the remote path, creating
// parent directories, preserving mode and mtime.
func putSFTPFile(sc *sftp.Client, localPath, remotePath string, info fs.FileInfo) error {
	if err := sc.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(remotePath), err)
	}
	rf, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open remote %s: %w", remotePath, err)
	}
	defer rf.Close()
	lf, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local %s: %w", localPath, err)
	}
	defer lf.Close()
	if _, err := io.Copy(rf, lf); err != nil {
		return fmt.Errorf("copy %s: %w", remotePath, err)
	}
	if err := rf.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", remotePath, err)
	}
	if err := rf.Chtimes(info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("chtimes %s: %w", remotePath, err)
	}
	return nil
}
```

- [ ] **Step 5: Update the pipeline to use `SyncDir` and remove `sshAddr`**

Modify `internal/deploy/deploy.go:76-82`:

```go
	// Phase 3: sync.
	p.Logger.PhaseStart("sync")
	if err := Sync(ctx, defaultRunner, ".", p.sshAddr()); err != nil {
		p.Logger.PhaseEnd("sync", err)
		return PreflightError(err)
	}
	p.Logger.PhaseEnd("sync", nil)
```

to:

```go
	// Phase 3: sync.
	p.Logger.PhaseStart("sync")
	if err := client.SyncDir(ctx, ".", p.DeployEnv.Path, rsyncExcludes); err != nil {
		p.Logger.PhaseEnd("sync", err)
		return PreflightError(err)
	}
	p.Logger.PhaseEnd("sync", nil)
```

Delete `sshAddr` (internal/deploy/deploy.go:177-179):

```go
func (p *Pipeline) sshAddr() string {
	return fmt.Sprintf("%s@%s:%s", p.SSH.User, p.SSH.Host, p.DeployEnv.Path)
}
```

- [ ] **Step 6: Delete the rsync subprocess code and its tests**

```bash
rm internal/deploy/rsync.go internal/deploy/rsync_test.go
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/deploy/... && go vet ./...`
Expected: PASS. (`TestSyncExcludes`/`TestSyncLocalPath`/`TestOsRunner*` are gone; replaced by `TestSyncDir*` + `TestPathExcluded`.)

- [ ] **Step 8: Commit**

```bash
git add internal/deploy/sftp.go internal/deploy/syncdir_test.go internal/deploy/testssh_test.go internal/deploy/deploy.go go.mod go.sum
git rm internal/deploy/rsync.go internal/deploy/rsync_test.go
git commit -m "feat(deploy): sync over SFTP instead of rsync subprocess"
```

---

### Task 6: CLI wiring — prompt helper and shared SSHConfig builder

**Files:**
- Modify: `internal/cli/helpers.go` (rename `readSudoPassword` → `readPassword`, add `newSSHConfig`)
- Modify: `internal/cli/bootstrap.go:26,62` (seam + call site)
- Modify: `internal/cli/deploy.go:39-41` (use `newSSHConfig`)
- Modify: `internal/cli/rollback.go:33` (use `newSSHConfig`)
- Modify: `internal/cli/status.go:69` (switch the literal to `newSSHConfig(dc)`)
- Modify: `internal/cli/status_test.go` (assert prompt wiring)

**Interfaces:**
- Consumes: `deploy.SSHConfig.PasswordPrompt`, `config.DeployConfig`, `sshKeyPath` (cli/deploy.go).
- Produces: `readPassword(prompt string) (string, error)`; `newSSHConfig(dc config.DeployConfig) deploy.SSHConfig` with `PasswordPrompt` set to a stderr prompt `SSH password for <user>@<host>: `. All four remote commands build their SSHConfig through `newSSHConfig`.

- [ ] **Step 1: Write the failing wiring test**

Append to `internal/cli/status_test.go`:

```go
func TestStatusRemoteConfigCarriesPasswordPrompt(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	var got deploy.SSHConfig
	origDial := statusDial
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		got = cfg
		return &fakeStatusRunner{}, nil
	}
	defer func() { statusDial = origDial }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Host != "h" || got.User != "u" {
		t.Errorf("SSHConfig host/user = %q/%q, want h/u", got.Host, got.User)
	}
	if got.PasswordPrompt == nil {
		t.Error("SSHConfig.PasswordPrompt is nil, want wired prompt")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestStatusRemoteConfigCarriesPasswordPrompt -v`
Expected: FAIL — `PasswordPrompt` is nil (status.go still builds the literal without the prompt).

- [ ] **Step 3: Rename `readSudoPassword` and add `newSSHConfig`**

Modify `internal/cli/helpers.go:17-28`:

```go
// readPassword prompts on stderr (so --json stdout stays clean)
// with echo disabled and returns the entered password. The prompt
// goes to stderr because it is not part of the command's output.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}

// newSSHConfig builds the SSHConfig for a deploy env: target host
// and user from [deploy.<env>], key path from sshKeyPath, and an
// interactive password prompt that Dial falls back to when the
// server rejects the key. The password is never stored.
func newSSHConfig(dc config.DeployConfig) deploy.SSHConfig {
	return deploy.SSHConfig{
		Host:    dc.Host,
		User:    dc.User,
		KeyPath: sshKeyPath(),
		PasswordPrompt: func() (string, error) {
			return readPassword(fmt.Sprintf("SSH password for %s@%s: ", dc.User, dc.Host))
		},
	}
}
```

Add imports `"github.com/Bonnary/pier/internal/config"` and `"github.com/Bonnary/pier/internal/deploy"` to `helpers.go`.

Update `internal/cli/bootstrap.go`:
- Line 26: `readSudoPwd    = readSudoPassword` → `readSudoPwd    = readPassword`
- Line 62: `sshCfg := deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()}` → `sshCfg := newSSHConfig(dc)`

Update `internal/cli/deploy.go:39-41`:

```go
		SSH: deploy.SSHConfig{
			Host: dc.Host, User: dc.User, KeyPath: sshKeyPath(),
		},
```

to:

```go
		SSH: newSSHConfig(dc),
```

Update `internal/cli/rollback.go:33`:

```go
	ssh := deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()}
```

to:

```go
	ssh := newSSHConfig(dc)
```

Update `internal/cli/status.go:69`:

```go
	client, err := statusDial(cmd.Context(), deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()})
```

to:

```go
	client, err := statusDial(cmd.Context(), newSSHConfig(dc))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ ./internal/deploy/`
Expected: PASS. (`bootstrap_test.go` overrides the `readSudoPwd` seam with fakes — the seam var survives the rename, so those tests are unchanged; its `probeEnvFn`/`bootstrapEnvFn` seams only assert Host/User/KeyPath, so the new `PasswordPrompt` field doesn't break assertions. Verify with `grep -c "PasswordPrompt" internal/cli/*_test.go` — expect exactly the new test.)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/helpers.go internal/cli/bootstrap.go internal/cli/deploy.go internal/cli/rollback.go internal/cli/status.go internal/cli/status_test.go
git commit -m "feat(cli): wire interactive SSH password prompt into all remote commands"
```

---

### Task 7: Docs and final verification

**Files:**
- Modify: `README.md` (Prerequisites, Features, Troubleshooting)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: nothing; documents the shipped behavior.

- [ ] **Step 1: Update README Prerequisites**

Modify the "OpenSSH" bullet (README.md:103-105) from:

```markdown
- **OpenSSH** client (`ssh`, `rsync`-over-ssh) for `pier deploy` and
  `pier rollback`. `pier` uses the host's `~/.ssh/id_ed25519` by
  default; override with `--ssh-key` or `$DEPLOY_SSH_KEY`.
```

to:

```markdown
- **SSH access to the deploy host.** `pier` uses the host's
  `~/.ssh/id_ed25519` by default; override with `$DEPLOY_SSH_KEY`.
  If the server rejects the key, `pier` falls back to a one-time
  interactive password prompt (echo disabled; never stored). File
  sync runs over SFTP on pier's own connection — no local `ssh` or
  `rsync` binaries are required.
```

- [ ] **Step 2: Update README Features**

Modify the `pier deploy` bullet (README.md:66-68) to:

```markdown
- **`pier deploy <env>`** — Build, sync, up, health-check, and
  commit a production image tag over SSH. A Bubble Tea TUI shows
  live phase progress. Key auth is tried first; password-only
  servers get an interactive prompt.
```

- [ ] **Step 3: Update README Troubleshooting**

Modify the `ssh: handshake failed` entry (README.md:383-385) to:

```markdown
- **"ssh: handshake failed"** — run `pier status`, check
  `~/.ssh/id_ed25519` perms (`chmod 600`), and confirm the host is
  reachable. Password-only servers are handled automatically: pier
  prompts for the password after key auth is rejected — no key
  setup needed on the server.
```

- [ ] **Step 4: Update CHANGELOG**

Add at the top of `## v0.0.2-beta` → `### Added` (CHANGELOG.md:5-9):

```markdown
- SSH password auth fallback: when a deploy host rejects the SSH key
  (or no key exists), `pier deploy`, `pier rollback`, `pier status <env>`,
  and `pier bootstrap` prompt once for the password and connect with
  `password` / `keyboard-interactive` auth. The password is never
  stored. Cancelling the prompt exits 130.
- Deploy file sync now runs over SFTP on pier's own SSH connection
  instead of the `rsync` subprocess, so no local `ssh`/`rsync` binary
  is required. `.env.production` is now actually synced (the old
  rsync exclude ordering dropped it).
```

- [ ] **Step 5: Full verification**

```bash
go build ./... && go vet ./... && go test -race ./... && golangci-lint run
```

Expected: all pass. If `golangci-lint` is not installed, report that explicitly and rely on `go vet` + `gofmt -l .` (expect empty output).

- [ ] **Step 6: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: SSH password fallback, SFTP sync, and changelog entry"
```

---

## Self-Review Notes

- **Spec coverage:** §1 auth fallback → Tasks 1-2; §2 SFTP sync → Tasks 4-5; §3 CLI wiring/prompts/errors → Tasks 3 + 6; §4 testing/docs → every task + Task 7. The out-of-scope list (no stored passwords, no env-var injection, no host-key verification) is untouched.
- **Spec deviation (deliberate):** the spec said "read the key exactly as today" — but a password-only user with no local key must still reach the prompt, so a *missing* key file now falls through to the password path (Task 1, `TestDialMissingKeyFileUsesPassword`). Key files that exist but fail to parse still fail fast.
- **Behavior fix surfaced during design:** rsync's first-match-wins exclude ordering actually drops `.env.production` today (verified against real rsync). The SFTP matcher checks include rules first, so `.env.production` now syncs as the original comment intended. Called out in the CHANGELOG entry.
- **Abort plumbing:** `Pipeline.Run` (deploy.go) and `runRemoteStatus` (status.go) are the only two places that re-wrap Dial errors — both must pass `ErrAborted` through or Ctrl+C would exit 2 instead of 130 (Task 3). `rollback` and `bootstrap` already return Dial errors unwrapped.
- **Type consistency:** `pathExcluded(rel string, excludes []string) bool` (Task 4) is consumed by `SyncDir` (Task 5); `SyncDir(ctx, local, remote, excludes []string) error` matches the pipeline call in Task 5 and the spec signature. `newSSHConfig(dc config.DeployConfig) deploy.SSHConfig` is used in deploy.go, rollback.go, status.go, bootstrap.go (Task 6). `writeTestKey`/`writeTestKeyPath`/`startSSHServer`/`passwordOnlyServer`/`keyOnlyServer(pub)`/`keyboardOnlyServer` are defined in Task 1 and used across Tasks 1, 2, and 5.

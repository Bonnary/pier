package deploy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

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

func (c *SSHConfig) port() int {
	if c.Port == 0 {
		return 22
	}
	return c.Port
}

// ErrPreflight is the sentinel for every error returned by Dial
// (empty host, missing key, bad permissions, handshake failure).
// Callers use errors.Is to surface a KindConfig error with a clear
// message.
var ErrPreflight = errors.New("deploy: preflight failed")

// Client is a thin wrapper around an *ssh.Client. It implements the
// internal runner interface (Run / RunStream) used by every other
// stage of the deploy pipeline.
type Client struct {
	Config SSHConfig
	conn   *ssh.Client
}

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
	if errors.As(err, &sae) {
		return true
	}
	// *ssh.ServerAuthError is only produced by the server side; the
	// client's rejection error is a plain fmt.Errorf ("ssh: unable to
	// authenticate, attempted methods %v, no supported methods
	// remain"). Match on its stable message text.
	return strings.Contains(err.Error(), "unable to authenticate")
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

// Run executes cmd on the remote host and returns the captured
// stdout, stderr, and the session error. Used by Tag, Up, and Rollback
// where the caller needs to inspect the output (or where streaming
// is unnecessary).
func (c *Client) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if err := sess.Run(cmd); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

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
	var stderrErr error
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
		stderrErr = sc.Err()
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if onStdout != nil {
			onStdout(sc.Text())
		}
	}
	<-done
	if err := sc.Err(); err != nil {
		return stderrBuf.Bytes(), err
	}
	if stderrErr != nil {
		return stderrBuf.Bytes(), stderrErr
	}
	return stderrBuf.Bytes(), sess.Wait()
}

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
	if t.max <= 0 {
		return
	}
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
// each line of stdout or stderr as it arrives. onLine may be invoked
// concurrently from two goroutines (the stdout and stderr readers),
// so callbacks must be safe for concurrent use. Used by Build, which
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

// Close shuts down the underlying SSH connection. Safe to call when
// the connection was never established.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

type runner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	RunStream(ctx context.Context, cmd string, onLine func(string)) error
}

// stdinRunner is the subset of *Client that bootstrap needs: plain
// Run, RunStdin for piping the sudo password, and RunStreamStdin for
// streaming output while piping it.
type stdinRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	RunStdin(ctx context.Context, cmd string, stdin string) ([]byte, []byte, error)
	RunStreamStdin(ctx context.Context, cmd, stdin string, onStdout, onStderr func(string)) ([]byte, error)
}

var _ stdinRunner = (*Client)(nil)

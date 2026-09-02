package deploy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHConfig is everything Dial needs to open a connection: target
// host, login user, path to the private key on the local filesystem,
// and the TCP port (0 means 22). When the server rejects the key
// (or the key file does not exist), Dial falls back to a password:
// Password wins, then PasswordPrompt (which may be nil to forbid
// prompting). KnownHostsPath names the OpenSSH known_hosts file to
// verify the remote host against; when it is set, the host key is
// verified trust-on-first-use (TOFU) — an unknown host is accepted and
// recorded, a known host whose key changed is rejected (MITM defense,
// F3). When empty, the host key is not verified.
type SSHConfig struct {
	Host           string
	User           string
	KeyPath        string
	Port           int
	Password       string
	PasswordPrompt func() (string, error)
	KnownHostsPath string
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
// When cfg.KnownHostsPath is set the remote host key is verified
// trust-on-first-use (F3); the CLI always sets it to
// ~/.ssh/known_hosts. Key auth is tried first; if the key file is
// missing or the server rejects every offered key (an auth-class
// failure), Dial falls back to the password from cfg.Password or
// cfg.PasswordPrompt and retries once. A password fallback is
// attempted only when a password source exists; otherwise the
// original handshake error is returned.
func Dial(ctx context.Context, cfg SSHConfig) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("%w: empty host", ErrPreflight)
	}
	if cfg.KeyPath == "" {
		return nil, fmt.Errorf("%w: empty key path", ErrPreflight)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.port())
	auth := []ssh.AuthMethod(nil)
	key, err := os.ReadFile(cfg.KeyPath)
	if err == nil {
		signer, perr := ssh.ParsePrivateKey(key)
		if perr != nil {
			return nil, fmt.Errorf("%w: parse key: %v", ErrPreflight, perr)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: read key: %v", ErrPreflight, err)
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
		// An interactive cancel surfaces as an abort so the CLI
		// exits 130 instead of a preflight error.
		return nil, AbortedError()
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
	cb, err := hostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	tcpConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	ncc, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: cb,
	})
	if err != nil {
		tcpConn.Close()
		return nil, err
	}
	return ssh.NewClient(ncc, chans, reqs), nil
}

// hostKeyCallback returns the HostKeyCallback for a connection. When
// cfg.KnownHostsPath is set it verifies the remote host trust-on-first-
// use (F3); otherwise it accepts any key (the historical, insecure
// behavior used by internal/test callers that do not name a known_hosts
// file — the CLI always sets KnownHostsPath).
func hostKeyCallback(cfg SSHConfig) (ssh.HostKeyCallback, error) {
	if cfg.KnownHostsPath == "" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	return tofuHostKeyCallback(cfg.KnownHostsPath)
}

// tofuHostKeyCallback verifies the remote host against the OpenSSH
// known_hosts file at path, trust-on-first-use: an unknown host is
// accepted and its key recorded, a known host whose key changed is
// rejected (a possible MITM). The file (and its parent directory) is
// created on first use if missing. The known_hosts file is re-read on
// every check so a key recorded by a prior connection in the same
// process is honored.
func tofuHostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	if err := ensureKnownHostsFile(path); err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		check, err := knownhosts.New(path)
		if err != nil {
			return fmt.Errorf("deploy: load known_hosts %s: %w", path, err)
		}
		err = check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) || len(ke.Want) > 0 {
			// Known host with a different key, or a revoked key: reject.
			return err
		}
		// Unknown host: TOFU — accept and record the key.
		if err := appendKnownHost(path, hostname, key); err != nil {
			return fmt.Errorf("deploy: record host key for %s: %w", hostname, err)
		}
		return nil
	}, nil
}

// ensureKnownHostsFile creates path (and its parent) with 0600 when it
// does not yet exist, so knownhosts.New can read it on first use.
func ensureKnownHostsFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

// appendKnownHost appends the host key line for hostname to the
// known_hosts file at path, creating it with 0600 if needed.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{hostname}, key)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
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

// StreamIn executes cmd on the remote host with stdin fed from in and
// invokes onLine for each stderr line as it arrives. The stdin path is
// binary-safe (no line scanning on the input side): it pipes `docker
// save` output into a remote `docker load`. stderr is line-streamed
// because docker load writes its progress and errors as lines. On a
// non-zero exit the returned error carries the last
// runStreamTailSize stderr lines.
func (c *Client) StreamIn(ctx context.Context, cmd string, in io.Reader, onLine func(string)) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	sess.Stdin = in
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
			if onLine != nil {
				onLine(line)
			}
		}
		stderrErr = sc.Err()
	}()
	err = sess.Wait()
	<-done
	if err != nil {
		return fmt.Errorf("remote command failed: %w (last output: %s)", err, tail.String())
	}
	if stderrErr != nil {
		return stderrErr
	}
	return nil
}

// StreamOut executes cmd on the remote host piping its stdout into
// out and invoking onLine for each stderr line as it arrives. The
// stdout path is binary-safe (io.Copy, no line scanning): it streams
// `docker save` output from a build server into a sink. On a non-zero
// exit the returned error carries the last runStreamTailSize stderr
// lines.
func (c *Client) StreamOut(ctx context.Context, cmd string, out io.Writer, onLine func(string)) error {
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
			if onLine != nil {
				onLine(line)
			}
		}
		stderrErr = sc.Err()
	}()
	if _, err := io.Copy(out, stdout); err != nil {
		return err
	}
	<-done
	if err := sess.Wait(); err != nil {
		return fmt.Errorf("remote command failed: %w (last output: %s)", err, tail.String())
	}
	if stderrErr != nil {
		return stderrErr
	}
	return nil
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

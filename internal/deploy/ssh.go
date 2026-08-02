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
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHConfig is everything Dial needs to open a connection: target
// host, login user, path to the private key on the local filesystem,
// and the TCP port (0 means 22).
type SSHConfig struct {
	Host    string
	User    string
	KeyPath string
	Port    int
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
func Dial(ctx context.Context, cfg SSHConfig) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("%w: empty host", ErrPreflight)
	}
	if cfg.KeyPath == "" {
		return nil, fmt.Errorf("%w: empty key path", ErrPreflight)
	}
	key, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: read key: %v", ErrPreflight, err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("%w: parse key: %v", ErrPreflight, err)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.port())
	d := net.Dialer{Timeout: 10 * time.Second}
	tcpConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %v", ErrPreflight, addr, err)
	}
	ncc, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		tcpConn.Close()
		return nil, fmt.Errorf("%w: handshake %s: %v", ErrPreflight, addr, err)
	}
	return &Client{Config: cfg, conn: ssh.NewClient(ncc, chans, reqs)}, nil
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

// RunStream executes cmd on the remote host and invokes onLine for
// each line of stdout as it arrives. Used by Build, which needs to
// surface `docker compose build` progress in the deploy TUI in real
// time.
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
	if err := sess.Start(cmd); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		onLine(scanner.Text())
	}
	return sess.Wait()
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
// Run plus RunStdin for piping the sudo password.
type stdinRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	RunStdin(ctx context.Context, cmd string, stdin string) ([]byte, []byte, error)
	RunStreamStdin(ctx context.Context, cmd, stdin string, onStdout, onStderr func(string)) ([]byte, error)
}

var _ stdinRunner = (*Client)(nil)

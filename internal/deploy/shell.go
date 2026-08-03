package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

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
// nil. Cancelling ctx aborts the in-flight session.
func RemoteExec(ctx context.Context, cfg SSHConfig, dir string, args []string) error {
	client, err := Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.remoteExec(ctx, cfg.Host, dir, args)
}

// remoteExec runs a one-off command in the app service of the prod
// compose file on the remote host, streaming stdout/stderr to pier's
// own streams. Cancelling ctx aborts the in-flight session.
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
	cancelled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Close()
		case <-cancelled:
		}
	}()
	defer close(cancelled)
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
// Cancelling ctx aborts the in-flight session.
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
// local stdin. Cancelling ctx aborts the in-flight session.
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
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Close()
		case <-done:
		}
	}()
	err = sess.Wait()
	close(done)
	if err != nil {
		return RemoteCommandError(c.Config.Host, err)
	}
	return nil
}

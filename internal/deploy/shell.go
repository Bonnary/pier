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

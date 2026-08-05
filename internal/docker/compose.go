// Package docker is a thin wrapper around the `docker compose` CLI
// (and plain `docker` for Exec). Every pier command that touches a
// container goes through Compose so that stdout/stderr handling,
// stderr capture for error wrapping, and stdin forwarding stay
// consistent.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner is the abstraction Compose uses to spawn `docker` (or
// anything else). The real implementation is ExecRunner; tests
// substitute a fake that captures the args.
type Runner interface {
	Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error
}

// ExecRunner is the production Runner: it spawns the command with
// exec.CommandContext and forwards the supplied writers. A nil
// stdin is treated as "inherit os.Stdin" so `pier exec` / `pier
// shell` remain interactive.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if stdin != nil {
		cmd.Stdin = stdin
	} else {
		cmd.Stdin = os.Stdin
	}
	return cmd.Run()
}

// Compose is a configured `docker compose` invocation bound to a
// specific compose file. Reuse one value across many calls; it is
// safe for concurrent use only when its Runner is.
type Compose struct {
	Workdir string
	File    string
	Runner  Runner
	// Stdout and Stderr are the writers used by the Runner when it streams
	// command output. When nil, they default to os.Stdout / os.Stderr.
	Stdout io.Writer
	Stderr io.Writer
}

func (c *Compose) file() string {
	if c.Workdir != "" && !filepath.IsAbs(c.File) {
		return filepath.Join(c.Workdir, c.File)
	}
	return c.File
}

func (c *Compose) base() []string {
	args := []string{"compose", "-f", c.file()}
	if c.Workdir != "" {
		args = append(args, "--project-directory", c.Workdir)
	}
	return args
}

// runStreaming executes the docker command, streaming output to the configured
// stdout/stderr writers. The returned error (if any) is wrapped with the
// captured stderr text so the caller can show a useful message to the user
// even when the stream is going to a TTY.
func (c *Compose) runStreaming(ctx context.Context, args ...string) error {
	stdout := c.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := c.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	var stderrBuf bytes.Buffer
	stderrW := io.MultiWriter(stderr, &stderrBuf)
	err := c.Runner.Run(ctx, os.Stdin, stdout, stderrW, "docker", append(c.base(), args...)...)
	if err != nil && stderrBuf.Len() > 0 {
		return fmt.Errorf("%s: %w", strings.TrimSpace(stderrBuf.String()), err)
	}
	return err
}

// runRaw executes the docker command with the given full arg list (caller
// has already prepended c.base() if needed). Output is streamed and stderr
// is captured for error wrapping.
func (c *Compose) runRaw(ctx context.Context, args ...string) error {
	stdout := c.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := c.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	var stderrBuf bytes.Buffer
	stderrW := io.MultiWriter(stderr, &stderrBuf)
	err := c.Runner.Run(ctx, os.Stdin, stdout, stderrW, "docker", args...)
	if err != nil && stderrBuf.Len() > 0 {
		return fmt.Errorf("%s: %w", strings.TrimSpace(stderrBuf.String()), err)
	}
	return err
}

// runCaptured executes the docker command, returning the captured stdout.
// stderr is streamed (so the user sees progress / errors live) but is not
// returned.
func (c *Compose) runCaptured(ctx context.Context, args ...string) ([]byte, error) {
	var stdoutBuf bytes.Buffer
	stdout := c.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := c.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	var stderrBuf bytes.Buffer
	stderrW := io.MultiWriter(stderr, &stderrBuf)
	stdoutW := io.MultiWriter(stdout, &stdoutBuf)
	err := c.Runner.Run(ctx, os.Stdin, stdoutW, stderrW, "docker", append(c.base(), args...)...)
	if err != nil && stderrBuf.Len() > 0 {
		return stdoutBuf.Bytes(), fmt.Errorf("%s: %w", strings.TrimSpace(stderrBuf.String()), err)
	}
	return stdoutBuf.Bytes(), err
}

// Up runs `docker compose -f <file> up -d [services...]` and streams
// the output. When services is empty, every service in the file is
// brought up. Used by `pier dev`, `pier service`, and the deploy
// pipeline.
func (c *Compose) Up(ctx context.Context, services ...string) error {
	args := append([]string{"up", "-d"}, services...)
	return c.runStreaming(ctx, args...)
}

// Down runs `docker compose -f <file> down`. Used by `pier stop`.
func (c *Compose) Down(ctx context.Context) error {
	return c.runStreaming(ctx, "down")
}

// Build runs `docker compose -f <file> build [services...]` and
// streams the output. Used by `pier dev` (unless --no-build is set).
func (c *Compose) Build(ctx context.Context, services ...string) error {
	args := append([]string{"build"}, services...)
	return c.runStreaming(ctx, args...)
}

// PS runs `docker compose -f <file> ps` and returns the captured
// stdout. Used by `pier dev` (after Up) and `pier status`.
func (c *Compose) PS(ctx context.Context) ([]byte, error) {
	return c.runCaptured(ctx, "ps")
}

// Config runs `docker compose -f <file> config` and returns the
// merged, normalized compose YAML. Currently unused by the CLI but
// exposed for tooling.
func (c *Compose) Config(ctx context.Context) ([]byte, error) {
	return c.runCaptured(ctx, "config")
}

// Pull runs `docker compose -f <file> pull` and streams the output.
// Used by `pier init` (in the warm-up step) and as a manual command.
func (c *Compose) Pull(ctx context.Context) error {
	return c.runStreaming(ctx, "pull")
}

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

type Runner interface {
	Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error
}

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

func (c *Compose) Up(ctx context.Context, services ...string) error {
	args := append([]string{"up", "-d"}, services...)
	return c.runStreaming(ctx, args...)
}

func (c *Compose) Down(ctx context.Context) error {
	return c.runStreaming(ctx, "down")
}

func (c *Compose) Build(ctx context.Context, services ...string) error {
	args := append([]string{"build"}, services...)
	return c.runStreaming(ctx, args...)
}

func (c *Compose) PS(ctx context.Context) ([]byte, error) {
	return c.runCaptured(ctx, "ps")
}

func (c *Compose) Config(ctx context.Context) ([]byte, error) {
	return c.runCaptured(ctx, "config")
}

func (c *Compose) Pull(ctx context.Context) error {
	return c.runStreaming(ctx, "pull")
}

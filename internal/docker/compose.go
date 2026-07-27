package docker

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type Compose struct {
	Workdir string
	File    string
	Runner  Runner
}

func (c *Compose) base() []string {
	file := c.File
	if c.Workdir != "" && !filepath.IsAbs(file) {
		file = filepath.Join(c.Workdir, file)
	}
	args := []string{"compose", "-f", file}
	if c.Workdir != "" {
		args = append(args, "--project-directory", c.Workdir)
	}
	return args
}

func (c *Compose) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	return c.Runner.Run(ctx, "docker", args...)
}

func (c *Compose) Up(ctx context.Context, services ...string) error {
	args := append(c.base(), "up", "-d")
	args = append(args, services...)
	_, _, err := c.run(ctx, args...)
	return err
}

func (c *Compose) Down(ctx context.Context) error {
	_, _, err := c.run(ctx, append(c.base(), "down")...)
	return err
}

func (c *Compose) Build(ctx context.Context, services ...string) error {
	args := append(c.base(), "build")
	args = append(args, services...)
	_, _, err := c.run(ctx, args...)
	return err
}

func (c *Compose) PS(ctx context.Context) ([]byte, error) {
	out, _, err := c.run(ctx, append(c.base(), "ps")...)
	return out, err
}

func (c *Compose) Config(ctx context.Context) ([]byte, error) {
	out, _, err := c.run(ctx, append(c.base(), "config")...)
	return out, err
}

func (c *Compose) Pull(ctx context.Context) error {
	_, _, err := c.run(ctx, append(c.base(), "pull")...)
	return err
}

package docker

import (
	"context"
	"fmt"
	"os"
)

type ExecOpts struct {
	Service string
	User    string
	TTY     bool
	Env     []string
}

func (c *Compose) Exec(ctx context.Context, opts ExecOpts, cmd ...string) error {
	if opts.Service == "" {
		return fmt.Errorf("docker: ExecOpts.Service is required")
	}
	args := append(c.base(), "exec")
	if opts.TTY {
		args = append(args, "-i")
	} else {
		args = append(args, "-T")
	}
	if opts.User != "" {
		args = append(args, "-u", opts.User)
	}
	for _, e := range opts.Env {
		args = append(args, "-e", e)
	}
	args = append(args, opts.Service)
	args = append(args, cmd...)
	_, _, err := c.run(ctx, args...)
	return err
}

func DetectTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

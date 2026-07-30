package docker

import (
	"context"
	"fmt"
	"os"
)

// ExecOpts is the parameter set for Compose.Exec. Service is
// required; User, TTY, and Env are optional. When TTY is true the
// command runs with -i (interactive) so stdin forwarding from
// `pier shell` works; otherwise -T is passed and stdin is a pipe.
type ExecOpts struct {
	Service string
	User    string
	TTY     bool
	Env     []string
}

// Exec runs `docker compose -f <file> exec [-i|-T] [-u user] [-e env...]
// <service> <cmd...>` on the host. Used by `pier shell` (TTY) and
// `pier exec <cmd...>` (TTY follows os.Stdout).
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
	return c.runRaw(ctx, args...)
}

// DetectTTY reports whether os.Stdout is a character device (i.e. an
// actual terminal, not a pipe or redirect). `pier exec` uses this
// to decide between -i and -T.
func DetectTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

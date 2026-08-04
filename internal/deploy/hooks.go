package deploy

import (
	"context"
	"errors"

	"golang.org/x/crypto/ssh"

	"github.com/Bonnary/pier/internal/config"
)

// runHooks runs each command in cmds inside the app container on the
// remote deploy host, streaming output through the logger. Commands
// run in order; a failing command logs a warning and the remaining
// commands still run. runHooks never fails the deploy — a pre/post
// deploy hook cannot abort a release. An empty list skips the phase
// entirely.
func (p *Pipeline) runHooks(ctx context.Context, c *Client, name string, cmds []string) {
	if len(cmds) == 0 {
		return
	}
	p.Logger.PhaseStart(name)
	for _, line := range cmds {
		args, err := config.SplitCommand(line)
		if err != nil {
			p.Logger.Log(name, "warning: skip %q: %v", line, err)
			continue
		}
		cmd := remoteExecCommand(p.DeployEnv.Path, args)
		err = c.RunStream(ctx, cmd, func(l string) {
			p.Logger.Log(name, "%s", l)
		})
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				p.Logger.Log(name, "warning: %q exited with status %d", line, exitErr.ExitStatus())
			} else {
				p.Logger.Log(name, "warning: %q failed: %v", line, err)
			}
			continue
		}
		p.Logger.Log(name, "ok: %q", line)
	}
	p.Logger.PhaseEnd(name, nil)
}

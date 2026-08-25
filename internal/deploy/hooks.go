package deploy

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"

	"github.com/Bonnary/pier/internal/config"
)

// runHooks runs each command in cmds inside the app container on the
// remote deploy host, streaming output through the logger. Commands
// run in order; the first failing command aborts the phase and the
// returned error fails the deploy — no further commands run. A
// cancelled context stops the loop immediately. An empty list skips
// the phase entirely.
func (p *Pipeline) runHooks(ctx context.Context, c *Client, name string, cmds []string) error {
	if len(cmds) == 0 {
		return nil
	}
	p.Logger.PhaseStart(name)
	for _, line := range cmds {
		if ctx.Err() != nil {
			p.Logger.PhaseEnd(name, ctx.Err())
			return ctx.Err()
		}
		args, err := config.SplitCommand(line)
		if err != nil {
			err = fmt.Errorf("invalid command %q: %v", line, err)
			p.Logger.Log(name, "error: %v", err)
			p.Logger.PhaseEnd(name, err)
			return err
		}
		cmd := remoteExecCommand(p.DeployEnv.Path, args)
		err = c.RunStream(ctx, cmd, func(l string) {
			p.Logger.Log(name, "%s", l)
		})
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				err = fmt.Errorf("%q exited with status %d", line, exitErr.ExitStatus())
			} else {
				err = fmt.Errorf("%q failed: %v", line, err)
			}
			p.Logger.Log(name, "error: %v", err)
			p.Logger.PhaseEnd(name, err)
			return err
		}
		p.Logger.Log(name, "ok: %q", line)
	}
	p.Logger.PhaseEnd(name, nil)
	return nil
}

package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

// remoteExecFn is a test seam for `pier exec <env> <cmd...>`.
var remoteExecFn = deploy.RemoteExec

func newExecCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "exec [env] <cmd...>",
		Short: "Run a one-off command in the app container (prefix <env> to target a deploy host)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}
}

func runExec(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if env, rest, remote, cerr := resolveRemoteExec(cfg, args); cerr != nil {
		return cerr
	} else if remote {
		return runRemoteExec(cmd, cfg, env, rest)
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	tty := docker.DetectTTY()
	if err := ensureUp(cmd, c); err != nil {
		return err
	}
	return c.Exec(context.Background(), docker.ExecOpts{Service: "laravel.test", User: shellUser(), TTY: tty}, args...)
}

// resolveRemoteExec decides whether args select a remote exec: the
// first argument must name a configured deploy env and be followed by
// at least one command argument. A lone env name is a clear error
// rather than a match; a first argument that names no env is a local
// exec. Returns remote=false when nothing matches.
func resolveRemoteExec(cfg *config.Config, args []string) (env string, rest []string, remote bool, err error) {
	if len(args) == 0 {
		return "", nil, false, nil
	}
	env = args[0]
	if _, ok := cfg.Deploy[env]; !ok {
		return "", nil, false, nil
	}
	if len(args) < 2 {
		return "", nil, false, cliError("remote exec: no command given for env %q", env)
	}
	return env, args[1:], true, nil
}

// runRemoteExec runs the one-off remote command on the
// [deploy.<env>] host. Dial/handshake failures (ErrPreflight) map to
// the SSH error kind; an interactive abort and every typed remote
// error pass through unchanged.
func runRemoteExec(cmd *cobra.Command, cfg *config.Config, env string, args []string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	err := remoteExecFn(cmd.Context(), newSSHConfig(dc), dc.Path, args)
	if errors.Is(err, deploy.ErrAborted) {
		return err
	}
	if errors.Is(err, deploy.ErrPreflight) {
		return SSHError(err)
	}
	return err
}

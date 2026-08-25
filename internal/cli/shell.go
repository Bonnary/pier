package cli

import (
	"context"
	"errors"
	"io"
	"os/user"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

// remoteShellFn is a test seam for `pier shell <env>`; production
// code dials and runs the interactive remote shell.
var remoteShellFn = deploy.RemoteShell

func newShellCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "shell [env]",
		Short: "Open an interactive bash in the app container (add <env> to target a deploy host)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd, args)
		},
	}
}

func runShell(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		return runRemoteShell(cmd, cfg, args[0])
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	tty := docker.DetectTTY()
	if err := ensureUp(cmd, c); err != nil {
		return err
	}
	return c.Exec(context.Background(), docker.ExecOpts{Service: "laravel.test", User: "0", TTY: tty}, "bash")
}

// runRemoteShell runs the interactive remote shell on the
// [deploy.<env>] host. Dial/handshake failures (ErrPreflight) map to
// the SSH error kind; an interactive abort and every typed remote
// error pass through unchanged.
func runRemoteShell(cmd *cobra.Command, cfg *config.Config, env string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	err := remoteShellFn(cmd.Context(), newSSHConfig(dc), dc.Path)
	if errors.Is(err, deploy.ErrAborted) {
		return err
	}
	if errors.Is(err, deploy.ErrPreflight) {
		return SSHError(err)
	}
	return err
}

func shellUser() string {
	u, err := user.Current()
	if err != nil {
		return "www-data"
	}
	if u.Uid == "0" {
		return "www-data"
	}
	return u.Uid
}

func ensureUp(cmd *cobra.Command, c *docker.Compose) error {
	ps, err := c.PS(context.Background())
	if err != nil {
		return err
	}
	if !containsString(string(ps), "laravel.test") {
		return ExecDownError()
	}
	return nil
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

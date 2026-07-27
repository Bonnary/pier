package cli

import (
	"context"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/docker"
)

func newExecCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <cmd...>",
		Short: "Run a one-off command in the laravel.test container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}
}

func runExec(cmd *cobra.Command, args []string) error {
	if _, err := config.Load(cfgPath); err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	tty := docker.DetectTTY()
	if err := ensureUp(cmd, c); err != nil {
		return err
	}
	return c.Exec(context.Background(), docker.ExecOpts{Service: "laravel.test", User: shellUser(), TTY: tty}, args...)
}

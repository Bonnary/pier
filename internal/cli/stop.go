package cli

import (
	"context"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
)

func newStopCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop and remove dev containers (volumes preserved)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd)
		},
	}
}

func runStop(cmd *cobra.Command) error {
	_, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	return c.Down(context.Background())
}

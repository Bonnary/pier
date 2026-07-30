package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
)

func newStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show project and container status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd)
		},
	}
}

func runStatus(cmd *cobra.Command) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	ps, err := c.PS(context.Background())
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", cfg.Project.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "domain:  %s\n", cfg.Project.Domain)
	fmt.Fprintf(cmd.OutOrStdout(), "stack:   %s (php %s, node %s)\n", cfg.Stack.Type, cfg.Stack.PHP, cfg.Stack.Node)
	fmt.Fprintf(cmd.OutOrStdout(), "services: %v\n", cfg.Stack.Services)
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", string(ps))
	return nil
}

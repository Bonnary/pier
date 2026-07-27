package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/docker"
	laravelpkg "github.com/pcnerd/pier/internal/stack/laravel"
)

func newDevCmd(stdout, stderr io.Writer) *cobra.Command {
	var noBuild bool
	cmd := &cobra.Command{
		Use:   "dev [services...]",
		Short: "Bring up the dev Docker stack",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDev(cmd, args, noBuild)
		},
	}
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip image build")
	return cmd
}

func runDev(cmd *cobra.Command, services []string, noBuild bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)
	composePath := filepath.Join(dir, "docker-compose.yml")
	var existing string
	if b, err := os.ReadFile(composePath); err == nil {
		existing = string(b)
	}
	merged, _, err := laravelpkg.MergeDev(existing, *cfg, func(w laravelpkg.MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		return err
	}
	if err := os.WriteFile(composePath, []byte(merged), 0644); err != nil {
		return err
	}
	c := &docker.Compose{Workdir: dir, File: composePath, Runner: dockerRunner}
	ctx := context.Background()
	if !noBuild {
		if err := c.Build(ctx); err != nil {
			return err
		}
	}
	if err := c.Up(ctx, services...); err != nil {
		return err
	}
	ps, err := c.PS(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(ps))
	return nil
}

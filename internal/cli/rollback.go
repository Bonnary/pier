package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
)

func newRollbackCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback <env>",
		Short: "Roll back <env> to the previously deployed image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollback(cmd, args[0])
		},
	}
}

func runRollback(cmd *cobra.Command, env string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	ssh := newSSHConfig(dc)
	c, err := deploy.Dial(cmd.Context(), ssh)
	if err != nil {
		return err
	}
	defer c.Close()
	bootstrapped, err := deploy.ProbeBootstrap(cmd.Context(), c)
	if err != nil {
		return err
	}
	if !bootstrapped {
		return deploy.NotBootstrappedError(env)
	}
	logger := NewLogger(jsonOut, cmd.OutOrStdout())
	logger.PhaseStart("rollback")
	if err := deploy.Rollback(context.Background(), deploy.SFTPStateStore{Client: c}, c, dc.Path, cfg.Project.Name); err != nil {
		logger.PhaseEnd("rollback", err)
		return err
	}
	logger.PhaseEnd("rollback", nil)
	return nil
}

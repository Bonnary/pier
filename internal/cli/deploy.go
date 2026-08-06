package cli

import (
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
)

func newDeployCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy <env>",
		Short: "Deploy the project to <env>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(cmd, args[0])
		},
	}
}

func runDeploy(cmd *cobra.Command, env string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	logger := NewLogger(jsonOut, cmd.OutOrStdout())
	p := &deploy.Pipeline{
		Config:    cfg,
		Env:       env,
		DeployEnv: dc,
		Logger:    logger,
		SSH:       newSSHConfig(dc),
		BuildSSH:  newBuildSSHConfig(dc),
		Health:    deploy.DefaultHealthConfig(*cfg, env),
	}
	return p.Run(cmd.Context())
}

func sshKeyPath() string {
	if v := osGetenv("DEPLOY_SSH_KEY"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".ssh", "id_ed25519")
}

func homeDir() string {
	h, _ := osUserHomeDir()
	return h
}

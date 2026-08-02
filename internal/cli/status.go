package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

// statusDial is a seam for tests to inject a fake StatusRunner into
// `pier status <env>` without a real SSH connection.
var statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
	return deploy.Dial(ctx, cfg)
}

// statusHealthURL is a seam for tests to point the remote health
// probe at a local test server instead of the project domain.
var statusHealthURL = func(cfg *config.Config, env string) string {
	return deploy.ResolvedURL(*cfg, env) + "/up"
}

func newStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status [env]",
		Short: "Show project and container status (add <env> to probe a remote host)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, args)
		},
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		return runRemoteStatus(cmd, cfg, args[0])
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

// runRemoteStatus probes the [deploy.<env>] host over SSH: container
// state, disk usage, last deploy record, and a single health check.
func runRemoteStatus(cmd *cobra.Command, cfg *config.Config, env string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	client, err := statusDial(cmd.Context(), newSSHConfig(dc))
	if err != nil {
		if errors.Is(err, deploy.ErrAborted) {
			return err
		}
		return SSHError(err)
	}
	defer client.Close()

	health := deploy.DefaultHealthConfig(cfg.Project.Domain)
	health.URL = statusHealthURL(cfg, env)
	health.Timeout = 10 * time.Second
	health.Interval = 100 * time.Millisecond
	health.MaxAttempts = 1

	rep, err := deploy.RemoteStatus(cmd.Context(), dc, health, client)
	if err != nil {
		return deploy.RemoteDockerError(dc.Host, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", cfg.Project.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "env:     %s (host: %s)\n", env, dc.Host)
	fmt.Fprintf(cmd.OutOrStdout(), "services: %v\n", cfg.Stack.Services)
	fmt.Fprintf(cmd.OutOrStdout(), "containers:\n%s\n", indentBlock(rep.Containers))
	fmt.Fprintf(cmd.OutOrStdout(), "disk:\n%s\n", indentBlock(rep.Disk))
	fmt.Fprintf(cmd.OutOrStdout(), "docker disk:\n%s\n", indentBlock(rep.DockerDisk))
	if rep.Healthy {
		fmt.Fprintf(cmd.OutOrStdout(), "health: OK (%s)\n", health.URL)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "health: DOWN (%s)\n", health.URL)
	}
	if rep.State != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "last deploy: %s, %s by %s\n", rep.State.Current, rep.State.DeployedAt, rep.State.DeployedBy)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "last deploy: none yet\n")
	}
	return nil
}

// indentBlock prefixes every line of s with two spaces, showing
// "(none)" for empty probe output.
func indentBlock(s string) string {
	if s == "" {
		return "  (none)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

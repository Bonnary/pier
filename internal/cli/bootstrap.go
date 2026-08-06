package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/tui"
)

type bootstrapFlags struct {
	all   bool
	force bool
}

// test seams — overridable from *_test.go.
var (
	pickEnvTUI     = tui.PickEnv
	probeEnvFn     = deploy.ProbeEnv
	bootstrapEnvFn = deploy.BootstrapEnv
	readSudoPwd    = readPassword
)

// errAlreadyBootstrapped marks a skipped machine so the caller can
// print its "already bootstrapped" line.
var errAlreadyBootstrapped = errors.New("already bootstrapped")

// provisionOne runs the full bootstrap flow (skip-check, sudo prompt,
// provision, path creation) for one machine and prints its status.
func provisionOne(ctx context.Context, cmd *cobra.Command, sshCfg deploy.SSHConfig, user, path string, force bool) error {
	if !force {
		ok, err := probeEnvFn(ctx, sshCfg)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(cmd.OutOrStdout(), "already bootstrapped — skipping\n")
			return errAlreadyBootstrapped
		}
	}
	pw, err := readSudoPwd(fmt.Sprintf("sudo password for %s@%s: ", sshCfg.User, sshCfg.Host))
	if err != nil {
		return err
	}
	bootstrap := func() error {
		return bootstrapEnvFn(ctx, sshCfg, pw, deploy.BootstrapOpts{
			User:     user,
			Force:    force,
			Path:     path,
			OnStdout: func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) },
			OnStderr: func(line string) { fmt.Fprintln(cmd.ErrOrStderr(), line) },
		})
	}
	err = bootstrap()
	if errors.Is(err, deploy.ErrSudoWrongPassword) {
		pw, err = readSudoPwd("wrong password — try again: ")
		if err != nil {
			return err
		}
		err = bootstrap()
	}
	if errors.Is(err, deploy.ErrSudoNotSudoers) {
		return fmt.Errorf("%w: add %q to sudoers on %s first, or bootstrap as a different user",
			err, sshCfg.User, sshCfg.Host)
	}
	if err != nil {
		return err
	}
	return nil
}

// newBootstrapCmd returns the `pier bootstrap` command: one-time
// server provisioning (Docker install + docker group membership for
// the deploy user), with a hidden sudo-password prompt.
func newBootstrapCmd(stdout, stderr io.Writer) *cobra.Command {
	f := &bootstrapFlags{}
	cmd := &cobra.Command{
		Use:   "bootstrap [env...]",
		Short: "Provision servers: install Docker and grant the deploy user docker access",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "bootstrap every env in pier.toml")
	cmd.Flags().BoolVar(&f.force, "force", false, "re-provision even if already bootstrapped")
	return cmd
}

// runBootstrap resolves the target envs and provisions each one in
// order. Already-bootstrapped servers are skipped (unless --force);
// the sudo password is prompted for per env, with one re-prompt on a
// wrong password.
func runBootstrap(cmd *cobra.Command, args []string, f *bootstrapFlags) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	envs, err := resolveBootstrapEnvs(cfg, args, f.all)
	if err != nil {
		return err
	}
	for _, env := range envs {
		dc := cfg.Deploy[env]
		if err := provisionOne(cmd.Context(), cmd, newSSHConfig(dc), dc.User, dc.Path, f.force); err != nil {
			if errors.Is(err, errAlreadyBootstrapped) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: already bootstrapped — skipping\n", env)
				continue
			}
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: done\n", env)
		if dc.BuilderMode() == "build_server" {
			buildSSH := newBuildSSHConfig(dc)
			if err := provisionOne(cmd.Context(), cmd, buildSSH, dc.BuildUser, dc.BuildPath, f.force); err != nil {
				if errors.Is(err, errAlreadyBootstrapped) {
					fmt.Fprintf(cmd.OutOrStdout(), "%s (build server): already bootstrapped — skipping\n", env)
					continue
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (build server): done\n", env)
		}
	}
	return nil
}

// resolveBootstrapEnvs turns the command's env arguments, --all, or
// (on a TTY) an interactive picker into the ordered list of env
// names to provision. Env names are sorted alphabetically when the
// picker or --all selects them, so the order is deterministic.
func resolveBootstrapEnvs(cfg *config.Config, args []string, all bool) ([]string, error) {
	if len(args) > 0 && all {
		return nil, fmt.Errorf("cannot combine env arguments with --all")
	}
	names := sortedEnvNames(cfg)
	if all {
		if len(names) == 0 {
			return nil, fmt.Errorf("no [deploy.<env>] sections in pier.toml")
		}
		return names, nil
	}
	if len(args) == 0 {
		if tuiForTest() {
			if len(names) == 0 {
				return nil, fmt.Errorf("no [deploy.<env>] sections in pier.toml")
			}
			labels := make([]string, len(names))
			for i, n := range names {
				labels[i] = fmt.Sprintf("%s (%s)", n, cfg.Deploy[n].Host)
			}
			idx, err := pickEnvTUI(labels, 0)
			if err != nil {
				if errors.Is(err, tui.ErrAborted) {
					return nil, nil // clean abort: exit 0
				}
				return nil, err
			}
			return []string{names[idx]}, nil
		}
		return nil, fmt.Errorf("no env given; pass one or more env names or --all")
	}
	for _, a := range args {
		if _, ok := cfg.Deploy[a]; !ok {
			return nil, cliError("no [deploy.%s] section in pier.toml", a)
		}
	}
	return args, nil
}

// sortedEnvNames returns the env names in pier.toml sorted
// alphabetically. Go maps don't preserve TOML order; sorting keeps
// the picker and --all deterministic.
func sortedEnvNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Deploy))
	for n := range cfg.Deploy {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

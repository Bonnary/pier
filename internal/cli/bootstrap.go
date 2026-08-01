package cli

import (
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
	readSudoPwd    = readSudoPassword
)

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
		sshCfg := deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()}
		if !f.force {
			ok, err := probeEnvFn(cmd.Context(), sshCfg)
			if err != nil {
				return err
			}
			if ok {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: already bootstrapped — skipping\n", env)
				continue
			}
		}
		pw, err := readSudoPwd(fmt.Sprintf("sudo password for %s@%s: ", dc.User, dc.Host))
		if err != nil {
			return err
		}
		err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{User: dc.User, Force: f.force})
		if errors.Is(err, deploy.ErrSudoWrongPassword) {
			pw, err = readSudoPwd("wrong password — try again: ")
			if err != nil {
				return err
			}
			err = bootstrapEnvFn(cmd.Context(), sshCfg, pw, deploy.BootstrapOpts{User: dc.User, Force: f.force})
		}
		if errors.Is(err, deploy.ErrSudoNotSudoers) {
			return fmt.Errorf("%w: add %q to sudoers on %s first, or bootstrap as a different user",
				err, dc.User, dc.Host)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: done\n", env)
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
			idx, err := pickEnvTUI(labels)
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

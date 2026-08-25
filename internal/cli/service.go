package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
	"github.com/Bonnary/pier/internal/tui"
)

type serviceFlags struct {
	noUp bool
}

// test seam — overridable from *_test.go. tuiForTest lives in init.go.
var pickServicesTUI = tui.PickServices

func newServiceCmd(stdout, stderr io.Writer) *cobra.Command {
	f := &serviceFlags{}
	cmd := &cobra.Command{
		Use:   "service [env]",
		Short: "Manage sidecar services with an interactive picker",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env := ""
			if len(args) > 0 {
				env = args[0]
			}
			return runService(cmd, env, f)
		},
	}
	cmd.Flags().BoolVar(&f.noUp, "no-up", false, "skip bringing newly added dev services up after saving")
	return cmd
}

func runService(cmd *cobra.Command, env string, f *serviceFlags) error {
	if !tuiForTest() {
		return cliError("pier service is interactive; run it in a terminal or edit pier.toml directly")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if env != "" {
		return runServiceEnv(cmd, cfg, env)
	}
	return runServiceDev(cmd, cfg, f)
}

// runServiceDev manages [stack].services: the picker shows every
// supported service with the current dev list pre-ticked; the final
// selection is written back, docker-compose.yml is re-rendered, and
// newly added services are brought up unless --no-up.
func runServiceDev(cmd *cobra.Command, cfg *config.Config, f *serviceFlags) error {
	picked, err := pickServicesTUI(laravelpkg.SupportedServices(), cfg.Stack.Services)
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return AbortedError()
		}
		return err
	}
	before := cfg.Stack.Services
	added := listDiff(picked, before)
	removed := listDiff(before, picked)
	if len(added) == 0 && len(removed) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no changes")
		return nil
	}
	cfg.Stack.Services = picked
	if err := writeConfig(cfgPath, *cfg); err != nil {
		return err
	}
	if err := rerenderDevCompose(cfgPath, *cfg); err != nil {
		return err
	}
	if !f.noUp && len(added) > 0 {
		dir := filepath.Dir(cfgPath)
		c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
		if err := c.Up(context.Background(), added...); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "added: %v\nremoved: %v\n", added, removed)
	return nil
}

// runServiceEnv manages [deploy.<env>].services: the picker shows
// every supported service with the env's effective list pre-ticked
// (inherited from [stack] when the env has no explicit list yet).
// The final selection is written to [deploy.<env>].services; the
// next deploy re-renders the remote compose from it.
func runServiceEnv(cmd *cobra.Command, cfg *config.Config, env string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	effective := cfg.ServicesForEnv(env)
	picked, err := pickServicesTUI(laravelpkg.SupportedServices(), effective)
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return AbortedError()
		}
		return err
	}
	want := slices.Clone(effective)
	sort.Strings(want)
	if slices.Equal(picked, want) {
		fmt.Fprintln(cmd.OutOrStdout(), "no changes")
		return nil
	}
	dc.Services = picked
	cfg.Deploy[env] = dc
	if err := writeConfig(cfgPath, *cfg); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "services: %v\n", picked)
	return nil
}

// listDiff returns the elements of xs that are not in ys.
func listDiff(xs, ys []string) []string {
	set := map[string]bool{}
	for _, y := range ys {
		set[y] = true
	}
	var out []string
	for _, x := range xs {
		if !set[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func writeConfig(path string, cfg config.Config) error {
	b, err := tomlEncode(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func rerenderDevCompose(cfgPath string, cfg config.Config) error {
	dir := filepath.Dir(cfgPath)
	composePath := filepath.Join(dir, "docker-compose.yml")
	var existing string
	if b, err := os.ReadFile(composePath); err == nil {
		existing = string(b)
	}
	merged, _, err := laravelpkg.MergeDev(existing, cfg, func(w laravelpkg.MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		return err
	}
	return os.WriteFile(composePath, []byte(merged), 0644)
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

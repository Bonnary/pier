package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/docker"
	laravelpkg "github.com/pcnerd/pier/internal/stack/laravel"
	"github.com/pcnerd/pier/internal/tui"
)

type serviceFlags struct {
	noUp   bool
	noStop bool
}

// test seams — overridable from *_test.go. tuiForTest lives in init.go.
var (
	pickAddTUI    = tui.PickServicesToAdd
	pickRemoveTUI = tui.PickServicesToRemove
)

func newServiceCmd(stdout, stderr io.Writer) *cobra.Command {
	f := &serviceFlags{}
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Add or remove services from pier.toml",
	}
	cmd.PersistentFlags().BoolVar(&f.noUp, "no-up", false, "skip bringing the service up after add")
	cmd.PersistentFlags().BoolVar(&f.noStop, "no-stop", false, "skip stopping the service after remove")

	add := &cobra.Command{
		Use:   "add [name...]",
		Short: "Add one or more services to pier.toml",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceAdd(cmd, args, f)
		},
	}
	rm := &cobra.Command{
		Use:   "remove [name...]",
		Short: "Remove one or more services from pier.toml",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceRemove(cmd, args, f)
		},
	}
	cmd.AddCommand(add, rm)
	return cmd
}

func runServiceAdd(cmd *cobra.Command, names []string, f *serviceFlags) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if tuiForTest() && len(names) == 0 {
		picked, err := pickAddTUI(laravelpkg.SupportedServices(), cfg.Stack.Services)
		if err != nil {
			if errors.Is(err, tui.ErrAborted) {
				return AbortedError()
			}
			return err
		}
		if len(picked) == 0 {
			return fmt.Errorf("no services selected")
		}
		names = picked
	}
	for _, n := range names {
		if n == "" {
			return fmt.Errorf("empty service name")
		}
		_ = laravelpkg.New().DefaultConfig()
	}
	updated, added := upsertServices(cfg, names)
	if err := writeConfig(cfgPath, updated); err != nil {
		return err
	}
	if err := rerenderDevCompose(cfgPath, updated); err != nil {
		return err
	}
	if !f.noUp && len(added) > 0 {
		dir := filepath.Dir(cfgPath)
		c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
		if err := c.Up(context.Background(), added...); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "added: %v\n", added)
	return nil
}

func runServiceRemove(cmd *cobra.Command, names []string, f *serviceFlags) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if tuiForTest() && len(names) == 0 {
		picked, err := pickRemoveTUI(cfg.Stack.Services)
		if err != nil {
			if errors.Is(err, tui.ErrAborted) {
				return AbortedError()
			}
			return err
		}
		if len(picked) == 0 {
			return fmt.Errorf("no services selected")
		}
		names = picked
	}
	updated, removed := removeServices(cfg, names)
	if err := writeConfig(cfgPath, updated); err != nil {
		return err
	}
	if err := rerenderDevCompose(cfgPath, updated); err != nil {
		return err
	}
	if !f.noStop && len(removed) > 0 {
		dir := filepath.Dir(cfgPath)
		c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
		_ = c
	}
	fmt.Fprintf(cmd.OutOrStdout(), "removed: %v\n", removed)
	return nil
}

func upsertServices(cfg *config.Config, names []string) (config.Config, []string) {
	have := map[string]bool{}
	for _, n := range cfg.Stack.Services {
		have[n] = true
	}
	var added []string
	for _, n := range names {
		if !have[n] {
			cfg.Stack.Services = append(cfg.Stack.Services, n)
			added = append(added, n)
			have[n] = true
		}
	}
	sort.Strings(cfg.Stack.Services)
	return *cfg, added
}

func removeServices(cfg *config.Config, names []string) (config.Config, []string) {
	rm := map[string]bool{}
	for _, n := range names {
		rm[n] = true
	}
	var removed []string
	var kept []string
	for _, n := range cfg.Stack.Services {
		if rm[n] {
			removed = append(removed, n)
			continue
		}
		kept = append(kept, n)
	}
	cfg.Stack.Services = kept
	return *cfg, removed
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

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
	"github.com/Bonnary/pier/internal/portcheck"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
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
	maybeWarnLanExposure(cmd.ErrOrStderr(), cfg)
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

	hostPorts, err := laravelpkg.CollectHostPorts([]byte(merged), cfg.Dev.Services)
	if err != nil {
		return err
	}
	taken, err := portcheck.Probe(context.Background(), hostPorts)
	if err != nil {
		return err
	}
	if len(taken) > 0 {
		printPortConflict(cmd.ErrOrStderr(), taken)
		return PortInUseError(hostPortsTaken(taken))
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

	printReadyBlock(cmd.OutOrStdout(), cfg, hostPorts)
	return nil
}

func printPortConflict(w io.Writer, taken map[int]string) {
	ports := make([]int, 0, len(taken))
	for p := range taken {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		who := taken[p]
		if who != "" {
			fmt.Fprintf(w, "pier: port %d is in use by %s\n", p, who)
		} else {
			fmt.Fprintf(w, "pier: port %d is already in use\n", p)
		}
	}
	fmt.Fprintln(w, "hint: edit [dev.ports] in pier.toml to remap")
}

func hostPortsTaken(taken map[int]string) []int {
	out := make([]int, 0, len(taken))
	for p := range taken {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func printReadyBlock(w io.Writer, cfg *config.Config, hostPorts []int) {
	bind := cfg.Dev.Bind
	fmt.Fprintln(w, "pier dev: ready")
	if v, ok := laravelpkg.ResolvePort("laravel", cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
		fmt.Fprintf(w, "  App:    http://%s:%d\n", bind, v)
	}
	if v, ok := laravelpkg.ResolvePort("vite", cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
		fmt.Fprintf(w, "  Vite:   http://%s:%d\n", bind, v)
	}
	for _, key := range []string{"mysql", "postgres", "redis", "meilisearch"} {
		if v, ok := laravelpkg.ResolvePort(key, cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
			fmt.Fprintf(w, "  %s:  %s:%d\n", capitalize(key), bind, v)
		}
	}
	for _, key := range []string{"mailpit_smtp", "mailpit_ui", "s3_api", "s3_filer", "s3_master"} {
		if v, ok := laravelpkg.ResolvePort(key, cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
			fmt.Fprintf(w, "  %s: %s:%d\n", strings.ToUpper(key), bind, v)
		}
	}
}

// maybeWarnLanExposure prints a one-time warning to w when the user has
// opted in to LAN exposure via [dev] bind = "0.0.0.0". Loopback is the
// safe default; this only fires for the explicit opt-in. The warning
// points the user at the loopback default to restore safety.
func maybeWarnLanExposure(w io.Writer, cfg *config.Config) {
	if cfg.Dev.Bind == config.DefaultDevBind {
		return
	}
	fmt.Fprintf(w,
		"warning: [dev] bind = %q — dev ports are reachable from your LAN\n"+
			"         set [dev] bind = %q in pier.toml to restore loopback-only\n",
		cfg.Dev.Bind, config.DefaultDevBind)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

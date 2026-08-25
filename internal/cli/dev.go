package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
	"github.com/Bonnary/pier/internal/portcheck"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
)

// appReadyTimeout is the default total time `pier dev` waits for the
// app to answer before showing the ready block anyway. Windows
// containers commonly take 10-30s to boot.
const appReadyTimeout = 120 * time.Second

// devAppTimeout is the wait budget used by runDev; a package var so
// tests can shorten it.
var devAppTimeout = appReadyTimeout

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
	// Wait for the app only when it is among the services being
	// started (or when the full stack is); `pier dev redis` should
	// not stall when the app container wasn't started.
	if url := appURL(cfg); url != "" && (len(services) == 0 || slices.Contains(services, "laravel.test")) {
		waitForApp(cmd.OutOrStdout(), url, devAppTimeout)
	}
	ps, err := c.PS(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(ps))

	printReadyBlock(cmd.OutOrStdout(), cfg)
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

// appURL returns the HTTP URL of the dev app, or "" when the user has
// opted out of exposing the laravel port ([dev.ports].laravel = 0).
func appURL(cfg *config.Config) string {
	v, ok := laravelpkg.ResolvePort("laravel", cfg.Dev.Ports, laravelpkg.DevPortDefaults)
	if !ok {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", cfg.Dev.Bind, v)
}

// waitForApp GETs url until any HTTP response arrives (the app server
// is up), printing a "waiting for app" line after the first failed
// attempt. On timeout it prints a warning and returns false; the
// caller still shows the ready block.
func waitForApp(w io.Writer, url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	printed := false
	for {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				return true
			}
		}
		if time.Now().After(deadline) {
			break
		}
		if !printed {
			fmt.Fprintf(w, "pier dev: waiting for app at %s...\n", url)
			printed = true
		}
		time.Sleep(time.Second)
	}
	fmt.Fprintf(w, "pier dev: app still not responding after %s (check `docker compose ps`)\n", timeout)
	return false
}

// printReadyBlock prints "pier dev: ready" plus the app, Vite, and
// every sidecar/dev service the user actually configured.
func printReadyBlock(w io.Writer, cfg *config.Config) {
	bind := cfg.Dev.Bind
	fmt.Fprintln(w, "pier dev: ready")
	if v, ok := laravelpkg.ResolvePort("laravel", cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
		fmt.Fprintf(w, "  App:    http://%s:%d\n", bind, v)
	}
	if v, ok := laravelpkg.ResolvePort("vite", cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
		fmt.Fprintf(w, "  Vite:   http://%s:%d\n", bind, v)
	}
	for _, name := range cfg.Stack.Services {
		for _, key := range laravelpkg.PortKeysFor(name) {
			if v, ok := laravelpkg.ResolvePort(key, cfg.Dev.Ports, laravelpkg.DevPortDefaults); ok {
				fmt.Fprintf(w, "  %s: %s:%d\n", labelFor(key), bind, v)
			}
		}
	}
	names := make([]string, 0, len(cfg.Dev.Services))
	for n := range cfg.Dev.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, p := range cfg.Dev.Services[name].Ports {
			if host, ok := laravelpkg.HostOfPortBinding(p); ok {
				fmt.Fprintf(w, "  %s: %s:%d\n", name, bind, host)
			}
		}
	}
}

// labelFor renders the ready-block label for a sidecar port key:
// capitalized for the DB/cache keys, uppercase for the rest
// (matching pier's historical output).
func labelFor(key string) string {
	switch key {
	case "mysql", "postgres", "redis", "meilisearch":
		return capitalize(key)
	}
	return strings.ToUpper(key)
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

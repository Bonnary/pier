package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/stack"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
	"github.com/Bonnary/pier/internal/tui"
)

type Decision = laravelpkg.Decision

const (
	DecisionKeep = laravelpkg.DecisionKeep
	DecisionDrop = laravelpkg.DecisionDrop
)

type initFlags struct {
	php          string
	node         string
	services     []string
	devcontainer bool
	builder      string
	host         string
	user         string
	path         string
	buildHost    string
	buildUser    string
	buildPath    string
}

// test seams — overridable from *_test.go.
var (
	tuiForTest = tui.ShouldRun
	runInitTUI = tui.RunInit
)

func newInitCmd(stdout, stderr io.Writer) *cobra.Command {
	f := &initFlags{}
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize a new pier project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			return runInit(cmd, path, f)
		},
	}
	cmd.Flags().StringVar(&f.php, "php", "", "PHP version (8.2, 8.3, 8.4, 8.5)")
	cmd.Flags().StringVar(&f.node, "node", "", "Node major version (20, 22)")
	cmd.Flags().StringSliceVar(&f.services, "services", nil, "comma-separated list of services to add")
	cmd.Flags().StringVar(&f.builder, "builder", "", "build machine: host_server, local_machine, or build_server")
	cmd.Flags().StringVar(&f.host, "host", "", "deploy host (SSH target)")
	cmd.Flags().StringVar(&f.user, "user", "", "deploy user")
	cmd.Flags().StringVar(&f.path, "path", "", "deploy path on the host")
	cmd.Flags().StringVar(&f.buildHost, "build-host", "", "build server host (with --builder build_server)")
	cmd.Flags().StringVar(&f.buildUser, "build-user", "", "build server user (with --builder build_server)")
	cmd.Flags().StringVar(&f.buildPath, "build-path", "", "build server path (with --builder build_server)")
	cmd.Flags().BoolVar(&f.devcontainer, "devcontainer", false, "also generate .devcontainer/devcontainer.json")
	return cmd
}

//nolint:gocyclo // long linear prompt sequence per the init plan
func runInit(cmd *cobra.Command, path string, f *initFlags) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !laravelpkg.New().Detect(abs) {
		return fmt.Errorf("no Laravel project found at %s (missing composer.json with laravel/framework or artisan)", abs)
	}
	tomlPath := filepath.Join(abs, "pier.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return fmt.Errorf("pier.toml exists at %s; edit it instead of running init again", tomlPath)
	}
	s, err := laravelpkg.New().DefaultConfig(), error(nil)
	_ = s
	php := f.php
	node := f.node
	services := f.services
	builder := f.builder
	if tuiForTest() && f.php == "" && f.node == "" && len(f.services) == 0 && f.builder == "" {
		res, err := runInitTUI(
			laravelpkg.SupportedPHPRuntimes(),
			laravelpkg.SupportedNodeVersions(),
			laravelpkg.SupportedServices(),
			[]string{"host_server", "local_machine", "build_server"},
		)
		if err != nil {
			return err
		}
		if res.Aborted {
			return AbortedError()
		}
		php = res.PHP
		node = res.Node
		services = res.Services
		builder = res.Builder
	}
	if php == "" {
		php = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "PHP version [8.3]: ", "8.3")
	}
	if node == "" {
		node = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Node version [22]: ", "22")
	}
	if services == nil {
		servicesStr := prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Services (comma-separated, blank for none) [redis,mailpit,s3]: ", "")
		if servicesStr != "" {
			services = splitCSV(servicesStr)
		}
	}
	if builder == "" {
		builder = promptBuilder(cmd.OutOrStdout(), cmd.InOrStdin())
	}
	if !validBuilderValue(builder) {
		return cliError("--builder %q: must be host_server, local_machine, or build_server", builder)
	}
	domain := prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Production domain (e.g. myapp.com; blank = plain HTTP by IP): ", "")
	host := f.host
	if host == "" {
		host = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Deploy host (SSH target Domain name or IP address, enter to skip): ", "")
	}
	user := f.user
	if user == "" {
		user = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Deploy user (enter to skip): ", "")
	}
	path = f.path
	if path == "" {
		path = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Deploy path (enter to skip): ", "")
	}
	if (host == "") != (user == "") || (host == "") != (path == "") {
		return cliError("host, user, and path must all be set together (leave all three empty to scaffold)")
	}
	buildHost, buildUser, buildPath := f.buildHost, f.buildUser, f.buildPath
	if builder == "build_server" {
		if buildHost == "" {
			if buildHost, err = promptRequired(cmd.OutOrStdout(), cmd.InOrStdin(), "Build server host: ", ""); err != nil {
				return err
			}
		}
		if buildUser == "" {
			if buildUser, err = promptRequired(cmd.OutOrStdout(), cmd.InOrStdin(), "Build server user: ", ""); err != nil {
				return err
			}
		}
		if buildPath == "" {
			if buildPath, err = promptRequired(cmd.OutOrStdout(), cmd.InOrStdin(), "Build server path: ", ""); err != nil {
				return err
			}
		}
	}
	dc := config.DeployConfig{
		Services: services,
		Builder:  builder,
	}
	if host != "" {
		dc.Host = host
	}
	if user != "" {
		dc.User = user
	}
	if path != "" {
		dc.Path = path
	}
	if host != "" && user != "" && path != "" {
		dc.Branch = "main"
	}
	dc.Domain = domain
	if builder == "build_server" {
		dc.BuildHost, dc.BuildUser, dc.BuildPath = buildHost, buildUser, buildPath
	}
	cfg := config.Config{
		Project: config.ProjectConfig{Name: filepath.Base(abs)},
		Stack:   config.StackConfig{Type: "laravel", PHP: php, Node: node, Services: services},
		Deploy:  map[string]config.DeployConfig{"production": dc},
	}
	b, _ := tomlMarshal(cfg)
	if err := os.WriteFile(tomlPath, b, 0644); err != nil {
		return fmt.Errorf("write pier.toml: %w", err)
	}
	stackMod, _ := stack.ForName(cfg.Stack.Type)
	devFiles, err := stackMod.GenerateDevCompose(cfg)
	if err != nil {
		return err
	}
	prodFiles, err := stackMod.GenerateProdFiles(cfg, "production")
	if err != nil {
		return err
	}
	composePath := filepath.Join(abs, "docker-compose.yml")
	if existing, err := os.ReadFile(composePath); err == nil {
		merged, warns, err := laravelpkg.MergeDev(string(existing), cfg, func(w laravelpkg.MergeWarning) Decision {
			fmt.Fprintf(cmd.OutOrStdout(), "warning: %s key %q in existing docker-compose.yml (keep or drop?): \n", w.Service, w.Key)
			ans := prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "  [k]eep/[d]rop: ", "k")
			if ans == "d" {
				return DecisionDrop
			}
			return DecisionKeep
		})
		if err != nil {
			return err
		}
		for _, w := range warns {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s key %q\n", w.Service, w.Key)
		}
		devFiles = replaceFile(devFiles, "docker-compose.yml", []byte(merged))
	}
	for _, file := range append(devFiles, prodFiles...) {
		dest := filepath.Join(abs, file.Path)
		// config/trustedproxy.php is user-adjacent (it lives in the
		// app's config dir): an existing file may pin a specific proxy
		// instead of pier's '*' default, so never overwrite it.
		if file.Path == "config/trustedproxy.php" {
			if _, err := os.Stat(dest); err == nil {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, file.Contents, file.Mode); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
	}
	changed, err := laravelpkg.EnsureViteHost(abs)
	if err != nil {
		return fmt.Errorf("patch vite.config: %w", err)
	}
	if changed {
		fmt.Fprintf(cmd.OutOrStdout(),
			"patched vite.config.ts: set server.host=true (required so Vite is reachable from the host through the Docker port forward)\n")
	}
	if f.devcontainer {
		if err := writeDevcontainer(abs); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "initialized pier project at %s\n", abs)
	return nil
}

func prompt(stdout io.Writer, stdin io.Reader, label, def string) string {
	fmt.Fprint(stdout, label)
	var line []byte
	var b [1]byte
	for {
		n, err := stdin.Read(b[:])
		if n == 1 {
			switch c := b[0]; c {
			case '\n':
				if len(line) == 0 {
					return def
				}
				return string(line)
			case '\r':
				continue
			default:
				line = append(line, c)
			}
			continue
		}
		if err != nil {
			if len(line) == 0 {
				return def
			}
			return string(line)
		}
	}
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// validBuilderValue reports whether v is one of the three builder
// modes config accepts.
func validBuilderValue(v string) bool {
	return v == "host_server" || v == "local_machine" || v == "build_server"
}

// promptBuilder asks the build-machine question as a numbered plain
// prompt — the TUI-free fallback when bubbletea cannot run. Defaults
// to host_server.
func promptBuilder(stdout io.Writer, stdin io.Reader) string {
	fmt.Fprintln(stdout, "Where should the production image be built?")
	fmt.Fprintln(stdout, "  1) host_server — on the deploy host (default)")
	fmt.Fprintln(stdout, "  2) local_machine — on this machine")
	fmt.Fprintln(stdout, "  3) build_server — on a dedicated server")
	ans := prompt(stdout, stdin, "choose [1]: ", "1")
	switch ans {
	case "2", "local_machine":
		return "local_machine"
	case "3", "build_server":
		return "build_server"
	}
	return "host_server"
}

// promptRequired reads a non-empty line from stdin, reprompting on
// empty answers. It cannot tell an empty line from EOF (the shared
// prompt helper returns the default for both), so after 3 consecutive
// empty answers it gives up with an error telling the user to edit
// pier.toml instead.
func promptRequired(stdout io.Writer, stdin io.Reader, label, def string) (string, error) {
	for empty := 0; ; {
		v := prompt(stdout, stdin, label, def)
		if v != "" {
			return v, nil
		}
		empty++
		if empty >= 3 {
			return "", fmt.Errorf("%s: required value missing (3 empty answers); edit pier.toml instead", label)
		}
	}
}

func replaceFile(files stack.Files, path string, contents []byte) stack.Files {
	for i, f := range files {
		if f.Path == path {
			files[i].Contents = contents
			return files
		}
	}
	return append(files, stack.File{Path: path, Contents: contents, Mode: 0644})
}

func tomlMarshal(c config.Config) ([]byte, error) {
	return tomlEncode(c)
}

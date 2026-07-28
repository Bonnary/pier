package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
	laravelpkg "github.com/pcnerd/pier/internal/stack/laravel"
	"github.com/pcnerd/pier/internal/tui"
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
	cmd.Flags().BoolVar(&f.devcontainer, "devcontainer", false, "also generate .devcontainer/devcontainer.json")
	return cmd
}

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
	if tuiForTest() && f.php == "" && f.node == "" && len(f.services) == 0 {
		res, err := runInitTUI(
			laravelpkg.SupportedPHPRuntimes(),
			laravelpkg.SupportedNodeVersions(),
			laravelpkg.SupportedServices(),
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
	cfg := config.Config{
		Project: config.ProjectConfig{Name: filepath.Base(abs), Domain: filepath.Base(abs) + ".example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: php, Node: node, Services: services},
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
	prodFiles, err := stackMod.GenerateProdFiles(cfg)
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
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, file.Contents, file.Mode); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
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
	scanner := bufio.NewScanner(stdin)
	if scanner.Scan() {
		s := scanner.Text()
		if s == "" {
			return def
		}
		return s
	}
	return def
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

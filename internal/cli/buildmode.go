package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/tui"
)

// pickBuilderTUI is a test seam for the builder picker (the shared
// single-select picker, with the current value pre-ticked).
var pickBuilderTUI = func(labels []string, current string) (string, error) {
	idx := 0
	for i, l := range labels {
		if l == current {
			idx = i
		}
	}
	got, err := tui.PickEnv(labels, idx)
	if err != nil {
		return "", err
	}
	return labels[got], nil
}

func newBuildmodeCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "buildmode <env>",
		Short: "Choose where the production image is built",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildmode(cmd, args[0])
		},
	}
}

// runBuildmode edits [deploy.<env>].builder with an interactive
// picker. Choosing build_server additionally prompts for the build
// server's host, user, and path.
func runBuildmode(cmd *cobra.Command, env string) error {
	if !tuiForTest() {
		return cliError("pier buildmode is interactive; run it in a terminal or edit pier.toml directly")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	labels := []string{"host_server", "local_machine", "build_server"}
	picked, err := pickBuilderTUI(labels, dc.BuilderMode())
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return AbortedError()
		}
		return err
	}
	if picked == dc.BuilderMode() {
		fmt.Fprintln(cmd.OutOrStdout(), "no changes")
		return nil
	}
	dc.Builder = picked
	if picked == "build_server" {
		// One shared reader for the whole run: a fresh bufio.Reader
		// per prompt would over-buffer the pipe and lose the
		// subsequent lines.
		reader := bufio.NewReader(os.Stdin)
		dc.BuildHost = promptString(reader, "build server host: ", dc.BuildHost)
		dc.BuildUser = promptString(reader, "build server user: ", dc.BuildUser)
		dc.BuildPath = promptString(reader, "build server path: ", dc.BuildPath)
	}
	cfg.Deploy[env] = dc
	if err := writeConfig(cfgPath, *cfg); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "builder: %s\n", picked)
	return nil
}

// promptString reads one line from reader, trimming whitespace. def is
// returned when the input is empty. Prompts go to stderr so --json
// stdout stays clean.
func promptString(reader *bufio.Reader, prompt, def string) string {
	fmt.Fprintf(os.Stderr, "%s", prompt)
	s, _ := reader.ReadString('\n')
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	return s
}

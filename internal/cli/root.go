// Package cli implements pier's command-line interface: a Cobra-based
// command tree for `pier init`, `pier dev`, `pier shell`, `pier exec`,
// `pier service add|remove`, `pier deploy <env>`, `pier rollback <env>`,
// and `pier status`. The package owns the root command, the global
// flags (--config, --json, --verbose), the typed error/exit-code
// contract that other internal packages hang errors off of, and the
// pre-flight helpers (port probe, merge-with-existing docker-compose)
// that the dev command runs before invoking `docker compose up`.
package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Version is the pier release string baked into the binary. Reported by
// `pier --version` and embedded in deploy output.
const Version = "0.0.4-beta"

var (
	cfgPath string
	jsonOut bool
	verbose bool
)

// NewRootCmd returns pier's cobra root command with all subcommands attached.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "pier",
		Short:         "Personal Laravel Docker dev + production CLI",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("pier {{.Version}}\n")

	root.PersistentFlags().StringVar(&cfgPath, "config", "pier.toml", "path to pier.toml")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit one JSON object per line per event")
	root.PersistentFlags().BoolVar(&verbose, "verbose", false, "unfiltered Docker build output")

	root.AddCommand(newInitCmd(stdout, stderr))
	root.AddCommand(newDevCmd(stdout, stderr))
	root.AddCommand(newStopCmd(stdout, stderr))
	root.AddCommand(newShellCmd(stdout, stderr))
	root.AddCommand(newExecCmd(stdout, stderr))
	root.AddCommand(newServiceCmd(stdout, stderr))
	root.AddCommand(newDeployCmd(stdout, stderr))
	root.AddCommand(newBootstrapCmd(stdout, stderr))
	root.AddCommand(newRollbackCmd(stdout, stderr))
	root.AddCommand(newStatusCmd(stdout, stderr))
	return root
}

// Execute runs the root command and returns the appropriate error/exit code.
func Execute() error {
	root := NewRootCmd(nil, nil) // overridden via SetOut in main.go
	return root.Execute()
}

// SetOut is a helper for tests and main.go to attach real writers.
func SetOut(root *cobra.Command, stdout, stderr io.Writer) {
	root.SetOut(stdout)
	root.SetErr(stderr)
}

// ExitCode returns the appropriate process exit code for err.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors_As(err, &ee) {
		return ee.Code
	}
	return ExitGeneral
}

// Verbose returns the current value of the --verbose flag, which is set
// during root command execution.  Used by main.go to decide whether
// PrintError should show the full error chain.
func Verbose() bool {
	return verbose
}

func errors_As(err error, target **ExitError) bool {
	for err != nil {
		if ee, ok := err.(*ExitError); ok {
			*target = ee
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// usage placeholder used by some commands during scaffolding.
var _ = fmt.Sprintf

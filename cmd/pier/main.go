// Command pier is the personal Laravel Docker dev + production CLI.
//
// See the README at the repo root for the full command list and a
// quickstart. The binary is wired up by main.go: it constructs the
// cobra root command from internal/cli, runs it, and translates
// the returned *cli.ExitError into a process exit code via
// cli.ExitCode. The blank import of internal/stack/laravel
// triggers the stack module's init() so `pier init` knows about
// it. The assets import pulls in the embedded pier logo so the
// binary is self-contained.
package main

import (
	"fmt"
	"os"

	"github.com/Bonnary/pier/assets"
	"github.com/Bonnary/pier/internal/cli"
	_ "github.com/Bonnary/pier/internal/stack/laravel"
)

// LogoBytes exposes the embedded application logo so other packages
// (or embedders) can render or serve it without re-importing
// assets.LogoPNG.
func LogoBytes() []byte { return assets.LogoPNG() }

// Version mirrors cli.Version so binaries that link this package
// (e.g. embedders) have a single importable release string.
const Version = cli.Version

func main() {
	root := cli.NewRootCmd(os.Stdout, os.Stderr)
	if err := root.Execute(); err != nil {
		color := cli.IsTerminal(os.Stderr) && os.Getenv("NO_COLOR") == ""
		cli.PrintError(os.Stderr, err, cli.Verbose(), color)
		fmt.Fprintln(os.Stderr)
		os.Exit(cli.ExitCode(err))
	}
}

package main

import (
	"fmt"
	"os"

	"github.com/pcnerd/pier/internal/cli"
	_ "github.com/pcnerd/pier/internal/stack/laravel"
)

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

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
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(cli.ExitCode(err))
	}
}

package deploy

import (
	"context"
	"os/exec"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

var defaultRunner CommandRunner = osRunner{}

var rsyncExcludes = []string{
	"--exclude=.git",
	"--exclude=node_modules",
	"--exclude=vendor",
	"--exclude=.env",
	"--exclude=.env.*",
	"--include=.env.production",
	"--exclude=storage/logs/*",
	"--exclude=.idea",
	"--exclude=.vscode",
	"--exclude=*.swp",
	"--exclude=.DS_Store",
}

func Sync(ctx context.Context, runner CommandRunner, local, remote string) error {
	args := []string{"-az", "-e", "ssh"}
	args = append(args, rsyncExcludes...)
	args = append(args, local+"/", remote+"/")
	return runner.Run(ctx, "rsync", args...)
}

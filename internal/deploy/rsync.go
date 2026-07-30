package deploy

import (
	"context"
	"os/exec"
)

// CommandRunner abstracts a single-shot command invocation. The real
// implementation is os/exec.CommandContext; tests can substitute a
// fake that captures the args without spawning a process.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

var defaultRunner CommandRunner = osRunner{}

// rsyncExcludes is the default set of files pier skips when syncing
// the project tree to the deploy host: version control, build
// artifacts, secrets, editor state, and macOS metadata. .env.production
// is allowed through; everything else starting with .env is dropped.
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

// Sync runs `rsync -az -e ssh` from local/ to remote/, applying the
// default exclude list. Used as stage 3 of the deploy pipeline
// (between render and build) to copy the rendered
// docker-compose.prod.yml and runtime files to the remote.
func Sync(ctx context.Context, runner CommandRunner, local, remote string) error {
	args := []string{"-az", "-e", "ssh"}
	args = append(args, rsyncExcludes...)
	args = append(args, local+"/", remote+"/")
	return runner.Run(ctx, "rsync", args...)
}

package cli

import (
	"context"
	"errors"
	"io"
	"os/user"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/docker"
)

func newShellCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Open an interactive bash in the laravel.test container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd)
		},
	}
}

func runShell(cmd *cobra.Command) error {
	if _, err := config.Load(cfgPath); err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	tty := docker.DetectTTY()
	user := shellUser()
	if err := ensureUp(cmd, c); err != nil {
		return err
	}
	return c.Exec(context.Background(), docker.ExecOpts{Service: "laravel.test", User: user, TTY: tty}, "bash")
}

func shellUser() string {
	u, err := user.Current()
	if err != nil {
		return "www-data"
	}
	if u.Uid == "0" {
		return "www-data"
	}
	return u.Uid
}

func ensureUp(cmd *cobra.Command, c *docker.Compose) error {
	ps, err := c.PS(context.Background())
	if err != nil {
		return err
	}
	if !containsString(string(ps), "laravel.test") {
		return ExecDownError()
	}
	_ = errors.New("")
	return nil
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

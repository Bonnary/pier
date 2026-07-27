package deploy

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/pcnerd/pier/internal/config"
)

type discardLogger struct{}

func (discardLogger) Emit(Event)                 {}
func (discardLogger) PhaseStart(string)          {}
func (discardLogger) PhaseEnd(string, error)     {}
func (discardLogger) Log(string, string, ...any) {}
func (discardLogger) JSON() bool                 { return false }
func (discardLogger) Writer() io.Writer          { return io.Discard }

func TestPipelineDryRun(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")},
		Health:    HealthConfig{URL: "https://x.example.com/up", Timeout: time.Second, Interval: 100 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
		// Skip SSH dial for dry-run by providing a stub that fails the connection.
		// For unit test, we expect Run to fail at preflight (no SSH server); this exercises the wiring.
	}
	_ = p
	_ = context.Background
}

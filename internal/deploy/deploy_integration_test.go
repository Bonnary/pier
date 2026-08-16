//go:build integration

package deploy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

// TestPipelineLocalMachineEndToEnd drives a full local_machine deploy
// against a real SSH host with a real docker daemon: the image is
// built locally, streamed into the remote daemon (docker load), and
// the app comes up. Requires docker locally and on the host.
//
// Run:
//
//	PIER_TEST_SSH_HOST=192.168.122.63 PIER_TEST_SSH_USER=deploy \
//	PIER_TEST_SSH_KEY=~/.ssh/id_ed25519 \
//	go test -tags=integration -run TestPipelineLocalMachineEndToEnd ./internal/deploy/
func TestPipelineLocalMachineEndToEnd(t *testing.T) {
	host := os.Getenv("PIER_TEST_SSH_HOST")
	if host == "" {
		t.Skip("PIER_TEST_SSH_HOST not set")
	}
	user := os.Getenv("PIER_TEST_SSH_USER")
	if user == "" {
		user = "deploy"
	}
	key := os.Getenv("PIER_TEST_SSH_KEY")
	if key == "" {
		key = filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
	}
	ctx := context.Background()

	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env.production", []byte("APP_KEY=base64:test\nAPP_DEBUG=false\nDB_PASSWORD=test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	remote := os.Getenv("PIER_TEST_DEPLOY_PATH")
	if remote == "" {
		remote = "/tmp/pier-it-" + time.Now().Format("150405")
	}

	dc := config.DeployConfig{
		Host: host, User: user, Path: remote, Branch: "main",
		Builder: "local_machine",
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "pierit"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": dc},
	}
	p := &Pipeline{
		Config: cfg, Env: "production", DeployEnv: dc,
		Logger: &stdTestLogger{t},
		SSH:    SSHConfig{Host: host, User: user, KeyPath: key},
		Health: HealthConfig{URL: "http://" + host + ":80/up", Timeout: 5 * time.Second, Interval: time.Second, MaxAttempts: 3},
		Now:    time.Now,
	}
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	client, err := Dial(ctx, SSHConfig{Host: host, User: user, KeyPath: key})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	images, _, err := client.Run(ctx, "docker images --format '{{.Repository}}:{{.Tag}}'")
	if err != nil {
		t.Fatalf("docker images: %v", err)
	}
	if !contains(string(images), "pierit:current") {
		t.Errorf("remote images missing pierit:current:\n%s", images)
	}
}

// stdTestLogger logs deploy events through t.Logf.
type stdTestLogger struct{ t *testing.T }

func (l *stdTestLogger) Emit(_ Event)        {}
func (l *stdTestLogger) PhaseStart(n string) { l.t.Logf("phase %s", n) }
func (l *stdTestLogger) PhaseEnd(n string, err error) {
	if err != nil {
		l.t.Logf("phase %s failed: %v", n, err)
		return
	}
	l.t.Logf("phase %s ok", n)
}
func (l *stdTestLogger) Log(_ string, format string, args ...any) { l.t.Logf(format, args...) }
func (l *stdTestLogger) JSON() bool                               { return false }
func (l *stdTestLogger) Writer() io.Writer                        { return io.Discard }

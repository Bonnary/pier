//go:build integration

package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestBootstrapRealServer provisions a real host when PIER_TEST_SSH_HOST
// is set. The provision path needs PIER_TEST_SUDO_PASSWORD (the deploy
// user's sudo password); without it the test runs the probe path only.
//
// Run:
//
//	PIER_TEST_SSH_HOST=192.168.122.63 PIER_TEST_SSH_USER=deploy \
//	PIER_TEST_SSH_KEY=~/.ssh/id_ed25519 PIER_TEST_SUDO_PASSWORD='...' \
//	go test -tags=integration -run TestBootstrapRealServer ./internal/deploy/
func TestBootstrapRealServer(t *testing.T) {
	host := os.Getenv("PIER_TEST_SSH_HOST")
	if host == "" {
		t.Skip("PIER_TEST_SSH_HOST not set")
	}
	user := os.Getenv("PIER_TEST_SSH_USER")
	if user == "" {
		user = "root"
	}
	key := os.Getenv("PIER_TEST_SSH_KEY")
	if key == "" {
		key = filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
	}
	cfg := SSHConfig{Host: host, User: user, KeyPath: key}
	ctx := context.Background()

	ok, err := ProbeEnv(ctx, cfg)
	if err != nil {
		t.Fatalf("ProbeEnv: %v", err)
	}
	t.Logf("probe before: bootstrapped=%v", ok)

	pw := os.Getenv("PIER_TEST_SUDO_PASSWORD")
	if pw == "" {
		t.Log("PIER_TEST_SUDO_PASSWORD not set; probe-only run")
		return
	}
	err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user})
	if err != nil && !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Fatalf("BootstrapEnv: %v", err)
	}
	err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user})
	if !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Fatalf("second BootstrapEnv = %v, want ErrAlreadyBootstrapped (idempotent)", err)
	}
	if err := BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Force: true}); err != nil {
		t.Fatalf("BootstrapEnv(force): %v", err)
	}
	ok, err = ProbeEnv(ctx, cfg)
	if err != nil {
		t.Fatalf("ProbeEnv after bootstrap: %v", err)
	}
	if !ok {
		t.Error("ProbeEnv after bootstrap = false, want true")
	}
}

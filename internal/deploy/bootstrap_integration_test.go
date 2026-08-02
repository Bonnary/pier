//go:build integration

package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	deployPath := os.Getenv("PIER_TEST_DEPLOY_PATH")
	if deployPath != "" && pw != "" {
		err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Path: deployPath})
		if err != nil && !errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatalf("BootstrapEnv: %v", err)
		}
		err = BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Path: deployPath})
		if !errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatalf("second BootstrapEnv = %v, want ErrAlreadyBootstrapped (idempotent)", err)
		}
		if err := BootstrapEnv(ctx, cfg, pw, BootstrapOpts{User: user, Force: true, Path: deployPath}); err != nil {
			t.Fatalf("BootstrapEnv(force): %v", err)
		}
		if err := assertRemotePathOwned(ctx, cfg, deployPath, user); err != nil {
			t.Fatalf("deploy path ownership: %v", err)
		}
		t.Logf("deploy path %s owned by %s", deployPath, user)
	}
	ok, err = ProbeEnv(ctx, cfg)
	if err != nil {
		t.Fatalf("ProbeEnv after bootstrap: %v", err)
	}
	if !ok {
		t.Error("ProbeEnv after bootstrap = false, want true")
	}
}

// assertRemotePathOwned dials host and asserts path exists and is
// owned by wantUser.
func assertRemotePathOwned(ctx context.Context, cfg SSHConfig, path, wantUser string) error {
	client, err := Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	_, _, err = client.Run(ctx, fmt.Sprintf(
		"[ -d %s ] && [ \"$(stat -c '%%U' %s)\" = %s ]",
		quoteShell(path), quoteShell(path), quoteShell(wantUser)))
	return err
}

// TestRunStreamStdinRealServer streams stdout/stderr lines from a
// real SSH session and returns captured stderr. Run with
// PIER_TEST_SSH_HOST (see TestBootstrapRealServer).
func TestRunStreamStdinRealServer(t *testing.T) {
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
	client, err := Dial(context.Background(), SSHConfig{Host: host, User: user, KeyPath: key})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	var stdoutLines, stderrLines []string
	stderr, err := client.RunStreamStdin(context.Background(),
		"printf 'a\\nb\\n'; printf 'x\\ny\\n' >&2", "ignored\n",
		func(l string) { stdoutLines = append(stdoutLines, l) },
		func(l string) { stderrLines = append(stderrLines, l) })
	if err != nil {
		t.Fatalf("RunStreamStdin: %v", err)
	}
	if !equalStr(stdoutLines, []string{"a", "b"}) {
		t.Errorf("stdout lines = %v, want [a b]", stdoutLines)
	}
	if !equalStr(stderrLines, []string{"x", "y"}) {
		t.Errorf("stderr lines = %v, want [x y]", stderrLines)
	}
	if string(stderr) != "x\ny\n" {
		t.Errorf("captured stderr = %q, want %q", stderr, "x\ny\n")
	}
}

// TestRunStreamRealServer streams stdout and stderr lines from a real
// SSH session and, on a non-zero exit, returns an error carrying the
// last streamed lines. Run with PIER_TEST_SSH_HOST (see
// TestBootstrapRealServer).
func TestRunStreamRealServer(t *testing.T) {
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
	client, err := Dial(context.Background(), SSHConfig{Host: host, User: user, KeyPath: key})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	var lines []string
	err = client.RunStream(context.Background(),
		"printf 'out\n'; printf 'err\n' >&2; exit 7",
		func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatal("RunStream(exit 7) = nil error, want non-nil")
	}
	var hasOut, hasErr bool
	for _, l := range lines {
		if l == "out" {
			hasOut = true
		}
		if l == "err" {
			hasErr = true
		}
	}
	if len(lines) != 2 || !hasOut || !hasErr {
		t.Errorf("streamed lines = %v, want both [out err] in any order", lines)
	}
	for _, want := range []string{"last output:", "out", "err"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

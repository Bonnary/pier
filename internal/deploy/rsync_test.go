package deploy

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCmd struct {
	calls []string
}

func (f *fakeCmd) Run(ctx context.Context, name string, args ...string) error {
	call := name
	for _, a := range args {
		call += " " + a
	}
	f.calls = append(f.calls, call)
	return nil
}

func TestSyncExcludes(t *testing.T) {
	// Use a fake local path; we don't actually run rsync, just assert the command line.
	dir := t.TempDir()
	runner := &fakeCmd{}
	if err := Sync(context.Background(), runner, dir, "user@host:/srv/app"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(runner.calls) == 0 {
		t.Fatal("no calls recorded")
	}
	call := runner.calls[0]
	for _, ex := range []string{"--exclude=.git", "--exclude=node_modules", "--exclude=vendor", "--exclude=.env"} {
		if !contains(call, ex) {
			t.Errorf("rsync missing exclude %s in: %s", ex, call)
		}
	}
}

func TestSyncLocalPath(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeCmd{}
	if err := Sync(context.Background(), runner, dir, "user@host:/srv/app"); err != nil {
		t.Fatal(err)
	}
	// First arg should be the local path.
	if !contains(runner.calls[0], dir) {
		t.Errorf("local path %s not in call: %s", dir, runner.calls[0])
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = filepath.Join

func TestOsRunnerCapturesOutputOnFailure(t *testing.T) {
	err := (osRunner{}).Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("osRunner.Run(failing) = nil error, want non-nil")
	}
	if !contains(err.Error(), "boom") {
		t.Errorf("err %q missing captured stderr", err.Error())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("err is %T, want *exec.ExitError in chain", err)
	} else if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3", exitErr.ExitCode())
	}
}

func TestOsRunnerSuccessNoError(t *testing.T) {
	if err := (osRunner{}).Run(context.Background(), "true"); err != nil {
		t.Fatalf("osRunner.Run(true): %v", err)
	}
}

func TestOsRunnerTrimsOutput(t *testing.T) {
	long := strings.Repeat("x", 8192)
	err := (osRunner{}).Run(context.Background(), "sh", "-c", "printf '%s' '"+long+"' >&2; exit 1")
	if err == nil {
		t.Fatal("osRunner.Run(long) = nil error, want non-nil")
	}
	if len(err.Error()) > 4096+64 {
		t.Errorf("error message length %d exceeds 4KB excerpt + margin", len(err.Error()))
	}
	if !strings.HasSuffix(err.Error(), "...") {
		t.Errorf("error %q missing truncation suffix", err.Error())
	}
}

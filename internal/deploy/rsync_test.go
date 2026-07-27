package deploy

import (
	"context"
	"path/filepath"
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

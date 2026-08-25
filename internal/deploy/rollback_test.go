package deploy

import (
	"context"
	"path/filepath"
	"testing"
)

type fakeRollbackRunner struct {
	cmds  []string
	lines []string
}

func (f *fakeRollbackRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	return nil, nil, nil
}

func (f *fakeRollbackRunner) RunStream(ctx context.Context, cmd string, onLine func(string)) error {
	f.cmds = append(f.cmds, cmd)
	for _, l := range f.lines {
		onLine(l)
	}
	return nil
}

func TestRollbackNoPrevious(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRollbackRunner{}
	if err := Rollback(context.Background(), nil, r, dir, "myapp", "current"); err == nil {
		t.Error("Rollback(no previous) = nil error, want non-nil")
	}
}

func TestRollbackSwitchesToPrevious(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Current: "new", Previous: "old", DeployedAt: "2026-07-27T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRollbackRunner{}
	if err := Rollback(context.Background(), localStateStore{}, r, dir, "myapp", "current"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	found := false
	for _, c := range r.cmds {
		if contains(c, "old") {
			found = true
		}
	}
	if !found {
		t.Errorf("rollback did not reference previous tag: %v", r.cmds)
	}
	_ = filepath.Join
}

// TestRollbackTargetsHostServerTag asserts the host_server compose
// variant references <project>:latest, so the rollback retag must
// repoint :latest (not :current) at the previous image — otherwise
// rollback is a silent no-op that keeps serving the broken release.
func TestRollbackTargetsHostServerTag(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, &State{Current: "new", Previous: "old", DeployedAt: "2026-07-27T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRollbackRunner{}
	if err := Rollback(context.Background(), localStateStore{}, r, dir, "myapp", "latest"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	found := false
	for _, c := range r.cmds {
		if contains(c, "docker tag myapp:old myapp:latest") {
			found = true
		}
	}
	if !found {
		t.Errorf("rollback did not retag the host_server tag (:latest): %v", r.cmds)
	}
}

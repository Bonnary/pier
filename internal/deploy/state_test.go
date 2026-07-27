package deploy

import (
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &State{Current: "abc", Previous: "def", DeployedAt: "2026-07-27T10:00:00Z", DeployedBy: "user@host"}
	if err := SaveState(dir, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Current != "abc" || got.Previous != "def" {
		t.Errorf("got %+v", got)
	}
}

func TestStateHasPrevious(t *testing.T) {
	s := &State{Current: "abc"}
	if s.HasPrevious() {
		t.Error("HasPrevious() = true, want false")
	}
	s.Previous = "def"
	if !s.HasPrevious() {
		t.Error("HasPrevious() = false, want true")
	}
}

func TestStateLoadMissing(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState(missing) = %v, want nil (first deploy)", err)
	}
	if s != nil {
		t.Errorf("LoadState(missing) = %+v, want nil", s)
	}
}

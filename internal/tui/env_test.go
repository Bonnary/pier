package tui

import "testing"

func TestPickEnvEmpty(t *testing.T) {
	idx, err := PickEnv(nil, 0)
	if err != nil {
		t.Fatalf("PickEnv(nil) = %v, want nil", err)
	}
	if idx != -1 {
		t.Errorf("PickEnv(nil) index = %d, want -1", idx)
	}
}

func TestPickEnvBuildsSinglePicker(t *testing.T) {
	// Contract lock: constructing the picker must not panic. Run() must
	// stay out of unit tests: on a real console it reads stdin and
	// blocks forever (the full Run path is covered by the CLI seam, see
	// cli/bootstrap_test.go).
	p := NewSinglePicker("Env to bootstrap", []string{"stage (s.example.com)", "production (p.example.com)"}, 1)
	if p == nil {
		t.Fatal("NewSinglePicker must return a picker")
	}
}

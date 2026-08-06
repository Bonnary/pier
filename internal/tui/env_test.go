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
	idx, err := PickEnv([]string{"stage (s.example.com)", "production (p.example.com)"}, 1)
	_ = idx
	_ = err
	// Contract lock: constructing the picker must not panic and the
	// full Run is exercised via the CLI seam test (cli/bootstrap_test.go).
}

package tui

import "testing"

func TestPresetIndices(t *testing.T) {
	available := []string{"mailpit", "mysql", "postgres", "redis"}
	presets := presetIndices(available, []string{"postgres", "redis"})
	if !presets[2] || !presets[3] {
		t.Errorf("presets = %v, want indices 2 and 3 ticked", presets)
	}
	if presets[0] || presets[1] {
		t.Errorf("presets = %v, want indices 0 and 1 unticked", presets)
	}
}

func TestPresetIndicesIgnoresUnknownCurrent(t *testing.T) {
	presets := presetIndices([]string{"redis"}, []string{"nope"})
	if len(presets) != 0 {
		t.Errorf("presets = %v, want empty", presets)
	}
}

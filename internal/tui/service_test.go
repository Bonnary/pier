package tui

import (
	"sort"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg2(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// These tests directly drive the underlying Picker through newAddPicker /
// newRemovePicker (the constructors) so we can assert the exact item set
// the user is shown — without rendering a real terminal.

func TestNewAddPickerFiltersInstalled(t *testing.T) {
	available := []string{"mysql", "postgres", "redis"}
	installed := []string{"redis"}
	p := newAddPicker(available, installed)
	got := p.items
	want := []string{"mysql", "postgres"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("items[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewRemovePickerShowsOnlyInstalled(t *testing.T) {
	installed := []string{"mailpit", "redis"}
	p := newRemovePicker(installed)
	got := p.items
	sort.Strings(got)
	want := []string{"mailpit", "redis"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("items[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewRemovePickerEmpty(t *testing.T) {
	p := newRemovePicker(nil)
	if len(p.items) != 0 {
		t.Errorf("items = %v, want []", p.items)
	}
}

func TestNewAddPickerAllInstalled(t *testing.T) {
	available := []string{"redis"}
	installed := []string{"redis"}
	p := newAddPicker(available, installed)
	if len(p.items) != 0 {
		t.Errorf("items = %v, want [] (everything already installed)", p.items)
	}
}

func TestAddPickerEnterWithSpaceToggle(t *testing.T) {
	// Drive the picker state machine: toggle index 0, then enter.
	p := newAddPicker([]string{"a", "b"}, nil)
	upd, _ := p.Update(keyMsg2(" "))
	upd, _ = upd.(*Picker).Update(keyMsg2("enter"))
	got := upd.(*Picker)
	if !got.done {
		t.Error("done = false, want true")
	}
	if !got.picked[0] {
		t.Error("picked[0] = false, want true")
	}
}

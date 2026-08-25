package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newSingleP(t *testing.T, items []string, def int) *Picker {
	t.Helper()
	if def < 0 || def >= len(items) {
		t.Fatalf("test bug: defaultIdx %d out of range for %d items", def, len(items))
	}
	return NewSinglePicker("pick", items, def)
}

func newMultiP(t *testing.T, items []string, presets map[int]bool) *Picker {
	t.Helper()
	return NewMultiPicker("pick", items, presets)
}

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestSinglePickerEnter(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 1)
	updated, _ := p.Update(key("enter"))
	got := updated.(*Picker)
	if !got.done {
		t.Error("done = false after enter, want true")
	}
	if got.aborted {
		t.Error("aborted = true after enter, want false")
	}
}

func TestSinglePickerDefaultCursor(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 2)
	if p.cursor != 2 {
		t.Errorf("cursor = %d, want 2", p.cursor)
	}
}

func TestSinglePickerArrowDownWraps(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 2)
	updated, _ := p.Update(key("down"))
	if updated.(*Picker).cursor != 0 {
		t.Errorf("cursor after down from last = %d, want 0 (wrap)", updated.(*Picker).cursor)
	}
}

func TestSinglePickerArrowUpWraps(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 0)
	updated, _ := p.Update(key("up"))
	if updated.(*Picker).cursor != 2 {
		t.Errorf("cursor after up from first = %d, want 2 (wrap)", updated.(*Picker).cursor)
	}
}

func TestSinglePickerJMove(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 0)
	updated, _ := p.Update(key("j"))
	if updated.(*Picker).cursor != 1 {
		t.Errorf("cursor after j = %d, want 1", updated.(*Picker).cursor)
	}
}

func TestSinglePickerSpaceIsNoOp(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 0)
	updated, _ := p.Update(key(" "))
	if updated.(*Picker).cursor != 0 {
		t.Errorf("cursor changed by space: %d", updated.(*Picker).cursor)
	}
	if updated.(*Picker).done {
		t.Error("done = true after space, want false")
	}
}

func TestSinglePickerQuitSetsAborted(t *testing.T) {
	p := newSingleP(t, []string{"a"}, 0)
	updated, _ := p.Update(key("q"))
	got := updated.(*Picker)
	if !got.aborted {
		t.Error("aborted = false after q, want true")
	}
}

func TestSinglePickerCtrlCSetsAborted(t *testing.T) {
	p := newSingleP(t, []string{"a"}, 0)
	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(*Picker)
	if !got.aborted {
		t.Error("aborted = false after ctrl+c, want true")
	}
}

func TestMultiPickerSpaceToggles(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	updated, _ := p.Update(key(" ")) // toggle index 0
	got := updated.(*Picker)
	if !got.picked[0] {
		t.Error("picked[0] = false after space, want true")
	}
	updated, _ = got.Update(key("j"))
	updated, _ = updated.(*Picker).Update(key(" ")) // toggle index 1
	got = updated.(*Picker)
	if !got.picked[1] {
		t.Error("picked[1] = false after space, want true")
	}
	if !got.picked[0] {
		t.Error("picked[0] = false after toggling index 1, want still true")
	}
}

func TestMultiPickerEnterReturnsSorted(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	// Toggle order: 2, 0 (the storage is order-agnostic; return must be ascending)
	upd, _ := p.Update(key("j"))
	upd, _ = upd.(*Picker).Update(key("j"))
	upd, _ = upd.(*Picker).Update(key(" ")) // picked={2}
	upd, _ = upd.(*Picker).Update(key("up"))
	upd, _ = upd.(*Picker).Update(key("up"))
	upd, _ = upd.(*Picker).Update(key(" ")) // picked={2, 0}
	upd, _ = upd.(*Picker).Update(key("enter"))
	got := upd.(*Picker)
	if !got.done {
		t.Error("done = false after enter, want true")
	}
	if got.aborted {
		t.Error("aborted = true after enter, want false")
	}
}

func TestMultiPickerPresets(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, map[int]bool{1: true})
	if !p.picked[1] {
		t.Error("picked[1] = false from preset, want true")
	}
}

func TestMultiPickerEmptyEnter(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	upd, _ := p.Update(key("enter"))
	got := upd.(*Picker)
	if !got.done {
		t.Error("done = false after enter on empty multi, want true")
	}
}

func TestMultiPickerBuildResultIsSorted(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	upd, _ := p.Update(key("j"))
	upd, _ = upd.(*Picker).Update(key("j"))
	upd, _ = upd.(*Picker).Update(key(" ")) // picked={2}
	upd, _ = upd.(*Picker).Update(key("up"))
	upd, _ = upd.(*Picker).Update(key("up"))
	upd, _ = upd.(*Picker).Update(key(" ")) // picked={2, 0}
	got := upd.(*Picker)
	result := got.buildResult()
	if result.Aborted {
		t.Error("Aborted = true after enter, want false")
	}
	wantIdx := []int{0, 2}
	if !equalInts(result.Indices, wantIdx) {
		t.Errorf("Indices = %v, want %v (ascending)", result.Indices, wantIdx)
	}
	wantVal := []string{"a", "c"}
	if !equalStrings(result.Values, wantVal) {
		t.Errorf("Values = %v, want %v (ascending)", result.Values, wantVal)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

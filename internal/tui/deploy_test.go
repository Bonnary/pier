package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelUpdate(t *testing.T) {
	m := model{phases: []phase{{Name: "preflight"}}}
	updated, _ := m.Update(logMsg("hello"))
	mm := updated.(model)
	if len(mm.logs) != 1 || mm.logs[0] != "hello" {
		t.Errorf("logs = %v", mm.logs)
	}
}

func TestModelQuitOnQ(t *testing.T) {
	m := model{}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Error("expected tea.Quit cmd on q")
	}
	_ = time.Now
}

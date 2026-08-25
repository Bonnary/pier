package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
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

func TestRunPhaseListIncludesTransferInImageModes(t *testing.T) {
	for _, b := range []string{"local_machine", "build_server"} {
		p := &deploy.Pipeline{DeployEnv: config.DeployConfig{Builder: b}}
		phases := deployPhases(p)
		got := []string{}
		for _, ph := range phases {
			got = append(got, ph.Name)
		}
		found := false
		for i, name := range got {
			if name == "transfer" {
				found = true
				if i == 0 || got[i-1] != "build" {
					t.Errorf("%s: transfer not right after build: %v", b, got)
				}
			}
		}
		if !found {
			t.Errorf("%s: phase list %v missing transfer", b, got)
		}
	}
}

func TestRunPhaseListOmitsTransferInHostMode(t *testing.T) {
	p := &deploy.Pipeline{DeployEnv: config.DeployConfig{}}
	for _, ph := range deployPhases(p) {
		if ph.Name == "transfer" {
			t.Fatal("host_server phase list must not include transfer")
		}
	}
}

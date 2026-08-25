package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestInitModelFlowHappyPath(t *testing.T) {
	m := newInitModel(
		[]string{"8.2", "8.3", "8.4", "8.5"},
		[]string{"20", "22"},
		[]string{"redis", "postgres"},
		[]string{"host_server", "local_machine", "build_server"},
	)
	// Default cursor on latest for PHP and Node
	if m.phpPicker.cursor != 3 {
		t.Errorf("phpPicker.cursor = %d, want 3 (latest 8.5)", m.phpPicker.cursor)
	}
	if m.nodePicker.cursor != 1 {
		t.Errorf("nodePicker.cursor = %d, want 1 (latest 22)", m.nodePicker.cursor)
	}
	// Step through: enter on PHP, enter on Node, toggle one service, enter
	upd, _ := m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateNode {
		t.Errorf("after enter on PHP: state = %d, want %d (stateNode)", m.state, stateNode)
	}
	if m.result.PHP != "8.5" {
		t.Errorf("result.PHP = %q, want 8.5", m.result.PHP)
	}
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateServices {
		t.Errorf("after enter on Node: state = %d, want %d (stateServices)", m.state, stateServices)
	}
	if m.result.Node != "22" {
		t.Errorf("result.Node = %q, want 22", m.result.Node)
	}
	upd, _ = m.Update(keyMsg(" ")) // toggle redis
	m = upd.(initModel)
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateBuilder {
		t.Errorf("after enter on services: state = %d, want %d (stateBuilder)", m.state, stateBuilder)
	}
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateDone {
		t.Errorf("after enter on builder: state = %d, want %d (stateDone)", m.state, stateDone)
	}
	if m.result.Builder != "host_server" {
		t.Errorf("result.Builder = %q, want host_server", m.result.Builder)
	}
	//nolint:staticcheck // brief: deliberate no-op sanity check (makes reader pause on invariant)
	if !m.result.Aborted == false {
		// sanity
	}
	if m.result.Aborted {
		t.Error("result.Aborted = true, want false")
	}
	if len(m.result.Services) != 1 || m.result.Services[0] != "redis" {
		t.Errorf("result.Services = %v, want [redis]", m.result.Services)
	}
}

func TestInitModelAbortOnPHP(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, []string{"host_server", "local_machine", "build_server"})
	upd, _ := m.Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q, want true")
	}
}

func TestInitModelAbortOnNode(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, []string{"host_server", "local_machine", "build_server"})
	upd, _ := m.Update(keyMsg("enter")) // -> stateNode
	upd, _ = upd.(initModel).Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q on Node, want true")
	}
	if got.result.PHP != "8.3" {
		t.Errorf("result.PHP = %q after abort on Node, want 8.3 (carried from prior step)", got.result.PHP)
	}
}

func TestInitModelAbortOnServices(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, []string{"host_server", "local_machine", "build_server"})
	upd, _ := m.Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q on services, want true")
	}
	if got.result.Node != "22" {
		t.Errorf("result.Node = %q, want 22", got.result.Node)
	}
}

func TestInitModelEmptyServicesOK(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis", "postgres"}, []string{"host_server", "local_machine", "build_server"})
	upd, _ := m.Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("enter")) // services confirm
	upd, _ = upd.(initModel).Update(keyMsg("enter")) // builder confirm
	got := upd.(initModel)
	if got.state != stateDone {
		t.Errorf("state = %d, want stateDone", got.state)
	}
	if len(got.result.Services) != 0 {
		t.Errorf("Services = %v, want []", got.result.Services)
	}
}

func TestInitModelBuilderStateStoresChoice(t *testing.T) {
	builders := []string{"host_server", "local_machine", "build_server"}
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, builders)
	if m.builderPicker.cursor != 0 {
		t.Errorf("builderPicker.cursor = %d, want 0 (host_server default)", m.builderPicker.cursor)
	}
	// Step through PHP, Node, services into the builder state.
	for i := 0; i < 3; i++ {
		upd, _ := m.Update(keyMsg("enter"))
		m = upd.(initModel)
	}
	if m.state != stateBuilder {
		t.Fatalf("state = %d, want %d (stateBuilder)", m.state, stateBuilder)
	}
	upd, _ := m.Update(keyMsg("j")) // down to local_machine
	m = upd.(initModel)
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateDone {
		t.Errorf("state = %d, want %d (stateDone)", m.state, stateDone)
	}
	if m.result.Builder != "local_machine" {
		t.Errorf("result.Builder = %q, want local_machine", m.result.Builder)
	}
}

func TestInitModelAbortOnBuilder(t *testing.T) {
	builders := []string{"host_server", "local_machine", "build_server"}
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, builders)
	for i := 0; i < 3; i++ {
		upd, _ := m.Update(keyMsg("enter"))
		m = upd.(initModel)
	}
	upd, _ := m.Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q on builder, want true")
	}
	if got.result.Node != "22" {
		t.Errorf("result.Node = %q after abort on builder, want 22 (carried from prior step)", got.result.Node)
	}
}

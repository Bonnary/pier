package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type initState int

const (
	statePHP initState = iota
	stateNode
	stateServices
	stateDone
)

type InitResult struct {
	PHP      string
	Node     string
	Services []string
	Aborted  bool
}

type initModel struct {
	state      initState
	phpPicker  *Picker
	nodePicker *Picker
	svcPicker  *Picker
	result     InitResult
}

func newInitModel(phpVersions, nodeVersions, services []string) initModel {
	return initModel{
		state:      statePHP,
		phpPicker:  NewSinglePicker("PHP version", phpVersions, len(phpVersions)-1),
		nodePicker: NewSinglePicker("Node version", nodeVersions, len(nodeVersions)-1),
		svcPicker:  NewMultiPicker("Services (space to toggle)", services, nil),
	}
}

func (m initModel) Init() tea.Cmd { return nil }

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if km.String() == "ctrl+c" || km.String() == "q" {
		m.result.Aborted = true
		m.state = stateDone
		return m, tea.Quit
	}
	if km.String() != "enter" {
		// forward to the current picker for navigation
		switch m.state {
		case statePHP:
			u, _ := m.phpPicker.Update(msg)
			m.phpPicker = u.(*Picker)
		case stateNode:
			u, _ := m.nodePicker.Update(msg)
			m.nodePicker = u.(*Picker)
		case stateServices:
			u, _ := m.svcPicker.Update(msg)
			m.svcPicker = u.(*Picker)
		}
		return m, nil
	}
	// enter: advance the state machine
	switch m.state {
	case statePHP:
		m.result.PHP = m.phpPicker.items[m.phpPicker.cursor]
		m.state = stateNode
	case stateNode:
		m.result.Node = m.nodePicker.items[m.nodePicker.cursor]
		m.state = stateServices
	case stateServices:
		res, _ := m.svcPicker.Run() // should already be done; but be safe
		_ = res
		// re-derive from the picker state directly (Run is what would produce this)
		var picked []string
		for i, on := range m.svcPicker.picked {
			if on {
				picked = append(picked, m.svcPicker.items[i])
			}
		}
		m.result.Services = picked
		m.state = stateDone
		return m, tea.Quit
	}
	return m, nil
}

func (m initModel) View() string {
	switch m.state {
	case statePHP:
		return m.phpPicker.View()
	case stateNode:
		return m.nodePicker.View()
	case stateServices:
		return m.svcPicker.View()
	case stateDone:
		return ""
	}
	return ""
}

func RunInit(phpVersions, nodeVersions, services []string) (InitResult, error) {
	m := newInitModel(phpVersions, nodeVersions, services)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return InitResult{}, err
	}
	got := final.(initModel)
	return got.result, nil
}

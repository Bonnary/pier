package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Result struct {
	Indices []int
	Values  []string
	Aborted bool
}

type Picker struct {
	title   string
	items   []string
	cursor  int
	picked  map[int]bool
	multi   bool
	done    bool
	aborted bool
}

func NewSinglePicker(title string, items []string, defaultIdx int) *Picker {
	if defaultIdx < 0 {
		defaultIdx = 0
	}
	if defaultIdx >= len(items) {
		defaultIdx = len(items) - 1
	}
	return &Picker{title: title, items: items, cursor: defaultIdx}
}

func NewMultiPicker(title string, items []string, presets map[int]bool) *Picker {
	picked := make(map[int]bool, len(presets))
	for k, v := range presets {
		picked[k] = v
	}
	return &Picker{title: title, items: items, picked: picked, multi: true}
}

func (p *Picker) Init() tea.Cmd { return nil }

func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch km.String() {
	case "ctrl+c", "q":
		p.aborted = true
		p.done = true
		return p, tea.Quit
	case "up", "k":
		if len(p.items) == 0 {
			return p, nil
		}
		p.cursor--
		if p.cursor < 0 {
			p.cursor = len(p.items) - 1
		}
		return p, nil
	case "down", "j":
		if len(p.items) == 0 {
			return p, nil
		}
		p.cursor++
		if p.cursor >= len(p.items) {
			p.cursor = 0
		}
		return p, nil
	case " ":
		if p.multi {
			p.picked[p.cursor] = !p.picked[p.cursor]
		}
		return p, nil
	case "enter":
		p.done = true
		return p, tea.Quit
	}
	return p, nil
}

func (p *Picker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(p.title))
	b.WriteString("\n")
	if len(p.items) == 0 {
		b.WriteString(helpStyle.Render("(no items)"))
		b.WriteString("\n")
		return b.String()
	}
	for i, item := range p.items {
		row := "  " + item
		if p.multi {
			marker := "[ ]"
			if p.picked[i] {
				marker = "[x]"
			}
			row = "  " + marker + " " + item
		}
		if i == p.cursor {
			b.WriteString(activeStyle.Render("> " + strings.TrimPrefix(row, "  ")))
		} else {
			if p.multi && p.picked[i] {
				b.WriteString(selectedStyle.Render(row))
			} else {
				b.WriteString(row)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if p.multi {
		b.WriteString(helpStyle.Render("(space to toggle, enter to confirm, q to abort)"))
	} else {
		b.WriteString(helpStyle.Render("(↑/↓ to choose, enter to confirm, q to abort)"))
	}
	b.WriteString("\n")
	return b.String()
}

func (p *Picker) Run() (Result, error) {
	final, err := tea.NewProgram(p).Run()
	if err != nil {
		return Result{}, err
	}
	pp := final.(*Picker)
	if pp.aborted {
		return Result{Aborted: true}, nil
	}
	if pp.multi {
		indices := make([]int, 0, len(pp.picked))
		for i, on := range pp.picked {
			if on {
				indices = append(indices, i)
			}
		}
		// sort ascending for deterministic CLI behavior
		sortInts(indices)
		values := make([]string, len(indices))
		for i, idx := range indices {
			values[i] = pp.items[idx]
		}
		return Result{Indices: indices, Values: values}, nil
	}
	return Result{
		Indices: []int{pp.cursor},
		Values:  []string{pp.items[pp.cursor]},
	}, nil
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

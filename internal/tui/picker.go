package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Result is what a Picker Run returns: the chosen indices and
// values, plus an Aborted flag for "the user hit q / Ctrl+C".
type Result struct {
	Indices []int
	Values  []string
	Aborted bool
}

// Picker is a single- or multi-select list backed by Bubble Tea.
// Construct with NewSinglePicker or NewMultiPicker; the public
// fields are unexported and are not part of the API.
type Picker struct {
	title   string
	items   []string
	cursor  int
	picked  map[int]bool
	multi   bool
	done    bool
	aborted bool
}

// NewSinglePicker returns a single-select Picker with the given
// title, items, and default cursor position. defaultIdx is
// clamped to [0, len(items)-1].
func NewSinglePicker(title string, items []string, defaultIdx int) *Picker {
	if defaultIdx < 0 {
		defaultIdx = 0
	}
	if defaultIdx >= len(items) {
		defaultIdx = len(items) - 1
	}
	return &Picker{title: title, items: items, cursor: defaultIdx}
}

// NewMultiPicker returns a multi-select Picker. presets maps item
// indices that should be checked at start (typically nil for
// add-pickers, the current install set for remove-pickers).
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

// Run drives the Picker via Bubble Tea and returns the result or
// a Bubble Tea error. Aborts (q / Ctrl+C) surface as Result.Aborted
// with a nil error.
func (p *Picker) Run() (Result, error) {
	final, err := tea.NewProgram(p).Run()
	if err != nil {
		return Result{}, err
	}
	pp := final.(*Picker)
	return pp.buildResult(), nil
}

func (p *Picker) buildResult() Result {
	if p.aborted {
		return Result{Aborted: true}
	}
	if p.multi {
		indices := make([]int, 0, len(p.picked))
		for i, on := range p.picked {
			if on {
				indices = append(indices, i)
			}
		}
		// sort ascending for deterministic CLI behavior
		sortInts(indices)
		values := make([]string, len(indices))
		for i, idx := range indices {
			values[i] = p.items[idx]
		}
		return Result{Indices: indices, Values: values}
	}
	return Result{
		Indices: []int{p.cursor},
		Values:  []string{p.items[p.cursor]},
	}
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

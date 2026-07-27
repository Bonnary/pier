package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pcnerd/pier/internal/deploy"
)

type phase struct {
	Name   string
	Status string
	Start  time.Time
	End    time.Time
}

type logMsg string

type pipelineDoneMsg struct{}

type model struct {
	pipeline *deploy.Pipeline
	phases   []phase
	logs     []string
	done     bool
	err      error
	ch       chan tea.Msg
}

func ShouldRun() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func Run(p *deploy.Pipeline) error {
	phases := []phase{
		{Name: "preflight"}, {Name: "render"}, {Name: "sync"},
		{Name: "build"}, {Name: "up"}, {Name: "health"}, {Name: "commit"},
	}
	ch := make(chan tea.Msg, 100)
	m := model{pipeline: p, phases: phases, ch: ch}
	p.Logger = tuiLogger{ch: ch}
	go func() {
		_ = p.Run(context.Background())
		close(ch)
	}()
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(model); ok {
		return fm.err
	}
	return nil
}

func (m model) Init() tea.Cmd {
	return waitMsg(m.ch)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			m.done = true
			return m, tea.Quit
		}
	case pipelineDoneMsg:
		m.done = true
		return m, tea.Quit
	case logMsg:
		m.logs = append(m.logs, string(msg))
		if len(m.logs) > 30 {
			m.logs = m.logs[len(m.logs)-30:]
		}
		return m, waitMsg(m.ch)
	}
	return m, nil
}

func waitMsg(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		v, ok := <-ch
		if !ok {
			return pipelineDoneMsg{}
		}
		return v
	}
}

func (m model) View() string {
	var s string
	s += titleStyle.Render("pier deploy") + "\n\n"
	for _, p := range m.phases {
		icon := "•"
		style := pendingStyle
		switch p.Status {
		case "active":
			icon = "▶"
			style = activeStyle
		case "ok":
			icon = "✓"
			style = okStyle
		case "error":
			icon = "✗"
			style = errorStyle
		}
		s += fmt.Sprintf("%s %s\n", style.Render(icon), p.Name)
	}
	s += "\n" + logBoxStyle.Render(joinLines(m.logs, 15)) + "\n"
	if m.done {
		s += "\n(q to quit)\n"
	}
	return s
}

func joinLines(lines []string, max int) string {
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

type tuiLogger struct {
	ch chan tea.Msg
}

func (l tuiLogger) Emit(_ deploy.Event) {}

func (l tuiLogger) PhaseStart(name string) {
	l.ch <- logMsg(fmt.Sprintf("▶ %s", name))
}

func (l tuiLogger) PhaseEnd(name string, err error) {
	if err != nil {
		l.ch <- logMsg(fmt.Sprintf("✗ %s: %v", name, err))
		return
	}
	l.ch <- logMsg(fmt.Sprintf("✓ %s", name))
}

func (l tuiLogger) Log(level, format string, args ...any) {
	l.ch <- logMsg(fmt.Sprintf("  %s", fmt.Sprintf(format, args...)))
}

func (l tuiLogger) JSON() bool { return false }

func (l tuiLogger) Writer() io.Writer { return os.Stdout }

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	phaseStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

type Event struct {
	Time    time.Time      `json:"time"`
	Phase   string         `json:"phase,omitempty"`
	Level   string         `json:"level,omitempty"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type Logger interface {
	Emit(Event)
	PhaseStart(name string)
	PhaseEnd(name string, err error)
	Log(level, format string, args ...any)
	JSON() bool
	Writer() io.Writer
}

type stdLogger struct {
	mu   sync.Mutex
	w    io.Writer
	json bool
	tty  bool
}

func NewLogger(jsonOut bool, w io.Writer) Logger {
	return &stdLogger{w: w, json: jsonOut, tty: !jsonOut}
}

type fileWriter struct{}

func (l *stdLogger) Writer() io.Writer { return l.w }
func (l *stdLogger) JSON() bool        { return l.json }

func (l *stdLogger) Emit(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.json {
		b, _ := json.Marshal(e)
		fmt.Fprintln(l.w, string(b))
		return
	}
	ts := e.Time.Format("15:04:05")
	if e.Phase != "" {
		fmt.Fprintf(l.w, "%s %s %s\n", ts, phaseStyle.Render(e.Phase), e.Message)
		return
	}
	level := e.Level
	if level == "" {
		level = "info"
	}
	fmt.Fprintf(l.w, "%s %s %s\n", ts, level, e.Message)
}

func (l *stdLogger) PhaseStart(name string) {
	l.Emit(Event{Phase: name, Message: "start"})
}

func (l *stdLogger) PhaseEnd(name string, err error) {
	msg := "ok"
	if err != nil {
		msg = "failed: " + err.Error()
	}
	l.Emit(Event{Phase: name, Message: msg, Level: ternary(err == nil, "info", "error")})
}

func (l *stdLogger) Log(level, format string, args ...any) {
	l.Emit(Event{Level: level, Message: fmt.Sprintf(format, args...)})
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

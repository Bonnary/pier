package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/pcnerd/pier/internal/deploy"
)

var (
	phaseStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
)

type stdLogger struct {
	mu   sync.Mutex
	w    io.Writer
	json bool
	tty  bool
}

func NewLogger(jsonOut bool, w io.Writer) deploy.Logger {
	return &stdLogger{w: w, json: jsonOut, tty: !jsonOut}
}

func (l *stdLogger) Writer() io.Writer { return l.w }
func (l *stdLogger) JSON() bool        { return l.json }

func (l *stdLogger) Emit(e deploy.Event) {
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
	l.Emit(deploy.Event{Phase: name, Message: "start"})
}

func (l *stdLogger) PhaseEnd(name string, err error) {
	msg := "ok"
	if err != nil {
		msg = "failed: " + err.Error()
	}
	l.Emit(deploy.Event{Phase: name, Message: msg, Level: ternary(err == nil, "info", "error")})
}

func (l *stdLogger) Log(level, format string, args ...any) {
	l.Emit(deploy.Event{Level: level, Message: fmt.Sprintf(format, args...)})
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

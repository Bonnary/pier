package deploy

import (
	"io"
	"time"
)

// Event is a single structured log record. The CLI's stdLogger
// marshals this to JSON when --json is set; the TUI logger ignores
// it. Time is filled in by the receiver if zero.
type Event struct {
	Time    time.Time      `json:"time"`
	Phase   string         `json:"phase,omitempty"`
	Level   string         `json:"level,omitempty"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// Logger is the contract every deploy event sink implements. The
// stdLogger (CLI) emits plain lines or JSON; the TUI logger pushes
// events into the Bubble Tea channel. The Pipeline writes to
// whichever Logger the caller hands it.
type Logger interface {
	Emit(Event)
	PhaseStart(name string)
	PhaseEnd(name string, err error)
	Log(level, format string, args ...any)
	JSON() bool
	Writer() io.Writer
}

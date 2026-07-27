package deploy

import (
	"io"
	"time"
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

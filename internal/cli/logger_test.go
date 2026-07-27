package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLoggerHuman(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(false, &buf)
	l.PhaseStart("preflight")
	l.Log("info", "connecting to %s", "host")
	l.PhaseEnd("preflight", nil)
	out := buf.String()
	if !strings.Contains(out, "preflight") {
		t.Errorf("output missing phase name: %q", out)
	}
	if !strings.Contains(out, "connecting to host") {
		t.Errorf("output missing log: %q", out)
	}
}

func TestLoggerJSON(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(true, &buf)
	l.PhaseStart("preflight")
	l.PhaseEnd("preflight", nil)
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("invalid JSON line %q: %v", line, err)
			continue
		}
		if ev["phase"] != "preflight" {
			t.Errorf("phase = %v, want preflight", ev["phase"])
		}
	}
}

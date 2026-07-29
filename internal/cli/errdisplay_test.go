package cli

import (
	"bytes"
	"testing"
)

func TestAnsiColorDisabled(t *testing.T) {
	got := ansiColor(false, 31, "boom")
	if got != "boom" {
		t.Errorf("ansiColor(false) = %q, want %q", got, "boom")
	}
}

func TestAnsiColorEnabled(t *testing.T) {
	got := ansiColor(true, 31, "boom")
	want := "\x1b[31mboom\x1b[0m"
	if got != want {
		t.Errorf("ansiColor(true, 31, ...) = %q, want %q", got, want)
	}
}

func TestIsTerminalBuffer(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Errorf("IsTerminal(bytes.Buffer) = true, want false")
	}
}

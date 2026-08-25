package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelp(t *testing.T) {
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"init", "dev", "stop", "shell", "exec", "service", "deploy", "rollback", "status"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestRootUnknownCommand(t *testing.T) {
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"nonexistent"})
	root.SilenceErrors = true
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute = nil error, want non-nil")
	}
}

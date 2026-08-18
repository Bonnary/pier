package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"pier", "--version"}

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	main()

	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "pier 0.0.7-beta") {
		t.Errorf("expected version output, got: %q", buf.String())
	}
}

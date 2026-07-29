package cli

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsTerminal returns true if w is one of the standard streams AND that
// stream is attached to a terminal.  For any other writer (e.g. a
// bytes.Buffer from tests) it returns false, which keeps the printer
// deterministic in tests without an indirection.
func IsTerminal(w io.Writer) bool {
	switch w {
	case os.Stderr:
		return term.IsTerminal(int(os.Stderr.Fd()))
	case os.Stdout:
		return term.IsTerminal(int(os.Stdout.Fd()))
	default:
		return false
	}
}

// IsTerminalFd is a thin wrapper around golang.org/x/term.IsTerminal
// that swallows the error.  Used by main.go when it knows the fd.
func IsTerminalFd(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

func ansiColor(enabled bool, code int, s string) string {
	if !enabled {
		return s
	}
	return "\x1b[" + itoa(code) + "m" + s + "\x1b[0m"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

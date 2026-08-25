package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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

// diskFullNeedle is the classic ENOSPC message docker surfaces when a
// host runs out of disk space (e.g. the buildx activity write that
// fails during a remote build).
const diskFullNeedle = "no space left on device"

// resolveHint picks the remediation hint for an error render. A
// disk-full failure anywhere in the chain gets a targeted prune hint;
// a remote (SSH) command failure gets a remote-inspect hint; anything
// else falls back to the kind hint (local docker errors keep the
// `pier status` / `pier dev` hint).
func resolveHint(ee *ExitError, chain []string) string {
	for _, msg := range chain {
		if strings.Contains(msg, diskFullNeedle) {
			if ee != nil && ee.RemoteHost != "" {
				return fmt.Sprintf("host %s is out of disk space: ssh in and run 'docker builder prune -af', then check 'docker system df'", ee.RemoteHost)
			}
			return "this host is out of disk space: run 'docker builder prune -af', then check 'docker system df'"
		}
	}
	if ee != nil && ee.RemoteHost != "" {
		return fmt.Sprintf("command failed on %s: ssh in and run 'docker compose ps' / 'docker system df' to inspect", ee.RemoteHost)
	}
	if ee != nil {
		return ee.Kind.Hint()
	}
	return ""
}

// PrintError writes a categorized, multi-line rendering of err to w.
//
//   - When color is true, output uses ANSI color codes (terminal use).
//   - When color is false, output is plain text (logs, CI, pipes).
//   - When verbose is true, every level of the wrap chain is shown.
//     When false, consecutive duplicate messages are collapsed.
//
// Output format:
//
//	error[kind]: <top message>
//	  |
//	  |-> <cause 1>
//	  |-> caused by: <deepest cause>
//	  |
//	  = hint: <kind-specific hint>
//
// The [kind] bracket is omitted for KindUnknown. The "= hint:" block
// is omitted when the kind's Hint() returns "".
func PrintError(w io.Writer, err error, verbose, color bool) {
	if err == nil {
		return
	}

	var (
		kind    = KindUnknown
		chain   []string
		current = err
	)

	var ee *ExitError
	if errors_AsTop(err, &ee) {
		kind = ee.Kind
	}

	for current != nil {
		var msg string
		if exitErr, ok := current.(*ExitError); ok {
			if exitErr.Err != nil {
				msg = exitErr.Err.Error()
			}
		} else {
			msg = current.Error()
		}
		if msg != "" {
			chain = append(chain, msg)
		}
		next := errors.Unwrap(current)
		if next == nil {
			break
		}
		current = next
	}

	if !verbose && len(chain) > 1 {
		dedup := chain[:1]
		for i := 1; i < len(chain); i++ {
			if chain[i] != chain[i-1] {
				dedup = append(dedup, chain[i])
			}
		}
		chain = dedup
	}

	topMsg := chain[0]
	causes := chain[1:]

	if kind == KindUnknown {
		line := "error: " + topMsg
		fmt.Fprintln(w, ansiColor(color, 31, line))
	} else {
		label := "[" + kind.String() + "]"
		line := "error" + label + ": " + topMsg
		code := kindColor(kind)
		fmt.Fprintln(w, ansiColor(color, code, line))
	}

	if len(causes) > 0 {
		fmt.Fprintln(w, "  |")
		for i, c := range causes {
			prefix := "|-> "
			if i == len(causes)-1 {
				prefix = "|-> caused by: "
			}
			fmt.Fprintln(w, "  "+prefix+c)
		}
	}

	hint := resolveHint(ee, chain)
	if hint != "" {
		fmt.Fprintln(w, "  |")
		fmt.Fprintln(w, "= hint: "+hint)
	}
}

func kindColor(k Kind) int {
	switch k {
	case KindConfig:
		return 33
	case KindUser:
		return 36
	default:
		return 31
	}
}

func errors_AsTop(err error, target **ExitError) bool {
	for err != nil {
		if ee, ok := err.(*ExitError); ok {
			*target = ee
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

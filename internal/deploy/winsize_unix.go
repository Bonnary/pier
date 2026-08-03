//go:build !windows

package deploy

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyWindowChanges forwards SIGWINCH to ch on unix platforms; on
// windows it is a no-op (winsize_windows.go).
func notifyWindowChanges(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}

// stopWindowChanges stops forwarding SIGWINCH to ch.
func stopWindowChanges(ch chan<- os.Signal) {
	signal.Stop(ch)
}

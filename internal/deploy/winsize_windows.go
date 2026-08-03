//go:build windows

package deploy

import "os"

// notifyWindowChanges is a no-op on windows: there is no SIGWINCH.
func notifyWindowChanges(ch chan<- os.Signal) {}

// stopWindowChanges is a no-op on windows.
func stopWindowChanges(ch chan<- os.Signal) {}

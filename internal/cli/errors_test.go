package cli

import (
	"errors"
	"testing"
)

func TestExitCodes(t *testing.T) {
	if ExitOK != 0 || ExitGeneral != 1 || ExitPreflight != 2 || ExitBuild != 3 || ExitUp != 4 || ExitExecDown != 5 {
		t.Errorf("exit codes changed: %d %d %d %d %d %d", ExitOK, ExitGeneral, ExitPreflight, ExitBuild, ExitUp, ExitExecDown)
	}
}

func TestPreflightError(t *testing.T) {
	err := &ExitError{Code: ExitPreflight, Err: errors.New("ssh unreachable")}
	if !errors.Is(err, ErrPreflight) {
		t.Error("errors.Is(err, ErrPreflight) = false, want true")
	}
}

package cli

import (
	"errors"
	"testing"

	"github.com/pcnerd/pier/internal/deploy"
)

func TestExitCodes(t *testing.T) {
	if ExitOK != 0 || ExitGeneral != 1 || ExitPreflight != 2 || ExitBuild != 3 || ExitUp != 4 || ExitExecDown != 5 || ExitAborted != 130 {
		t.Errorf("exit codes changed: %d %d %d %d %d %d %d", ExitOK, ExitGeneral, ExitPreflight, ExitBuild, ExitUp, ExitExecDown, ExitAborted)
	}
}

func TestPreflightError(t *testing.T) {
	err := &ExitError{Code: ExitPreflight, Err: errors.New("ssh unreachable")}
	if !errors.Is(err, ErrPreflight) {
		t.Error("errors.Is(err, ErrPreflight) = false, want true")
	}
}

func TestAbortedError(t *testing.T) {
	err := AbortedError()
	if err == nil {
		t.Fatal("AbortedError() = nil")
	}
	if !errors.Is(err, ErrAborted) {
		t.Errorf("errors.Is(err, ErrAborted) = false, want true")
	}
	if got := ExitCode(err); got != ExitAborted {
		t.Errorf("ExitCode(err) = %d, want %d", got, ExitAborted)
	}
}

func TestReexportedKinds(t *testing.T) {
	if KindConfig != deploy.KindConfig {
		t.Errorf("KindConfig = %v, want %v", KindConfig, deploy.KindConfig)
	}
	if KindDocker != deploy.KindDocker {
		t.Errorf("KindDocker = %v, want %v", KindDocker, deploy.KindDocker)
	}
}

func TestReexportedConstructors(t *testing.T) {
	base := errors.New("x")
	if got := ConfigError(base); !errors.Is(got, base) {
		t.Errorf("ConfigError does not wrap base")
	}
	if got := DockerError(base); !errors.Is(got, base) {
		t.Errorf("DockerError does not wrap base")
	}
}

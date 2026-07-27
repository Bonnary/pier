package cli

import (
	"errors"
	"fmt"
)

const (
	ExitOK        = 0
	ExitGeneral   = 1
	ExitPreflight = 2
	ExitBuild     = 3
	ExitUp        = 4
	ExitExecDown  = 5
)

var (
	ErrPreflight = errors.New("preflight")
	ErrBuild     = errors.New("build")
	ErrUp        = errors.New("up")
	ErrExecDown  = errors.New("container not running")
)

// ExitError is a typed error carrying the desired process exit code.
// Use errors.Is to test the underlying sentinel (ErrPreflight, etc.).
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d: %v", e.Code, e.Err) }
func (e *ExitError) Unwrap() error { return e.Err }

func (e *ExitError) Is(target error) bool {
	switch e.Code {
	case ExitPreflight:
		return target == ErrPreflight
	case ExitBuild:
		return target == ErrBuild
	case ExitUp:
		return target == ErrUp
	case ExitExecDown:
		return target == ErrExecDown
	}
	return false
}

// PreflightError wraps err with the preflight exit code.
func PreflightError(err error) error { return &ExitError{Code: ExitPreflight, Err: err} }

// BuildError wraps err with the build exit code.
func BuildError(err error) error { return &ExitError{Code: ExitBuild, Err: err} }

// UpError wraps err with the up/health exit code.
func UpError(err error) error { return &ExitError{Code: ExitUp, Err: err} }

// ExecDownError returns ExitExecDown with a fixed message.
func ExecDownError() error { return &ExitError{Code: ExitExecDown, Err: ErrExecDown} }

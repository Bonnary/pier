package deploy

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
	ErrBuild    = errors.New("build")
	ErrUp       = errors.New("up")
	ErrExecDown = errors.New("container not running")
)

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

func PreflightError(err error) error { return &ExitError{Code: ExitPreflight, Err: err} }
func BuildError(err error) error     { return &ExitError{Code: ExitBuild, Err: err} }
func UpError(err error) error        { return &ExitError{Code: ExitUp, Err: err} }
func ExecDownError() error           { return &ExitError{Code: ExitExecDown, Err: ErrExecDown} }

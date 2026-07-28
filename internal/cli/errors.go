package cli

import "github.com/pcnerd/pier/internal/deploy"

const (
	ExitOK        = deploy.ExitOK
	ExitGeneral   = deploy.ExitGeneral
	ExitPreflight = deploy.ExitPreflight
	ExitBuild     = deploy.ExitBuild
	ExitUp        = deploy.ExitUp
	ExitExecDown  = deploy.ExitExecDown
	ExitAborted   = deploy.ExitAborted
)

var (
	ErrPreflight = deploy.ErrPreflight
	ErrBuild     = deploy.ErrBuild
	ErrUp        = deploy.ErrUp
	ErrExecDown  = deploy.ErrExecDown
	ErrAborted   = deploy.ErrAborted
)

type ExitError = deploy.ExitError

func PreflightError(err error) error { return deploy.PreflightError(err) }
func BuildError(err error) error     { return deploy.BuildError(err) }
func UpError(err error) error        { return deploy.UpError(err) }
func ExecDownError() error           { return deploy.ExecDownError() }
func AbortedError() error            { return deploy.AbortedError() }

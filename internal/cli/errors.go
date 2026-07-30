package cli

import "github.com/Bonnary/pier/internal/deploy"

const (
	ExitOK        = deploy.ExitOK
	ExitGeneral   = deploy.ExitGeneral
	ExitPreflight = deploy.ExitPreflight
	ExitBuild     = deploy.ExitBuild
	ExitUp        = deploy.ExitUp
	ExitExecDown  = deploy.ExitExecDown
	ExitPortInUse = deploy.ExitPortInUse
	ExitAborted   = deploy.ExitAborted
)

var (
	ErrPreflight = deploy.ErrPreflight
	ErrBuild     = deploy.ErrBuild
	ErrUp        = deploy.ErrUp
	ErrExecDown  = deploy.ErrExecDown
	ErrPortInUse = deploy.ErrPortInUse
	ErrAborted   = deploy.ErrAborted
)

type (
	Kind      = deploy.Kind
	ExitError = deploy.ExitError
)

const (
	KindUnknown = deploy.KindUnknown
	KindConfig  = deploy.KindConfig
	KindDocker  = deploy.KindDocker
	KindSSH     = deploy.KindSSH
	KindNetwork = deploy.KindNetwork
	KindUser    = deploy.KindUser
)

func PreflightError(err error) error   { return deploy.PreflightError(err) }
func BuildError(err error) error       { return deploy.BuildError(err) }
func UpError(err error) error          { return deploy.UpError(err) }
func ExecDownError() error             { return deploy.ExecDownError() }
func PortInUseError(ports []int) error { return deploy.PortInUseError(ports) }
func AbortedError() error              { return deploy.AbortedError() }

func ConfigError(err error) error  { return deploy.ConfigError(err) }
func DockerError(err error) error  { return deploy.DockerError(err) }
func SSHError(err error) error     { return deploy.SSHError(err) }
func NetworkError(err error) error { return deploy.NetworkError(err) }
func UserError(err error) error    { return deploy.UserError(err) }

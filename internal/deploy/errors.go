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
	// ExitPortInUse is returned by `pier dev` when the pre-flight
	// host-port probe finds one or more pier-owned host ports already
	// in use on 127.0.0.1. The user must edit [dev.ports] in pier.toml
	// to remap before retrying.
	ExitPortInUse = 6
	// ExitAborted is returned when the user aborts an interactive TUI
	// (q / Ctrl+C). 130 = 128 + SIGINT, the POSIX shell convention.
	ExitAborted = 130
)

var (
	ErrBuild     = errors.New("build")
	ErrUp        = errors.New("up")
	ErrExecDown  = errors.New("container not running")
	ErrPortInUse = errors.New("port in use")
	ErrAborted   = errors.New("aborted")
)

type ExitError struct {
	Code int
	Kind Kind
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
	case ExitPortInUse:
		return target == ErrPortInUse
	case ExitAborted:
		return target == ErrAborted
	}
	return false
}

func PreflightError(err error) error {
	return &ExitError{Code: ExitPreflight, Kind: KindConfig, Err: err}
}
func BuildError(err error) error { return &ExitError{Code: ExitBuild, Kind: KindDocker, Err: err} }
func UpError(err error) error    { return &ExitError{Code: ExitUp, Kind: KindDocker, Err: err} }
func ExecDownError() error       { return &ExitError{Code: ExitExecDown, Kind: KindDocker, Err: ErrExecDown} }

// PortInUseError builds an error for a pre-flight port-probe collision.
// ports is the list of host ports that were already listening on
// 127.0.0.1 when `pier dev` started. The user edits [dev.ports] in
// pier.toml to remap and retries.
func PortInUseError(ports []int) error {
	return &ExitError{
		Code: ExitPortInUse,
		Kind: KindUser,
		Err:  fmt.Errorf("%w: %v", ErrPortInUse, ports),
	}
}
func AbortedError() error { return &ExitError{Code: ExitAborted, Kind: KindUser, Err: ErrAborted} }

type Kind int

const (
	KindUnknown Kind = iota
	KindConfig
	KindDocker
	KindSSH
	KindNetwork
	KindUser
)

func (k Kind) String() string {
	switch k {
	case KindConfig:
		return "config"
	case KindDocker:
		return "docker"
	case KindSSH:
		return "ssh"
	case KindNetwork:
		return "network"
	case KindUser:
		return "user"
	default:
		return "unknown"
	}
}

func ConfigError(err error) error  { return &ExitError{Code: ExitGeneral, Kind: KindConfig, Err: err} }
func DockerError(err error) error  { return &ExitError{Code: ExitGeneral, Kind: KindDocker, Err: err} }
func SSHError(err error) error     { return &ExitError{Code: ExitGeneral, Kind: KindSSH, Err: err} }
func NetworkError(err error) error { return &ExitError{Code: ExitGeneral, Kind: KindNetwork, Err: err} }
func UserError(err error) error    { return &ExitError{Code: ExitGeneral, Kind: KindUser, Err: err} }

func (k Kind) Hint() string {
	switch k {
	case KindConfig:
		return "see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'"
	case KindDocker:
		return "run 'pier status' to see container state, then 'pier dev' to (re)start the stack"
	case KindSSH:
		return "verify ssh access: 'ssh deploy@<host>', check ~/.ssh/id_ed25519 perms (chmod 600)"
	case KindNetwork:
		return "check internet/VPN; 'docker pull <image>' manually to isolate registry vs DNS"
	default:
		return ""
	}
}

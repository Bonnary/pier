package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Sentinel errors for the bootstrap flow. The CLI inspects these
// with errors.Is to decide whether to skip, re-prompt, or abort.
var (
	// ErrNotBootstrapped wraps the deploy-side fail-fast: the server
	// is not provisioned and `pier bootstrap` must run first.
	ErrNotBootstrapped = errors.New("server not bootstrapped")
	// ErrAlreadyBootstrapped is returned by BootstrapEnv when the
	// probe passes; the CLI prints "already bootstrapped — skipping".
	ErrAlreadyBootstrapped = errors.New("already bootstrapped")
	// ErrSudoWrongPassword is returned by ValidateSudo when the sudo
	// password is rejected. The CLI re-prompts once.
	ErrSudoWrongPassword = errors.New("wrong sudo password")
	// ErrSudoNotSudoers is returned when the user has no sudo rights
	// at all; the CLI aborts with instructions.
	ErrSudoNotSudoers = errors.New("deploy user has no sudo rights")
)

// ProbeBootstrap reports whether the deploy user can run docker
// without sudo: `command -v docker` and `docker info` must both
// succeed. A non-zero exit counts as "not bootstrapped" (false, nil);
// only session-level failures (connection resets, etc.) return a
// non-nil error.
func ProbeBootstrap(ctx context.Context, r stdinRunner) (bool, error) {
	cmd := "command -v docker && docker info"
	_, stderr, err := r.Run(ctx, cmd)
	if err == nil {
		return true, nil
	}
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) {
		return false, fmt.Errorf("%w: probe %q: %v (stderr: %s)", ErrPreflight, cmd, err, bytes.TrimSpace(stderr))
	}
	return false, nil
}

// classifySudoErr maps a failed `sudo -S` run to its sentinel,
// distinguishing a wrong password from a user with no sudo rights.
func classifySudoErr(stderr []byte, err error) error {
	s := strings.ToLower(string(stderr))
	switch {
	case strings.Contains(s, "not in the sudoers") || strings.Contains(s, "may not run sudo"):
		return ErrSudoNotSudoers
	case strings.Contains(s, "sorry, try again") || strings.Contains(s, "incorrect password") || strings.Contains(s, "authentication failure"):
		return ErrSudoWrongPassword
	default:
		return fmt.Errorf("remote command failed: %v (stderr: %s)", err, bytes.TrimSpace(stderr))
	}
}

// runSudo executes cmd via `sudo -S sh -c '<cmd>'` with the password
// piped on the session's stdin — never on the command line. Embedded
// apostrophes are escaped so they cannot break out of the single-quoted
// sh -c argument.
func runSudo(ctx context.Context, r stdinRunner, password, cmd string) error {
	cmd = strings.ReplaceAll(cmd, "'", `'\''`)
	full := fmt.Sprintf("sudo -S sh -c '%s'", cmd)
	_, stderr, err := r.RunStdin(ctx, full, password+"\n")
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}

// ValidateSudo proves the password works by running `sudo -S -v`.
// Returns ErrSudoWrongPassword or ErrSudoNotSudoers on failure.
func ValidateSudo(ctx context.Context, r stdinRunner, password string) error {
	_, stderr, err := r.RunStdin(ctx, "sudo -S -v", password+"\n")
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}

// Provision installs Docker Engine + the compose plugin with the
// official get.docker.com script and adds user to the docker group,
// both under sudo. Idempotent — safe to re-run with --force.
func Provision(ctx context.Context, r stdinRunner, password, user string) error {
	if err := runSudo(ctx, r, password, "curl -fsSL https://get.docker.com | sh"); err != nil {
		return fmt.Errorf("install docker: %w", err)
	}
	if err := runSudo(ctx, r, password, "usermod -aG docker "+strconv.Quote(user)); err != nil {
		return fmt.Errorf("add user to docker group: %w", err)
	}
	return nil
}

// VerifyBootstrap confirms the daemon runs, the compose plugin is
// present, and the user is a member of the docker group. Group
// membership only applies to new SSH connections, so the group file
// is checked directly (getent) instead of re-running docker.
func VerifyBootstrap(ctx context.Context, r stdinRunner, password, user string) error {
	cmd := fmt.Sprintf("docker info && docker compose version && getent group docker | grep -qw %s", strconv.Quote(user))
	if err := runSudo(ctx, r, password, cmd); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	return nil
}

// bootstrapConn is the subset of *Client that BootstrapEnv uses.
type bootstrapConn interface {
	stdinRunner
	Close() error
}

// dialBootstrap is a seam for tests to inject a fake connection.
var dialBootstrap = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
	return Dial(ctx, cfg)
}

// BootstrapOpts is the parameter set for BootstrapEnv.
type BootstrapOpts struct {
	// Force skips the probe and re-provisions even when the server
	// is already bootstrapped.
	Force bool
	// User is the deploy user that gets docker group membership.
	User string
}

// BootstrapEnv runs the full one-time provisioning flow for one
// server: probe (unless Force), sudo validation, provision, verify.
// Returns ErrAlreadyBootstrapped when the probe passes and Force is
// false.
func BootstrapEnv(ctx context.Context, cfg SSHConfig, password string, opts BootstrapOpts) error {
	client, err := dialBootstrap(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	if !opts.Force {
		ok, err := ProbeBootstrap(ctx, client)
		if err != nil {
			return err
		}
		if ok {
			return ErrAlreadyBootstrapped
		}
	}
	if err := ValidateSudo(ctx, client, password); err != nil {
		return err
	}
	if err := Provision(ctx, client, password, opts.User); err != nil {
		return err
	}
	return VerifyBootstrap(ctx, client, password, opts.User)
}

// ProbeEnv dials cfg and runs the bootstrap probe. Convenience for
// the CLI's skip check before prompting for the password.
func ProbeEnv(ctx context.Context, cfg SSHConfig) (bool, error) {
	client, err := dialBootstrap(ctx, cfg)
	if err != nil {
		return false, err
	}
	defer client.Close()
	return ProbeBootstrap(ctx, client)
}

// NotBootstrappedError builds the deploy-side fail-fast error: the
// server was never provisioned, so `pier bootstrap <env>` must run
// first. Wrapped as a preflight error so the CLI exits with code 2.
func NotBootstrappedError(env string) error {
	return PreflightError(fmt.Errorf("%w: %s — run `pier bootstrap %s` first", ErrNotBootstrapped, env, env))
}

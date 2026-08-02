package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

// quoteShell wraps s in single quotes with POSIX apostrophe escaping
// (`'` becomes `'\”`), so it can be embedded in a remote shell
// command string. runSudo applies this to its whole command body.
func quoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runSudo executes cmd via `sudo -S -p ” sh -c '<cmd>'` with the
// password piped on the session's stdin — never on the command
// line — streaming each output line to the callbacks. `-p ”`
// suppresses sudo's own password prompt (the password is already
// piped). Embedded apostrophes are escaped so they cannot break out
// of the single-quoted sh -c argument. On failure the captured
// stderr is classified into the bootstrap sentinels.
func runSudo(ctx context.Context, r stdinRunner, password, cmd string, onStdout, onStderr func(string)) error {
	full := fmt.Sprintf("sudo -S -p '' sh -c %s", quoteShell(cmd))
	stderr, err := r.RunStreamStdin(ctx, full, password+"\n", onStdout, onStderr)
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}

// ValidateSudo proves the password works by running `sudo -S -v`.
// Returns ErrSudoWrongPassword or ErrSudoNotSudoers on failure.
func ValidateSudo(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error {
	stderr, err := r.RunStreamStdin(ctx, "sudo -S -v", password+"\n", onStdout, onStderr)
	if err != nil {
		return classifySudoErr(stderr, err)
	}
	return nil
}

// ClockSyncThreshold is the max |remote - local| offset in seconds
// tolerated before pier force-sets the remote clock. Freshly-reset
// VMs boot with a stale RTC; apt rejects signed Release files whose
// dates fall outside the (wrong) guest clock, so even minutes of
// skew break provisioning.
const ClockSyncThreshold = 60

// EnsureClockSynced compares the remote clock to the local clock and,
// when they differ by more than ClockSyncThreshold seconds, force-sets
// the remote clock from the local one under sudo (`date -s @<epoch>`).
// Needs sudo only when a correction is required. On correction it
// re-reads the remote epoch and emits one line via onStdout:
// `remote clock was Ns off; corrected to <RFC3339>`.
func EnsureClockSynced(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error {
	read := func() (int64, error) {
		stdout, _, err := r.Run(ctx, "date +%s")
		if err != nil {
			return 0, fmt.Errorf("read remote clock: %w", err)
		}
		epoch, err := strconv.ParseInt(strings.TrimSpace(string(stdout)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("read remote clock: parse %q: %w", strings.TrimSpace(string(stdout)), err)
		}
		return epoch, nil
	}
	remote, err := read()
	if err != nil {
		return err
	}
	local := time.Now().Unix()
	skew := local - remote
	if skew < 0 {
		skew = -skew
	}
	if skew <= ClockSyncThreshold {
		return nil
	}
	if err := runSudo(ctx, r, password, fmt.Sprintf("date -s @%d", local), onStdout, onStderr); err != nil {
		return fmt.Errorf("sync remote clock: %w", err)
	}
	remote, err = read()
	if err != nil {
		return err
	}
	if onStdout != nil {
		onStdout(fmt.Sprintf("remote clock was %ds off; corrected to %s", skew, time.Unix(remote, 0).Format(time.RFC3339)))
	}
	return nil
}

// Provision installs Docker Engine + the compose plugin with the
// official get.docker.com script and adds user to the docker group,
// both under sudo. Idempotent — safe to re-run with --force.
func Provision(ctx context.Context, r stdinRunner, password, user string, onStdout, onStderr func(string)) error {
	if err := runSudo(ctx, r, password, "curl -fsSL https://get.docker.com | sh", onStdout, onStderr); err != nil {
		return fmt.Errorf("install docker: %w", err)
	}
	if err := runSudo(ctx, r, password, "usermod -aG docker "+strconv.Quote(user), onStdout, onStderr); err != nil {
		return fmt.Errorf("add user to docker group: %w", err)
	}
	return nil
}

// VerifyBootstrap confirms the daemon runs, the compose plugin is
// present, and the user is a member of the docker group. Group
// membership only applies to new SSH connections, so the group file
// is checked directly (getent) instead of re-running docker.
func VerifyBootstrap(ctx context.Context, r stdinRunner, password, user string, onStdout, onStderr func(string)) error {
	cmd := fmt.Sprintf("docker info && docker compose version && getent group docker | grep -qw %s", strconv.Quote(user))
	if err := runSudo(ctx, r, password, cmd, onStdout, onStderr); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	return nil
}

// cmdRunner is the subset of *Client that EnsureDeployPath needs.
// Both *Client and the test scriptedRunner satisfy it.
type cmdRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
}

// ProvisionDeployPath creates the env's deploy path on the remote
// host under sudo and hands it to the deploy user: `mkdir -p <path>
// && chown <user>:<user> <path>`. Idempotent, so `--force` re-runs
// are safe. The user is quoted the same way Provision quotes it.
func ProvisionDeployPath(ctx context.Context, r stdinRunner, password, user, path string, onStdout, onStderr func(string)) error {
	cmd := fmt.Sprintf("mkdir -p %s && chown %s:%s %s",
		quoteShell(path), strconv.Quote(user), strconv.Quote(user), quoteShell(path))
	return runSudo(ctx, r, password, cmd, onStdout, onStderr)
}

// EnsureDeployPath creates the deploy path as the deploy user without
// sudo, so rsync has a writable destination. Fails when the parent
// directory is not writable; the deploy preflight turns that into an
// actionable error.
func EnsureDeployPath(ctx context.Context, r cmdRunner, path string) error {
	_, _, err := r.Run(ctx, "mkdir -p "+quoteShell(path))
	return err
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
	// Path is the env's deploy directory ([deploy.<env>].path),
	// created with sudo and chowned to User. Empty means no path to
	// create.
	Path string
	// OnStdout/OnStderr stream each remote output line as it
	// arrives; may be nil.
	OnStdout func(string)
	OnStderr func(string)
}

// BootstrapEnv runs the full one-time provisioning flow for one
// server: probe (unless Force), sudo validation, clock sync, provision,
// deploy path creation (unless Path is empty), verify. Returns
// ErrAlreadyBootstrapped when the probe passes and Force is false.
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
	if err := ValidateSudo(ctx, client, password, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	if err := EnsureClockSynced(ctx, client, password, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	if err := Provision(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr); err != nil {
		return err
	}
	if opts.Path != "" {
		if err := ProvisionDeployPath(ctx, client, password, opts.User, opts.Path, opts.OnStdout, opts.OnStderr); err != nil {
			return fmt.Errorf("create deploy path: %w", err)
		}
	}
	return VerifyBootstrap(ctx, client, password, opts.User, opts.OnStdout, opts.OnStderr)
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

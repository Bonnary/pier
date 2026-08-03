# Remote shell / exec for deploy environments

Date: 2026-08-03

## Goal

`pier shell <env>` opens an interactive bash in the `app` container of a
deploy environment (e.g. `pier shell production`), and `pier exec <env>
<cmd...>` runs a one-off command there — both over the same SSH transport
`pier deploy` already uses. The no-env forms (`pier shell`, `pier exec
<cmd...>`) keep their current local-dev behavior unchanged.

## Context

- `pier shell` today runs local `docker compose exec laravel.test bash`
  (`internal/cli/shell.go`); `pier exec` runs the local equivalent.
- Production runs `docker-compose.prod.yml` from `[deploy.<env>].path`
  on the remote host, with the app service named `app` (and `webserver`).
- SSH plumbing already exists: `deploy.Dial` (key auth with password
  fallback), `newSSHConfig` (`internal/cli/helpers.go`), and the typed
  error/exit-code contract (`internal/cli/errors.go`).
- No remote interactive (PTY) session support exists yet; remote
  commands today are non-interactive `Run`/`RunStream` calls.

## Command surface

- `pier shell [env]` — no env: local dev shell (unchanged). With env:
  SSH to `[deploy.<env>]`, run
  `cd <path> && docker compose -f docker-compose.prod.yml exec app bash`
  in a PTY session. Any configured env name works, not just
  "production".
- `pier exec <cmd...>` — local (unchanged). With env: first positional
  argument is treated as an env iff it matches a configured
  `[deploy.<env>]` name AND at least one more argument follows; the
  rest are the remote command. Example:
  `pier exec production php artisan migrate`.
- Unknown env in the env position → existing
  `no [deploy.<env>] section in pier.toml` error (same message as
  `pier status <env>` / `pier deploy <env>`).
- `pier exec production` with no command → clear error
  (`no command given for remote exec`).

## Behavior

### Remote exec (non-interactive)

- No PTY; remote command runs with `-T` (`docker compose exec -T`).
- stdout/stderr streamed to pier's stdout/stderr as they arrive.
- Remote exit code propagated to pier's exit code:
  `ssh.ExitError.ExitStatus()` when present, else the standard
  SSH error wrapping.
- Runs as the container's default user (no `-u` flag), matching the
  baked production image's runtime user.

### Remote shell (interactive)

- PTY requested via `session.RequestPty("xterm-256color", rows, cols,
  modes)` with the local terminal's current size.
- Local stdin put into raw mode (`term.MakeRaw`) for the duration;
  restored (including on error paths) before returning.
- Local terminal resize (SIGWINCH) forwards `session.WindowChange` to
  the remote pty.
- Bidirectional byte copy: local stdin → session, session stdout/stderr
  → local stdout/stderr. One writer per direction, single goroutine
  each, no concurrent writers on the session.
- Exit code propagated as in remote exec.
- If local stdin is not a TTY, fail with a clear error instead of
  running a broken interactive session.

## New code

- `internal/deploy/shell.go`:
  - `RemoteExec(ctx context.Context, cfg SSHConfig, dir string, args []string) error`
    — dials, runs the non-TTY remote command, maps exit status.
  - `RemoteShell(ctx context.Context, cfg SSHConfig, dir string) error`
    — dials, requests the pty, wires raw-mode stdin + resize + copies.
- `internal/cli/shell.go` / `exec.go`: accept optional env, resolve via
  `cfg.Deploy[env]`, dial with `newSSHConfig(dc)`, wrap failures in the
  existing error contract (`SSHError` for dial/transport failures,
  remote non-zero exit surfaces as-is with its exit code).
- No changes to `deploy.Dial`, `SSHConfig`, or the error package.

## Edge cases

- Container down on the remote host: docker's own
  `service "app" is not running`-style error surfaces; wrapped as
  `SSHError` with a hint.
- `[deploy.<env>].path` empty: remote command runs without `cd` and
  relies on `-f docker-compose.prod.yml` resolving in the login
  directory (matches `pier deploy`'s tolerance).
- Ctrl+C inside the remote shell: raw-mode pass-through; the remote
  shell receives SIGINT, session exits, pier restores the terminal.
- Env name matching is exact (case-sensitive), same as `pier deploy`.

## Testing

- Unit tests for the CLI arg resolution (env detection rule, unknown
  env, missing command) reusing the existing `newShellCmd`/`newExecCmd`
  test seams in `internal/cli`.
- `RemoteExec`/`RemoteShell` integration tests against the in-process
  SSH test server (`internal/deploy/testssh_test.go`, already handles
  `exec`, `env`, `pty-req`, and `shell` requests) — verify the exact
  remote command string, pty request, exit-code propagation, and that
  raw mode is restored after the session.
- README command table gains the env forms of `shell` / `exec`.

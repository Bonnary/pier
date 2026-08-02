# Pier — Deploy Path Provisioning — Design

**Date:** 2026-08-02
**Status:** Draft (brainstorming), pending spec review

## Problem

`pier deploy <env>` syncs the project tree to `[deploy.<env>].path`
on the remote host with rsync (`internal/deploy/deploy.go:72` →
`internal/deploy/rsync.go:46`). Nothing in pier ever creates that
directory:

- The design spec (2026-07-26-pier-design.md#6) says pre-flight
  "confirms deploy path is writable" — it does not create it.
- `pier bootstrap` provisions Docker and grants the deploy user
  passwordless Docker access, but never creates the project
  directory (2026-08-01-bootstrap-design.md#116 — three
  provisioning steps, none touch the deploy path).

When the path does not exist and its parent is not writable by the
deploy user (e.g. `path = "/test_web"` on a fresh VPS), rsync fails:

```
rsync: [Receiver] mkdir "/test_web" failed: Permission denied (13)
rsync error: error in file IO (code 11) at main.c(800) [Receiver=3.2.7]
```

pier then surfaces this as a `KindConfig` error whose only message is
`exit status 11` — the rsync stderr is discarded (`osRunner` runs the
command with nil stdout/stderr, `internal/deploy/rsync.go:17-20`) —
and the hint points at `pier.toml` configuration, which has nothing
to do with the failure:

```
error[config]: exit status 11
  |
= hint: see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'
```

Real-world reproduction: `pier deploy production` with
`host = 192.168.122.63`, `user = host`, `path = "/test_web"` fails at
the sync phase with exit code 11 because `/test_web` does not exist
and `host` cannot create it in `/` (password-protected sudo).

## Goals

1. `pier bootstrap <env>` creates the env's deploy path
   (`sudo mkdir -p <path>` + `sudo chown <user>:<user> <path>`) as
   part of the one-time provisioning flow, since it is the only
   non-interactive step that has the sudo password.
2. `pier deploy <env>` preflight ensures the deploy path exists
   (`mkdir -p <path>` as the deploy user, no sudo) and fails fast
   with an actionable message — including the exact `sudo mkdir` /
   `chown` commands — instead of the cryptic rsync `exit status 11`.
3. Rsync stderr is captured and included in sync failures so any
   future rsync problem is self-explanatory.
4. Deploy stays non-interactive; no sudo password prompt is ever
   added to `pier deploy`.

## Non-goals

- Creating the path on an arbitrary writable parent at deploy time
  via sudo. Deploy must stay scriptable/CI-able; privileged path
  creation belongs to the one-time bootstrap flow.
- Migrating or hardening existing remote state; `SaveState` already
  creates `.pier/` itself (`internal/deploy/state.go:53-56`).
- Verifying that `[deploy.<env>].path` is absolute. Existing config
  validation behavior is unchanged.

## Approach

**A (chosen): bootstrap creates the path; deploy preflight ensures
it.** One new provisioning step plus one preflight check. Reuses the
existing `runSudo` / `stdinRunner` plumbing — no new SSH surface.

Rejected alternatives:

- **B — deploy prompts for sudo when the path is missing.** Breaks
  the bootstrap spec's "deploy / rollback stay non-interactive and
  fail fast" contract (`2026-08-01-bootstrap-design.md#44`).
- **C — documentation + better error message only.** Leaves the
  footgun in place; every fresh VPS hits the same wall.

## Design

### 1. Bootstrap: create the deploy path (`internal/deploy/bootstrap.go`)

`BootstrapOpts` gains a `Path string` field — the deploy directory
for the env being bootstrapped (empty when unset).

`BootstrapEnv` runs one additional `runSudo` step between
`Provision` and `VerifyBootstrap`, only when `Path != ""`:

```
sudo -S -p '' sh -c 'mkdir -p <path> && chown <user>:<user> <path>'
```

- The path is shell-quoted the same way `runSudo` already handles
  embedded apostrophes (`strings.ReplaceAll(cmd, "'", `'\''`)`
  inside the existing single-quoted `sh -c` wrapper,
  `internal/deploy/bootstrap.go:70-78`). The user is quoted the same
  way the existing `Provision` step quotes it (`strconv.Quote`,
  `internal/deploy/bootstrap.go:97`).
- Both commands are idempotent, so `--force` re-runs are safe.
- The step runs before `VerifyBootstrap` so a bootstrap run either
  completes fully or fails before the verify step; the user sees the
  failing step's error and can re-run.

Flow per env after this change:

1. Probe (skip if bootstrapped, unless `--force`).
2. Validate sudo password (`sudo -S -v`).
3. Install Docker Engine + compose plugin.
4. Grant deploy user docker group membership.
5. **Create deploy path: `mkdir -p <path> && chown <user>:<user> <path>`.**
6. Verify (`docker info`, `docker compose version`, group membership).

### 2. CLI: pass the path (`internal/cli/bootstrap.go`)

Both `BootstrapOpts` constructions in `runBootstrap` (the initial
attempt and the wrong-password retry) pass `Path: dc.Path`.

`--all` and explicit env args are unaffected structurally: the
per-env loop already resolves `dc := cfg.Deploy[env]`, so each env
creates its own path with its own sudo session.

### 3. Deploy preflight: ensure the path (`internal/deploy/deploy.go`)

`preflight` gains a step after the bootstrap probe (and before the
function returns the client): run

```
mkdir -p <path>
```

as the deploy user via `client.Run` — no sudo. Two outcomes:

- **Success** (path existed or the parent was writable): pipeline
  proceeds unchanged.
- **Failure** (e.g. `mkdir /test_web: permission denied`): return a
  `PreflightError` (KindConfig, exit 2) with the actionable message:

```
deploy path /test_web on 192.168.122.63 is not writable for host.
Create it once with:  sudo mkdir -p /test_web
                      sudo chown host:host /test_web
(or re-run `pier bootstrap production` to create it automatically.)
```

The path is single-quoted for the remote command; the same
apostrophe escaping as `runSudo` applies.

### 4. Sync: surface rsync stderr (`internal/deploy/rsync.go`)

`osRunner.Run` captures the command's combined output and, on a
non-zero exit, wraps the error with a trimmed excerpt (first 4 KB)
of the output. The `CommandRunner` interface is unchanged; `Sync`
and its callers are untouched. The CLI error rendering then shows
e.g. `exit status 11: mkdir "/test_web" failed: Permission denied
(13)` instead of a bare `exit status 11`.

### 5. Error classification

No changes to `Kind` / `ExitError` / hints. The preflight path error
is already a `PreflightError` (exit 2, KindConfig); its message is
self-explanatory, so the generic config hint is fine as a secondary
pointer. The rsync stderr excerpt rides inside the existing
`PreflightError` wrap from `deploy.go:74`.

## Testing

### Unit (fake runner, no SSH)

| Test | Asserts |
|---|---|
| `TestBootstrapCreatesDeployPath` | `BootstrapEnv` with `Path` set runs the `mkdir -p` + `chown` sudo command with the path quoted; step order: provision → path → verify |
| `TestBootstrapSkipsPathWhenEmpty` | `Path == ""` → no mkdir/chown command is run |
| `TestBootstrapPathSudoFailure` | mkdir/chown sudo failure classifies through `classifySudoErr` (wrong password → `ErrSudoWrongPassword`, not-in-sudoers → `ErrSudoNotSudoers`) |
| `TestDeployPreflightCreatesPath` | Fake runner: `mkdir -p` succeeds → pipeline proceeds to render/sync |
| `TestDeployPreflightPathUnwritable` | Fake runner: `mkdir -p` fails → `PreflightError` whose message names the path, host, user, and the exact sudo fix commands; exit code 2 |
| `TestOsRunnerCapturesOutput` | `osRunner` fails with a non-zero exit → error contains the captured stderr excerpt |
| `TestBootstrapCliPassesPath` | CLI `runBootstrap` passes `dc.Path` into both `BootstrapOpts` constructions |

Existing fakes: `bootstrap_test.go` fake runner and the deploy
pipeline unit tests (`deploy_unit_test.go`) are extended to record
commands and simulate `mkdir -p` results.

### Integration (`-tags=integration`, Linux)

`TestBootstrapRealServer` (`bootstrap_integration_test.go`) gains an
optional `PIER_TEST_DEPLOY_PATH` env: when set (with
`PIER_TEST_SUDO_PASSWORD`), it passes `Path` to `BootstrapEnv` and
asserts the directory exists on the host and is owned by the deploy
user after the run.

## Files changed

- `internal/deploy/bootstrap.go` — `BootstrapOpts.Path`, new
  provisioning step.
- `internal/cli/bootstrap.go` — pass `Path` in both `BootstrapOpts`
  constructions.
- `internal/deploy/deploy.go` — preflight `mkdir -p <path>` with
  actionable failure error.
- `internal/deploy/rsync.go` — capture combined output in
  `osRunner.Run`, wrap non-zero exits with a trimmed excerpt.
- `internal/deploy/bootstrap_test.go` — new unit tests (fake
  runner).
- `internal/deploy/deploy_unit_test.go` — preflight path tests
  (fake runner).
- `internal/deploy/rsync_test.go` — `osRunner` output capture test.
- `internal/cli/bootstrap_test.go` — CLI path-passing test.
- `internal/deploy/bootstrap_integration_test.go` — optional path
  assertion.
- `README.md` — bootstrap section: mention that bootstrap creates
  the deploy path.

Boundary rules hold: `cli` never runs SSH directly; `deploy` never
reads the TUI; `deploy` never knows about Laravel. The sudo password
is still never stored anywhere.

## Verification checklist (manual)

1. In a project with `[deploy.production]` pointing at a fresh
   server: `pier bootstrap production` creates the deploy path,
   owned by the deploy user.
2. `pier deploy production` against a server whose path was deleted:
   fails fast in preflight with the sudo-fix message (no rsync).
3. `pier deploy production` against the real test server
   (`192.168.122.63`, `path = "/test_web"`): preflight creates the
   path, sync succeeds, build/up/health complete.
4. `pier bootstrap --force production` re-runs cleanly (idempotent).

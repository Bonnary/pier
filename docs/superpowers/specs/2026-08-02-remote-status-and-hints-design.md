# Design: Remote status probe and remote-aware error hints

Date: 2026-08-02

## Problem

`pier deploy production` failed on the remote host with
`no space left on device` (docker buildx activity file write). The
error rendering added the hint `run 'pier status' to see container
state, then 'pier dev' to (re)start the stack`, which is wrong for
remote failures: `pier status` only inspects the local machine and
`pier dev` only restarts the local stack.

Two defects:

1. The `KindDocker.Hint()` (internal/deploy/errors.go) hint is
   written for local docker failures but is also attached to remote
   build/up failures (`BuildError`, `UpError`), which always run on
   the remote host over SSH.
2. There is no way to inspect container state, disk usage, health,
   or last deploy on a production host from the CLI.

## Decisions

- `pier status` with no env argument keeps its current local-only
  behavior (backwards compatible).
- `pier status <env>` performs a full remote health probe over SSH.
- Remote docker failures get remote-aware hints; local docker errors
  keep the current local hint. "no space left on device" gets a
  targeted disk-full hint.
- No changes to exit codes or sentinel errors; the hint rendering is
  the only behavior change for existing errors.

## Section 1: Remote-aware hints

Add a `RemoteHost string` field to `ExitError` (deploy package).
`BuildError` and `UpError` are only ever used for remote pipeline
failures (verified by grep: only the deploy pipeline and its cli
re-export use them). Add two new constructors that stamp the host:

- `RemoteBuildError(host string, err error)`
- `RemoteUpError(host string, err error)`

Both return `*ExitError` with the same Code/Kind as their plain
counterparts plus `RemoteHost` set. The pipeline calls these with
`p.SSH.Host`; the plain `BuildError`/`UpError` remain for any
host-less callers and for the existing code/kind mapping tests.

`PrintError` (internal/cli/errdisplay.go) resolves the hint from the
error before falling back to `kind.Hint()`:

1. If the error chain contains `no space left on device`:
   `host <host> is out of disk space: ssh in and run 'docker builder prune -af', then check 'docker system df'`
2. Else if the top `ExitError` is remote (`RemoteHost != ""`):
   `command failed on <host>: ssh in and run 'docker compose ps' / 'docker system df' to inspect`
3. Otherwise the current `kind.Hint()` is used unchanged (local
   docker errors keep `run 'pier status' ... then 'pier dev'`).

Update `internal/deploy/errors_test.go` and
`internal/cli/errdisplay_test.go` assertions to cover the three
paths.

## Section 2: `pier status <env>` remote health probe

New file `internal/deploy/status.go`:

```
func RemoteStatus(ctx context.Context, ssh SSHConfig, de config.DeployConfig,
                  health HealthConfig, r remoteRunner) (*StatusReport, error)
```

`StatusReport` is a struct with fields: containers (raw `docker
compose ps` output), disk (`df -h <path>` output), dockerDisk
(`docker system df` output), state (parsed `State` or nil), healthy
(bool).

`remoteRunner` is a small interface (`Run(ctx, cmd) ([]byte, []byte,
error)`) satisfied by `*Client` and by the test fake, mirroring the
existing runner-fake test pattern. The CLI dials a real `*Client`
and passes it; tests pass a fake.

CLI wiring in `internal/cli/status.go`:

- `pier status` (no args): unchanged local behavior.
- `pier status <env>`:
  - Look up `[deploy.<env>]` in pier.toml; error
    `no [deploy.%s] section in pier.toml` if missing (same message
    as deploy).
  - Dial over SSH (reuse `Dial` and `sshKeyPath()`).
  - Collect: `docker compose ps` in the deploy path, `df -h <path>`,
    `docker system df`, `cat .pier/state.json` (tolerate missing
    file -> no deploys yet).
  - Health probe: reuse `Probe` with a single-attempt config
    (`https://<domain>/up`, ~10 s timeout) instead of the 60 s retry
    loop.
  - Print in the same plain-text style as local status:

    ```
    project: test_web
    env:     production (host: <host>)
    services: [...]
    containers: ...
    disk: ...
    docker disk: ...
    health: OK (https://domain/up)
    last deploy: gitsha, 2026-08-02T... by user@host
    ```

Error handling:

- SSH dial failure: `SSHError` (KindSSH) so the ssh hint applies.
- A failed health probe is reported as `health: DOWN`, not a command
  failure.
- The command exits 0 as long as no probe command itself failed
  (SSH failure, docker compose failure).

Tests: `internal/deploy/status_test.go` with a fake runner covering:
happy path, missing state.json, health down, ssh/dial failure,
missing [deploy.<env>] section (cli-level test).

## Out of scope

- Strict SSH host-key verification (deferred in v1 design already).
- `pier status` iterating all deploy envs by default.
- Remote `pier dev`-style operations.

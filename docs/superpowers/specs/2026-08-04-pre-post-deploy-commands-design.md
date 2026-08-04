# Pre/Post Deploy Commands Design

Date: 2026-08-04
Status: Approved

## Problem

Users need to run commands in the app container around a production
deploy (e.g. `php artisan migrate --force` after the new release is
up, `php artisan down` before it switches). Today pier has no way to
express this; users must `ssh` in and run them manually.

## Goals

- Users declare a list of commands in pier.toml, per environment.
- Commands run inside the app container (`docker compose exec -T app`)
  on the deploy host, exactly like `pier exec <env> ...`.
- `before_deploy` commands run while the old container is still
  serving; `after_deploy` commands run against the new release.
- Commands run in order and stop at the first failure: a failing
  command aborts the deploy (exit code 7). `before_deploy` failures
  leave the old release serving; `after_deploy` failures roll back to
  the previous image.
- `before_deploy` is skipped on a first deploy, when the app
  container does not exist yet.
- `pier init` writes the new keys into pier.toml commented out, in the
  same style as the existing `# bind = "0.0.0.0"` line.

## Non-Goals

- No per-command `continue_on_error` flag — every command is
  fail-the-deploy on failure.
- No plain-host (non-container) command target.
- Hooks do not run during `pier rollback`; rollback stays a pure image
  switch.
- No pre/post hooks for the `dev` flow.
- No environment-variable interpolation inside command strings.

## Config Schema

`internal/config/config.go`, `DeployConfig` gains two fields:

```toml
[deploy.production]
host = "1.2.3.4"
user = "deploy"
path = "/srv/app"
branch = "main"
before_deploy = ["php artisan down"]                    # optional
after_deploy = ["php artisan migrate --force"]          # optional
```

- Both keys optional; default empty (nil).
- Each entry is a full command line string (e.g. `"php artisan
  migrate --force"`), tokenized into args before dispatch.
- Validation (`internal/config/parse.go`): every entry must tokenize
  to at least one non-empty arg; otherwise `ErrConfigInvalid` naming
  the offending env/key/entry.

## Command Splitting

New small shellwords-style tokenizer in `internal/config`
(`split.go`). It lives in config (not deploy) because config
validation tokenizes entries and `internal/deploy` already imports
`internal/config` — putting the tokenizer in deploy would create an
import cycle. The deploy pipeline calls the same function at
execution time.

- Splits a command line on whitespace, honoring single quotes,
  double quotes, and backslash escapes (no env expansion).
- Unknown/unterminated quote → error (surfaced at config-validation
  time, not at deploy time).
- The tokenized args are dispatched through the existing
  `remoteExecCommand(dir, args)` helper (`internal/deploy/shell.go`),
  which POSIX-quotes each arg and builds

  `cd <dir> && docker compose --env-file .env.production -f
  docker-compose.prod.yml exec -T app <args...>`

  — reusing the exact path `pier exec` uses, so quoting is
  consistent.

## Pipeline Placement

`internal/deploy/deploy.go` — two new phases between the existing
ones:

```
preflight → render → sync → build
  → before_deploy   (new; old container still serving)
  → up              (docker compose up -d --wait + nginx reload)
  → after_deploy    (new; new release is up)
  → health probe
  → commit
```

- Each phase uses `Logger.PhaseStart("before_deploy")` /
  `"after_deploy"` and streams command output via
  `Logger.Log("before_deploy", ...)` (consistent with the build
  phase's streaming).
- Commands run in listed order. The first failing command (non-zero
  remote exit status or transport error) aborts the phase and fails
  the deploy with exit code 7 (`ErrHooks`); no further commands run.
  `before_deploy` failures return before `up`, leaving the old
  release serving; `after_deploy` failures take the rollback path
  (retag previous image + re-`up`), like a failed health probe.
- Hooks only run when every preceding phase succeeded: a build or up
  failure aborts the pipeline (and up failure triggers rollback)
  before `after_deploy` runs; `before_deploy` never runs after a
  failed build.
- On a first deploy (no `.pier/state.json` on the remote) the app
  container does not exist when `before_deploy` would run, so the
  phase is skipped entirely (logged) instead of failing. Likewise, a
  first-deploy `after_deploy` or health failure has no previous image
  to roll back to: rollback is skipped (logged) and the failure
  itself is reported (exit code 7 for a failed hook) instead of a
  dead-end "no previous deploy to roll back to" error.
- `up` runs `docker compose up -d --wait --wait-timeout 120` so
  `after_deploy` hooks run against a ready stack: plain `up -d`
  returns as soon as containers start, and a fresh database volume
  still initializing refused connections from the first
  `after_deploy` command (`SQLSTATE[08006]`), which only worked when
  re-run manually minutes later. `--wait` blocks until every
  healthchecked service is healthy; the timeout bounds that wait.
- `before_deploy` is skipped entirely when the list is empty;
  likewise `after_deploy`.

## Execution Details

- Hooks run through the same SSH client as the rest of the pipeline
  (`client.RunStream`), so output streams to the TUI/JSON logger.
- Exit status of a failed command is captured from the remote session
  and included in the error (reuse `ssh.ExitError` handling; the
  "last output" tail already available via `Run`'s error wrapping).
- ctx cancellation aborts the in-flight hook, same as other phases.

## Generated pier.toml

`internal/cli/toml.go` `tomlEncode` renders, per `[deploy.<env>]`
table, after the existing keys (bind-style commented examples):

```toml
[deploy.production]
host = "1.2.3.4"
user = "deploy"
path = "/srv/app"
branch = "main"
# before_deploy = ["php artisan down"]              # uncomment: runs in the app container before the new release starts
# after_deploy = ["php artisan migrate --force"]    # uncomment: runs in the app container after the new release is up
```

## Tests

- `config` `split_test.go`: tokenizer unit tests — plain args,
  single/double quotes, escaped spaces, unterminated quote error,
  empty string error.
- `config` tests: valid lists decode; blank/empty entries fail
  validation with the env/key named.
- `deploy` pipeline tests: hooks run in order at the right pipeline
  position; a failing hook fails the deploy (before_deploy: abort
  before up, after_deploy: rollback path) and stops the remaining
  commands; empty lists skip the phases; before_deploy is skipped on
  a first deploy; rollback path does not invoke hooks.
- `cli` tests: generated pier.toml contains the commented
  `before_deploy`/`after_deploy` lines.
- `shell` tests: `remoteExecCommand` with multi-arg tokens builds the
  expected exec command (extend existing cases).

## Docs

README "deploy" section gains a short subsection describing
`before_deploy` / `after_deploy`: where they run (app container on the
deploy host), when (before up / after up before health probe), and
failure semantics (fail the deploy; after_deploy rolls back).

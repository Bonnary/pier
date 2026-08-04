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
- A failing command logs a warning and the deploy continues.
- `pier init` writes the new keys into pier.toml commented out, in the
  same style as the existing `# bind = "0.0.0.0"` line.

## Non-Goals

- No per-command `continue_on_error` flag — every command is
  warn-and-continue on failure.
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

New small shellwords-style tokenizer in `internal/deploy`
(`split.go`):

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
  → up              (docker compose up -d + nginx reload)
  → after_deploy    (new; new release is up)
  → health probe
  → commit
```

- Each phase uses `Logger.PhaseStart("before_deploy")` /
  `"after_deploy"` and streams command output via
  `Logger.Log("before_deploy", ...)` (consistent with the build
  phase's streaming).
- Commands run in listed order. On failure, log a warning with the
  remote exit status and continue with the next command; the deploy
  itself never aborts on hook failure.
- `before_deploy` is skipped entirely when the list is empty;
  likewise `after_deploy`.

## Execution Details

- Hooks run through the same SSH client as the rest of the pipeline
  (`client.RunStream`), so output streams to the TUI/JSON logger.
- Exit status of a failed command is captured from the remote session
  and included in the warning (reuse `ssh.ExitError` handling; the
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

- `split_test.go`: tokenizer unit tests — plain args, single/double
  quotes, escaped spaces, unterminated quote error, empty string
  error.
- `config` tests: valid lists decode; blank/empty entries fail
  validation with the env/key named.
- `deploy` pipeline tests: hooks run in order at the right pipeline
  position; a failing hook logs a warning and the pipeline continues;
  empty lists skip the phases; rollback path does not invoke hooks.
- `cli` tests: generated pier.toml contains the commented
  `before_deploy`/`after_deploy` lines.
- `shell` tests: `remoteExecCommand` with multi-arg tokens builds the
  expected exec command (extend existing cases).

## Docs

README "deploy" section gains a short subsection describing
`before_deploy` / `after_deploy`: where they run (app container on the
deploy host), when (before up / after up before health probe), and
failure semantics (warn + continue).

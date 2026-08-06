# pier init — Full Deploy Setup Design

> **Date:** 2026-08-06
> **Status:** Approved design (pending written-spec review)

## Goal

`pier init` currently asks only PHP version, Node version, and services, and
writes a bare `[deploy.production]` section (host/user/path/builder left for
later manual editing). This spec makes `pier init` walk the user through the
full deploy setup: deploy host, user, path, branch, and the build machine
(`builder` mode, with build server host/user/path when `build_server` is
chosen).

## Architecture

The builder question becomes the 4th state of the existing init TUI
(approach A — one continuous TUI, one quit point). Deploy target fields
that are free text (host, user, path, branch, build host/user/path) are
plain text prompts run after the TUI completes, using the existing `prompt`
helper pattern. The flow reuses the single-select picker already used by the
PHP/Node states and the builder labels from the buildmode command
(`host_server`, `local_machine`, `build_server`).

## Components

### 1. TUI — `internal/tui/init.go`

- New state `stateBuilder`, inserted after `stateServices` and before
  `stateDone`. The `initModel` gains a `builderPicker *Picker`
  (single-select over `host_server`, `local_machine`, `build_server`,
  pre-ticked at index 0 = `host_server`).
- Enter on `stateBuilder` sets `result.Builder` and quits, mirroring the
  existing states' behavior.
- `InitResult` gains a `Builder string` field: empty when aborted,
  otherwise exactly one of `host_server` / `local_machine` / `build_server`.
- `RunInit` gains the builder labels parameter:
  `RunInit(phpVersions, nodeVersions, services []string, builders []string)`
  (and `newInitModel` likewise). The CLI passes the same three labels the
  buildmode command uses.

### 2. CLI — `internal/cli/init.go`

- **Flags:** add `--builder`, `--host`, `--user`, `--path`, `--build-host`,
  `--build-user`, `--build-path` to `newInitCmd`. A flag value skips its
  prompt. The existing "any of --php/--node/--services set skips the TUI"
  condition extends to the new flags (a TUI run is only entered when none
  of the flag-driven questions need answering).
- **Deploy target prompts (after PHP/Node/services questions):** host,
  user, path — empty answer allowed and skips writing the key; branch with
  default `main`. Prompts use the existing `prompt` helper.
- **Build machine question:** answered by the TUI (or `--builder`, or the
  non-TUI fallback). When the result is `build_server`, prompt for build
  host, user, path with no defaults and reprompt until non-empty — the
  config validator rejects `builder = "build_server"` with any empty
  `build_*` field.
- **Non-TUI fallback:** when the TUI is unavailable, the builder question
  is a numbered text prompt (`1) host_server 2) local_machine
  3) build_server`, default 1), matching the existing plain-prompt
  fallbacks for PHP/Node/services.
- **Config write:** `[deploy.production]` is written unconditionally now
  (today it is written only when services > 0). It contains the collected
  non-empty fields: `Host`, `User`, `Path`, `Branch` (default `main` when
  the deploy section is created), `Builder`, `BuildHost`/`BuildUser`/
  `BuildPath` (build_server only), `Services`. `tomlEncode` already renders
  `builder` and `build_*` keys.
- **No new validation in init:** `config.Validate` is not called by init
  today and stays that way; a fully-empty deploy section is valid, and
  partially-filled sections are caught later by the deploy-time validator.

### 3. Tests

- `internal/tui/init_test.go`: builder state stores the picked value; abort
  during the builder state returns `Aborted`.
- `internal/cli/init_test.go`:
  - Full flag-driven run writes `builder`/`build_host`/`build_user`/
    `build_path` into pier.toml.
  - Scripted stdin prompts (host/user/path and build fields) produce the
    same output.
  - Empty host/user/path answers write no host keys.
  - `build_server` with empty build path reprompts.
  - Existing assertions that rely on "no deploy section when no services"
    are updated to the always-written deploy section.
- Docs: README `pier init` bullet and Commands table row gain the deploy
  questions and new flags; CHANGELOG entry under Unreleased.

## Data Flow

```
pier init [path]
  → detect Laravel, reject existing pier.toml
  → (flags set?) no:
      TUI: PHP → Node → Services → Builder   (q/Ctrl+C = abort, no write)
  → flags / fallback prompts fill missing values
  → text prompts: host, user, path (empty ok), branch [main]
  → builder == build_server? prompt build host/user/path (required)
  → build Config{Project, Stack, Deploy{production: collected fields}}
  → write pier.toml, generate dev/prod compose, patch vite, done
```

## Error Handling

- q/Ctrl+C anywhere in the TUI → `AbortedError()`, pier.toml not written
  (existing behavior preserved).
- Empty required build-server fields → reprompt while the prompt returns
  non-empty input. On EOF the `prompt` helper returns the default (empty
  for required fields), so a repeated EOF would spin: the reprompt loop
  counts consecutive EOF returns and breaks after 3, returning an error
  telling the user to run `pier buildmode` or edit pier.toml instead.
  (Simpler alternative if this proves overengineered in review: accept
  the empty value and let deploy-time validation report it — decided in
  implementation.)

## Out of Scope

- `pier buildmode` remains the way to change the builder later — no changes
  to it.
- No changes to deploy-time validation, bootstrap, or the pipeline.
- No TUI text-input form; free-text fields stay plain prompts.

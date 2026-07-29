# Error Handling Improvements — Design

Date: 2026-07-29
Status: Approved (brainstorming), pending spec review

## Problem

`pier` currently prints errors as a single line:

```
error: <top error message>
```

The codebase already wraps errors with `%w` and assigns distinct exit codes
(`0`, `1`, `2`, `3`, `4`, `5`, `130`) via `&ExitError{Code, Err}`, but the
wrapped cause chain is never shown, there is no error categorization, and no
guidance on what to do next. Users get something like `error: exit 1: build`
with no indication whether the problem is their `pier.toml`, Docker, SSH, the
network, or their own input.

The user wants errors that look like Rust `anyhow` / `eyre` output:
categorized, source chain visible, with actionable hints.

## Goals

1. Categorize every user-facing error so the category (Config, Docker, SSH,
   Network, User, Unknown) is visible in the output.
2. Show the full wrapped error chain (not just the top message).
3. Provide a short actionable hint per category.
4. Stay backward compatible with the existing `ExitError` shape and exit
   codes — no script-facing behavior change.
5. Color when stderr is a TTY, plain text otherwise.
6. Minimal churn: do not refactor every existing error call site; the
   existing constructors pick up sensible default categories.

## Non-goals

- No new external dependencies. ANSI codes written by hand.
- No backtrace support. Can be added later under `--verbose`.
- No change to the cobra command structure.
- No change to existing exit codes.
- No refactor of every `fmt.Errorf` call site.

## Design

### 1. Add `Kind` to `ExitError`

File: `internal/deploy/errors.go`

```go
type Kind int

const (
    KindUnknown Kind = iota
    KindConfig
    KindDocker
    KindSSH
    KindNetwork
    KindUser
)

type ExitError struct {
    Code int
    Kind Kind
    Err  error
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d: %v", e.Code, e.Err) }
func (e *ExitError) Unwrap() error { return e.Err }
```

`Kind` is the zero-value `KindUnknown` for any `ExitError` constructed
without a kind, so existing code that builds `&ExitError{Code: x, Err: y}`
directly still compiles and runs.

`(*ExitError).Is` is updated to also match by Kind when the target is one
of the package-level sentinel errors.

### 2. New constructors

Add wrappers in `internal/deploy/errors.go` and re-export them in
`internal/cli/errors.go`:

```go
// deploy/errors.go
func ConfigError(err error) error   { return &ExitError{Code: ExitGeneral, Kind: KindConfig, Err: err} }
func DockerError(err error) error   { return &ExitError{Code: ExitGeneral, Kind: KindDocker, Err: err} }
func SSHError(err error) error      { return &ExitError{Code: ExitGeneral, Kind: KindSSH, Err: err} }
func NetworkError(err error) error  { return &ExitError{Code: ExitGeneral, Kind: KindNetwork, Err: err} }
func UserError(err error) error     { return &ExitError{Code: ExitGeneral, Kind: KindUser, Err: err} }
```

### 3. Existing constructors pick up default Kinds

- `PreflightError` → `KindConfig` (preflight failures originate in
  `pier.toml` / `ssh` reachability).
- `BuildError`, `UpError`, `ExecDownError` → `KindDocker`.
- `AbortedError` → `KindUser`.

Behavior of the existing constructors (signature, exit code, return type)
does not change. Only the `Kind` field is set.

### 4. Pretty printer

New file: `internal/cli/errdisplay.go`

```go
// PrintError writes a categorized, multi-line rendering of err to w.
// When w is a TTY, output is colored; otherwise plain text. When verbose
// is true, the full unwrapped chain is shown; otherwise consecutive
// duplicates are collapsed.
func PrintError(w io.Writer, err error, verbose bool)
```

Output format (TTY example):

```
error[config]: pier.toml is invalid
  |
  |-> project.name is required
  |-> caused by: invalid pier.toml
  |
  = hint: see the [project] section in docs/superpowers/specs/2026-07-26-pier-design.md
```

Output format (plain, same input):

```
error[config]: pier.toml is invalid
  |
  |-> project.name is required
  |-> caused by: invalid pier.toml
  |
  = hint: see the [project] section in docs/superpowers/specs/2026-07-26-pier-design.md
```

(no ANSI codes when not a TTY — bytes are identical except for escapes)

Colors per Kind (TTY only):

| Kind     | Label     | Color  |
|----------|-----------|--------|
| Config   | `config`  | yellow |
| Docker   | `docker`  | red    |
| SSH      | `ssh`     | red    |
| Network  | `network` | red    |
| User     | `user`    | cyan   |
| Unknown  | (omitted) | red    |

Unknown is shown as `error: <msg>` with no bracket label, matching the
current behavior for non-`ExitError` errors.

The printer walks `errors.Unwrap` to build the source chain. The top
message is shown after `error[kind]:`. Each cause is prefixed with
`|-> `. The final cause is shown as `caused by: <msg>`. Duplicates are
collapsed unless `verbose` is true.

### 5. Hints

Each `Kind` has a `Hint() string` returning a single actionable line
pointing to the existing troubleshooting sections in `README.md` or the
project spec:

| Kind     | Hint |
|----------|------|
| Config   | `see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'` |
| Docker   | `run 'pier status' to see container state, then 'pier dev' to (re)start the stack` |
| SSH      | `verify ssh access: 'ssh deploy@<host>', check ~/.ssh/id_ed25519 perms (chmod 600)` |
| Network  | `check internet/VPN; 'docker pull <image>' manually to isolate registry vs DNS` |
| User     | (empty — the message is already self-explanatory) |
| Unknown  | (empty) |

The hint is rendered under the source chain as `= hint: <text>`. If the
hint is empty it is omitted entirely.

### 6. TTY detection

`PrintError` accepts `w io.Writer`. To detect TTY we cannot query `w`
directly (it's just an `io.Writer`), so we instead expose two paths:

- `PrintError(w io.Writer, err error, verbose bool)` — color when
  `isatty(os.Stderr)` returns true, plain otherwise. The TTY check is
  done in `main.go` (which has `os.Stderr`) and the result is passed in
  as an extra parameter, or computed lazily in the printer via a small
  helper that calls `os.Stderr` only when `w == os.Stderr`.

Decision: pass `color bool` from `main.go` (which knows the writer).
This keeps the printer pure and testable.

Final signature:

```go
func PrintError(w io.Writer, err error, verbose, color bool)
```

`main.go` calls:

```go
if err := root.Execute(); err != nil {
    cli.PrintError(os.Stderr, err, verbose, isTerminal(os.Stderr))
    os.Exit(cli.ExitCode(err))
}
```

`isTerminal` is implemented using `golang.org/x/term.IsTerminal`.
`golang.org/x/term` is currently a transitive dep in `go.sum`; we will
promote it to a direct dep in `go.mod`. If `x/term.IsTerminal` returns
false we also respect the `NO_COLOR` environment variable (see
https://no-color.org) — if `NO_COLOR` is set to any non-empty value,
treat the stream as non-terminal regardless of the `IsTerminal` result.

### 7. `main.go` change

Before:

```go
if err := root.Execute(); err != nil {
    fmt.Fprintln(os.Stderr, "error:", err)
    os.Exit(cli.ExitCode(err))
}
```

After:

```go
if err := root.Execute(); err != nil {
    cli.PrintError(os.Stderr, err, verbose, isatty(os.Stderr))
    os.Exit(cli.ExitCode(err))
}
```

Behavior change: a TTY user now sees a multi-line colored error with
hint. A non-TTY user (CI, logs) sees a multi-line plain-text error with
hint. The exit code is unchanged.

### 8. Tests

New tests in `internal/cli/errdisplay_test.go`:

- `TestPrintError_Config_Plain` — known config error, no color → exact
  expected string.
- `TestPrintError_Docker_Color` — color enabled → contains ANSI escape
  for red.
- `TestPrintError_Unknown_NoBracket` — non-`ExitError` error → no
  `[kind]` label.
- `TestPrintError_Chain` — error with three wrap levels → all three
  shown.
- `TestPrintError_Verbose` — same input, `verbose=true` shows
  duplicates that would otherwise collapse.
- `TestPrintError_NoHint` — `KindUser` → no hint line.
- `TestExitCode_Stable` — exit code is still 1/2/3/4/5/130 for the
  existing constructors (regression guard).

Update existing tests in `internal/cli/root_test.go` and
`cmd/pier/main_test.go` if any of them assert on the literal
`"error: ..."` output of `main.go`. (Spot-check: `root_test.go`
currently doesn't assert on stderr contents; `main_test.go` does not
exist as a meaningful test file beyond smoke.)

## File-level change list

1. `internal/deploy/errors.go` — add `Kind`, update `ExitError`, add
   `ConfigError`/`DockerError`/`SSHError`/`NetworkError`/`UserError`,
   default `Kind` on existing constructors, add `Hint()` method on
   `Kind`.
2. `internal/deploy/errors_test.go` — add tests for new constructors
   and `Is` behavior.
3. `internal/cli/errors.go` — re-export the new constructors and the
   `Kind` constants.
4. `internal/cli/errdisplay.go` (new) — `PrintError`, ANSI helpers,
   `isTerminal` wrapper (uses `golang.org/x/term`).
5. `internal/cli/errdisplay_test.go` (new) — display tests.
6. `cmd/pier/main.go` — swap the one-line printer for `PrintError`.
7. `go.mod` / `go.sum` — promote `golang.org/x/term` to a direct dep.

No other files need to change. Existing call sites of the existing
constructors continue to work.

## Open questions

None. All resolved during brainstorming:

- Scope: Display + categorization.
- Categories: Config, Docker, SSH, Network, User.
- Color policy: TTY-only.

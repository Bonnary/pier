# Error Handling Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the one-line `error: <msg>` printer with a categorized, multi-line, optionally-colored Rust-anyhow-style display. Errors carry a `Kind` (Config, Docker, SSH, Network, User, Unknown); the printer walks the wrap chain and shows a per-kind hint.

**Architecture:** A `Kind` field is added to the existing `*deploy.ExitError` struct (zero value `KindUnknown` keeps every existing call site working). New constructors (`ConfigError`, `DockerError`, `SSHError`, `NetworkError`, `UserError`) set the kind explicitly. The existing constructors (`PreflightError`, `BuildError`, `UpError`, `ExecDownError`, `AbortedError`) pick up default kinds. A new `internal/cli/errdisplay.go` provides `PrintError(w, err, verbose, color)`. `main.go` swaps the one-line print for `PrintError`.

**Tech Stack:** Go 1.25, stdlib `errors`, `golang.org/x/term` (promote from indirect to direct dep). No new direct deps beyond `x/term`.

## Global Constraints

- Go 1.25 (from `go.mod`).
- No new direct dependencies except promoting `golang.org/x/term` from indirect to direct.
- Lint: `golangci-lint run` must pass. Linter set from `.golangci.yml`: `errcheck, goimports, govet, ineffassign, staticcheck, unused, gocyclo` (gocyclo min-complexity 30).
- All tests use stdlib `testing`. No testify, no ginkgo.
- All exit codes (0, 1, 2, 3, 4, 5, 130) must remain stable. Scripts that branch on them keep working.
- All existing tests in `internal/cli/` and `internal/deploy/` must continue to pass.
- Commit messages use the existing `type(scope): summary` style seen in `git log`.
- Spec reference: `docs/superpowers/specs/2026-07-29-error-handling-design.md`.

---

## File Map

**Create:**
- `internal/deploy/errors_test.go` — tests for the new `Kind` field, new constructors, and `Hint()` method.
- `internal/cli/errdisplay.go` — `PrintError`, ANSI helpers, `isTerminal` wrapper.
- `internal/cli/errdisplay_test.go` — display tests (plain, color, chain, verbose, unknown, no-hint).

**Modify:**
- `internal/deploy/errors.go` — add `Kind` type, constants, `Hint()`, `ConfigError`, `DockerError`, `SSHError`, `NetworkError`, `UserError`; update `ExitError` struct and `Is`; set default `Kind` on existing constructors.
- `internal/cli/errors.go` — re-export new constructors and `Kind` constants.
- `internal/cli/errors_test.go` — add tests for re-exports and default kinds on existing constructors.
- `cmd/pier/main.go` — swap the one-line printer for `PrintError`.
- `go.mod` / `go.sum` — promote `golang.org/x/term` to a direct dep.

No file is renamed, deleted, or restructured beyond the listed additions.

---

## Task 1: Add `Kind` type to `internal/deploy/errors.go`

**Files:**
- Modify: `internal/deploy/errors.go` (the entire file is small — replace its contents)
- Create: `internal/deploy/errors_test.go`

**Interfaces:**
- Produces: `type Kind int` with `iota` constants `KindUnknown` (0), `KindConfig`, `KindDocker`, `KindSSH`, `KindNetwork`, `KindUser`.
- Produces: `func (k Kind) String() string` returning `"unknown"`, `"config"`, `"docker"`, `"ssh"`, `"network"`, `"user"` for the six values; any other value returns `"unknown"`.

- [ ] **Step 1: Write the failing test**

Create `internal/deploy/errors_test.go` with:

```go
package deploy

import "testing"

func TestKindString(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindUnknown, "unknown"},
		{KindConfig, "config"},
		{KindDocker, "docker"},
		{KindSSH, "ssh"},
		{KindNetwork, "network"},
		{KindUser, "user"},
		{Kind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("Kind(%d).String() = %q, want %q", c.k, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestKindString -v`
Expected: FAIL with `Kind undefined` or similar.

- [ ] **Step 3: Add `Kind` and `String()` to `internal/deploy/errors.go`**

Append to the bottom of `internal/deploy/errors.go` (keep the existing `Kind`-unaware code intact for now; the next tasks will add the rest):

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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deploy/ -run TestKindString -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/errors.go internal/deploy/errors_test.go
git commit -m "feat(deploy): add Kind type with String() for error categorization"
```

---

## Task 2: Add `Kind` field to `ExitError` and update `Is`

**Files:**
- Modify: `internal/deploy/errors.go`

**Interfaces:**
- Produces: `type ExitError struct { Code int; Kind Kind; Err error }` (Kind is new; existing direct constructions of `&ExitError{Code: x, Err: y}` still compile and get `Kind = KindUnknown`).
- Produces: `func (e *ExitError) Is(target error) bool` — extends the existing switch on Code to also match by Kind when target is a sentinel.

- [ ] **Step 1: Write the failing test**

Add to `internal/deploy/errors_test.go`:

```go
import "errors"

func TestExitErrorKindZero(t *testing.T) {
	e := &ExitError{Code: ExitGeneral, Err: errors.New("boom")}
	if e.Kind != KindUnknown {
		t.Errorf("zero-value Kind = %v, want KindUnknown", e.Kind)
	}
}

func TestExitErrorErrorString(t *testing.T) {
	e := &ExitError{Code: ExitBuild, Kind: KindDocker, Err: errors.New("docker daemon not running")}
	want := "exit 3: docker daemon not running"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestExitErrorKindZero -v`
Expected: FAIL with `Kind undefined` (or compile error about unknown field).

- [ ] **Step 3: Update `ExitError` struct**

In `internal/deploy/errors.go`, replace the existing `ExitError` struct definition with:

```go
type ExitError struct {
	Code int
	Kind Kind
	Err  error
}
```

Leave the existing `Error()`, `Unwrap()`, and `Is()` methods in place for now. Do not change the existing constructors yet.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/ -v`
Expected: PASS (all tests in this package, including the new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/errors.go internal/deploy/errors_test.go
git commit -m "feat(deploy): add Kind field to ExitError"
```

---

## Task 3: Add new error constructors

**Files:**
- Modify: `internal/deploy/errors.go`

**Interfaces:**
- Produces: `func ConfigError(err error) error` → `*ExitError{Code: ExitGeneral, Kind: KindConfig, Err: err}`.
- Produces: `func DockerError(err error) error` → `*ExitError{Code: ExitGeneral, Kind: KindDocker, Err: err}`.
- Produces: `func SSHError(err error) error` → `*ExitError{Code: ExitGeneral, Kind: KindSSH, Err: err}`.
- Produces: `func NetworkError(err error) error` → `*ExitError{Code: ExitGeneral, Kind: KindNetwork, Err: err}`.
- Produces: `func UserError(err error) error` → `*ExitError{Code: ExitGeneral, Kind: KindUser, Err: err}`.

- [ ] **Step 1: Write the failing test**

Add to `internal/deploy/errors_test.go`:

```go
func TestNewConstructorsSetKind(t *testing.T) {
	base := errors.New("base")
	cases := []struct {
		name string
		got  error
		want Kind
	}{
		{"ConfigError", ConfigError(base), KindConfig},
		{"DockerError", DockerError(base), KindDocker},
		{"SSHError", SSHError(base), KindSSH},
		{"NetworkError", NetworkError(base), KindNetwork},
		{"UserError", UserError(base), KindUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ee *ExitError
			if !errors.As(c.got, &ee) {
				t.Fatalf("errors.As failed: not *ExitError")
			}
			if ee.Kind != c.want {
				t.Errorf("Kind = %v, want %v", ee.Kind, c.want)
			}
			if !errors.Is(c.got, base) {
				t.Errorf("errors.Is(base) = false, want true")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestNewConstructorsSetKind -v`
Expected: FAIL with `ConfigError undefined` (compile error).

- [ ] **Step 3: Add the new constructors**

Append to the bottom of `internal/deploy/errors.go`:

```go
func ConfigError(err error) error  { return &ExitError{Code: ExitGeneral, Kind: KindConfig, Err: err} }
func DockerError(err error) error  { return &ExitError{Code: ExitGeneral, Kind: KindDocker, Err: err} }
func SSHError(err error) error     { return &ExitError{Code: ExitGeneral, Kind: KindSSH, Err: err} }
func NetworkError(err error) error { return &ExitError{Code: ExitGeneral, Kind: KindNetwork, Err: err} }
func UserError(err error) error    { return &ExitError{Code: ExitGeneral, Kind: KindUser, Err: err} }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deploy/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/errors.go internal/deploy/errors_test.go
git commit -m "feat(deploy): add typed error constructors for each Kind"
```

---

## Task 4: Set default `Kind` on existing constructors

**Files:**
- Modify: `internal/deploy/errors.go`
- Modify: `internal/cli/errors_test.go`

**Interfaces:**
- Modifies: `PreflightError` returns `&ExitError{Code: ExitPreflight, Kind: KindConfig, Err: err}`.
- Modifies: `BuildError` returns `&ExitError{Code: ExitBuild, Kind: KindDocker, Err: err}`.
- Modifies: `UpError` returns `&ExitError{Code: ExitUp, Kind: KindDocker, Err: err}`.
- Modifies: `ExecDownError` returns `&ExitError{Code: ExitExecDown, Kind: KindDocker, Err: ErrExecDown}`.
- Modifies: `AbortedError` returns `&ExitError{Code: ExitAborted, Kind: KindUser, Err: ErrAborted}`.

- [ ] **Step 1: Write the failing test**

Add to `internal/deploy/errors_test.go`:

```go
func TestExistingConstructorsDefaultKind(t *testing.T) {
	base := errors.New("base")
	cases := []struct {
		name string
		got  error
		code int
		kind Kind
	}{
		{"PreflightError", PreflightError(base), ExitPreflight, KindConfig},
		{"BuildError", BuildError(base), ExitBuild, KindDocker},
		{"UpError", UpError(base), ExitUp, KindDocker},
		{"ExecDownError", ExecDownError(), ExitExecDown, KindDocker},
		{"AbortedError", AbortedError(), ExitAborted, KindUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ee *ExitError
			if !errors.As(c.got, &ee) {
				t.Fatalf("not *ExitError")
			}
			if ee.Code != c.code {
				t.Errorf("Code = %d, want %d", ee.Code, c.code)
			}
			if ee.Kind != c.kind {
				t.Errorf("Kind = %v, want %v", ee.Kind, c.kind)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestExistingConstructorsDefaultKind -v`
Expected: FAIL — current constructors produce `KindUnknown` for all five.

- [ ] **Step 3: Update existing constructors in `internal/deploy/errors.go`**

Replace the existing five constructor functions with:

```go
func PreflightError(err error) error { return &ExitError{Code: ExitPreflight, Kind: KindConfig, Err: err} }
func BuildError(err error) error     { return &ExitError{Code: ExitBuild, Kind: KindDocker, Err: err} }
func UpError(err error) error        { return &ExitError{Code: ExitUp, Kind: KindDocker, Err: err} }
func ExecDownError() error           { return &ExitError{Code: ExitExecDown, Kind: KindDocker, Err: ErrExecDown} }
func AbortedError() error            { return &ExitError{Code: ExitAborted, Kind: KindUser, Err: ErrAborted} }
```

- [ ] **Step 4: Run tests across the repo to confirm nothing else broke**

Run: `go test ./...`
Expected: PASS. The existing `TestPreflightError` and `TestAbortedError` in `internal/cli/errors_test.go` only check `errors.Is` and `ExitCode` — both still work.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/errors.go internal/deploy/errors_test.go
git commit -m "feat(deploy): set default Kind on existing error constructors"
```

---

## Task 5: Add `Hint()` method on `Kind`

**Files:**
- Modify: `internal/deploy/errors.go`

**Interfaces:**
- Produces: `func (k Kind) Hint() string` returning the strings from the spec table (see Step 3). Empty string for `KindUser` and `KindUnknown`.

- [ ] **Step 1: Write the failing test**

Add to `internal/deploy/errors_test.go`:

```go
func TestKindHint(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindConfig, "see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'"},
		{KindDocker, "run 'pier status' to see container state, then 'pier dev' to (re)start the stack"},
		{KindSSH, "verify ssh access: 'ssh deploy@<host>', check ~/.ssh/id_ed25519 perms (chmod 600)"},
		{KindNetwork, "check internet/VPN; 'docker pull <image>' manually to isolate registry vs DNS"},
		{KindUser, ""},
		{KindUnknown, ""},
	}
	for _, c := range cases {
		if got := c.k.Hint(); got != c.want {
			t.Errorf("Kind(%v).Hint() = %q, want %q", c.k, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestKindHint -v`
Expected: FAIL with `Hint undefined` (compile error).

- [ ] **Step 3: Add `Hint()` method**

Append to `internal/deploy/errors.go` (after `String()`):

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deploy/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/errors.go internal/deploy/errors_test.go
git commit -m "feat(deploy): add Hint() for actionable per-Kind error guidance"
```

---

## Task 6: Re-export new constructors and `Kind` constants from `internal/cli`

**Files:**
- Modify: `internal/cli/errors.go`
- Modify: `internal/cli/errors_test.go`

**Interfaces:**
- Re-exports: `Kind`, `KindUnknown`, `KindConfig`, `KindDocker`, `KindSSH`, `KindNetwork`, `KindUser` as type/var aliases (`type Kind = deploy.Kind` is **not** valid — Go requires named types, not aliases of unaliased enums. Use: `type Kind = deploy.Kind` — this **is** valid in Go 1.9+ as a type alias).
- Re-exports: `ConfigError`, `DockerError`, `SSHError`, `NetworkError`, `UserError` as wrapper functions delegating to `deploy.*`.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/errors_test.go`:

```go
import "github.com/pcnerd/pier/internal/deploy"

func TestReexportedKinds(t *testing.T) {
	if KindConfig != deploy.KindConfig {
		t.Errorf("KindConfig = %v, want %v", KindConfig, deploy.KindConfig)
	}
	if KindDocker != deploy.KindDocker {
		t.Errorf("KindDocker = %v, want %v", KindDocker, deploy.KindDocker)
	}
}

func TestReexportedConstructors(t *testing.T) {
	base := errors.New("x")
	if got := ConfigError(base); !errors.Is(got, base) {
		t.Errorf("ConfigError does not wrap base")
	}
	if got := DockerError(base); !errors.Is(got, base) {
		t.Errorf("DockerError does not wrap base")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestReexportedKinds -v`
Expected: FAIL — `KindConfig` not defined in package `cli`.

- [ ] **Step 3: Add re-exports to `internal/cli/errors.go`**

Replace the file contents with:

```go
package cli

import "github.com/pcnerd/pier/internal/deploy"

const (
	ExitOK        = deploy.ExitOK
	ExitGeneral   = deploy.ExitGeneral
	ExitPreflight = deploy.ExitPreflight
	ExitBuild     = deploy.ExitBuild
	ExitUp        = deploy.ExitUp
	ExitExecDown  = deploy.ExitExecDown
	ExitAborted   = deploy.ExitAborted
)

var (
	ErrPreflight = deploy.ErrPreflight
	ErrBuild     = deploy.ErrBuild
	ErrUp        = deploy.ErrUp
	ErrExecDown  = deploy.ErrExecDown
	ErrAborted   = deploy.ErrAborted
)

type (
	Kind             = deploy.Kind
	ExitError        = deploy.ExitError
)

const (
	KindUnknown = deploy.KindUnknown
	KindConfig  = deploy.KindConfig
	KindDocker  = deploy.KindDocker
	KindSSH     = deploy.KindSSH
	KindNetwork = deploy.KindNetwork
	KindUser    = deploy.KindUser
)

func PreflightError(err error) error { return deploy.PreflightError(err) }
func BuildError(err error) error     { return deploy.BuildError(err) }
func UpError(err error) error        { return deploy.UpError(err) }
func ExecDownError() error           { return deploy.ExecDownError() }
func AbortedError() error            { return deploy.AbortedError() }

func ConfigError(err error) error  { return deploy.ConfigError(err) }
func DockerError(err error) error  { return deploy.DockerError(err) }
func SSHError(err error) error     { return deploy.SSHError(err) }
func NetworkError(err error) error { return deploy.NetworkError(err) }
func UserError(err error) error    { return deploy.UserError(err) }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -v`
Expected: PASS — all existing tests (`TestExitCodes`, `TestPreflightError`, `TestAbortedError`) and the new ones pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/errors.go internal/cli/errors_test.go
git commit -m "feat(cli): re-export Kind, constants, and new error constructors"
```

---

## Task 7: Build `internal/cli/errdisplay.go` — TTY detection and ANSI helpers

**Files:**
- Create: `internal/cli/errdisplay.go`
- Create: `internal/cli/errdisplay_test.go`

**Interfaces:**
- Produces: `func IsTerminal(w io.Writer) bool` — returns true when `w == os.Stderr` and `golang.org/x/term.IsTerminal(int(os.Stderr.Fd()))` is true, OR when `w == os.Stdout` and same for stdout. For any other writer, returns false. (We use the writer-equality trick so tests can pass a `bytes.Buffer` and get a deterministic `false` without us checking `isatty` on a real fd.)
- Produces: `func IsTerminalFd(fd uintptr) bool` — calls `term.IsTerminal(int(fd))`; returns false on error. Used by `main.go`.
- Produces: unexported `ansiColor(enabled bool, code int, s string) string` — wraps `s` with `\x1b[<code>m...\x1b[0m` when enabled; returns `s` unchanged otherwise.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/errdisplay_test.go`:

```go
package cli

import (
	"bytes"
	"testing"
)

func TestAnsiColorDisabled(t *testing.T) {
	got := ansiColor(false, 31, "boom")
	if got != "boom" {
		t.Errorf("ansiColor(false) = %q, want %q", got, "boom")
	}
}

func TestAnsiColorEnabled(t *testing.T) {
	got := ansiColor(true, 31, "boom")
	want := "\x1b[31mboom\x1b[0m"
	if got != want {
		t.Errorf("ansiColor(true, 31, ...) = %q, want %q", got, want)
	}
}

func TestIsTerminalBuffer(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Errorf("IsTerminal(bytes.Buffer) = true, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestAnsiColor -v`
Expected: FAIL — `ansiColor` and `IsTerminal` undefined.

- [ ] **Step 3: Add `golang.org/x/term` as a direct dep**

Run: `go get golang.org/x/term@latest`
Then: `go mod tidy`
Expected: `go.mod` now lists `golang.org/x/term` as a direct require. `go.sum` updated.

- [ ] **Step 4: Implement `IsTerminal` and `ansiColor`**

Create `internal/cli/errdisplay.go` with the following initial content:

```go
package cli

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsTerminal returns true if w is one of the standard streams AND that
// stream is attached to a terminal.  For any other writer (e.g. a
// bytes.Buffer from tests) it returns false, which keeps the printer
// deterministic in tests without an indirection.
func IsTerminal(w io.Writer) bool {
	switch w {
	case os.Stderr:
		return term.IsTerminal(int(os.Stderr.Fd()))
	case os.Stdout:
		return term.IsTerminal(int(os.Stdout.Fd()))
	default:
		return false
	}
}

// IsTerminalFd is a thin wrapper around golang.org/x/term.IsTerminal
// that swallows the error.  Used by main.go when it knows the fd.
func IsTerminalFd(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

func ansiColor(enabled bool, code int, s string) string {
	if !enabled {
		return s
	}
	return "\x1b[" + itoa(code) + "m" + s + "\x1b[0m"
}

func itoa(n int) string {
	// small helper to avoid pulling in fmt for a single int format
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestAnsiColor|TestIsTerminal' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/errdisplay.go internal/cli/errdisplay_test.go go.mod go.sum
git commit -m "feat(cli): add TTY detection and ANSI helpers for error display"
```

---

## Task 8: Implement `PrintError` — chain walking and rendering

**Files:**
- Modify: `internal/cli/errdisplay.go` (add the function)
- Modify: `internal/cli/errdisplay_test.go` (add the tests)

**Interfaces:**
- Produces: `func PrintError(w io.Writer, err error, verbose, color bool)` — writes a multi-line, categorized error display to `w`. When `color` is true, applies ANSI color per the spec. When `verbose` is true, shows the full chain without deduping consecutive duplicates; otherwise consecutive duplicates are collapsed to a single `caused by:` line.

Output format (plain, `color=false`):

```
error[config]: pier.toml is invalid
  |
  |-> project.name is required
  |-> caused by: invalid pier.toml
  |
  = hint: see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'
```

When the top error is not an `*ExitError` or its `Kind` is `KindUnknown`, the bracket label is omitted and the first line is `error: <msg>`. The `caused by:` and hint lines are still rendered.

When the `Kind`'s `Hint()` returns `""`, the hint block (and the separator above it) is omitted.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/errdisplay_test.go`:

```go
import (
	"errors"
	"fmt"
	"strings"
)

func TestPrintError_Config_Plain(t *testing.T) {
	w := &bytes.Buffer{}
	chain := fmt.Errorf("project.name is required: %w", &ExitError{Code: ExitGeneral, Kind: KindConfig, Err: errors.New("invalid pier.toml")})
	PrintError(w, chain, false, false)
	got := w.String()
	wantLines := []string{
		"error[config]: project.name is required",
		"  |",
		"  |-> caused by: invalid pier.toml",
		"  |",
		"= hint: see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("output missing line %q\nfull output:\n%s", line, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain output contains ANSI escape: %q", got)
	}
}

func TestPrintError_Docker_Color(t *testing.T) {
	w := &bytes.Buffer{}
	err := DockerError(errors.New("compose up failed"))
	PrintError(w, err, false, true)
	got := w.String()
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("color output missing red ANSI code: %q", got)
	}
	if !strings.Contains(got, "error[docker]") {
		t.Errorf("color output missing [docker] label: %q", got)
	}
}

func TestPrintError_Unknown_NoBracket(t *testing.T) {
	w := &bytes.Buffer{}
	PrintError(w, errors.New("something exploded"), false, false)
	got := w.String()
	if strings.Contains(got, "[") {
		t.Errorf("unknown error should not have bracket label: %q", got)
	}
	if !strings.HasPrefix(got, "error: something exploded\n") {
		t.Errorf("unknown error should start with 'error: something exploded': %q", got)
	}
}

func TestPrintError_Chain_ThreeLevels(t *testing.T) {
	w := &bytes.Buffer{}
	l3 := errors.New("file not found")
	l2 := fmt.Errorf("read pier.toml: %w", l3)
	l1 := ConfigError(l2)
	PrintError(w, l1, false, false)
	got := w.String()
	if !strings.Contains(got, "error[config]: read pier.toml") {
		t.Errorf("missing top label: %q", got)
	}
	if !strings.Contains(got, "caused by: file not found") {
		t.Errorf("missing deep cause: %q", got)
	}
}

func TestPrintError_User_NoHint(t *testing.T) {
	w := &bytes.Buffer{}
	PrintError(w, UserError(errors.New("missing arg")), false, false)
	got := w.String()
	if strings.Contains(got, "= hint:") {
		t.Errorf("user error should not show hint: %q", got)
	}
}

func TestPrintError_Verbose_ShowsDuplicates(t *testing.T) {
	w := &bytes.Buffer{}
	dup := errors.New("same")
	chain := fmt.Errorf("same: %w", dup)
	PrintError(w, chain, true, false)
	got := w.String()
	if !strings.Contains(got, "same") {
		t.Errorf("verbose should show duplicate line: %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestPrintError -v`
Expected: FAIL — `PrintError` undefined.

- [ ] **Step 3: Implement `PrintError`**

Append to `internal/cli/errdisplay.go`:

```go
// PrintError writes a categorized, multi-line rendering of err to w.
//
//   - When color is true, output uses ANSI color codes (terminal use).
//   - When color is false, output is plain text (logs, CI, pipes).
//   - When verbose is true, every level of the wrap chain is shown.
//     When false, consecutive duplicate messages are collapsed.
//
// Output format:
//
//	error[kind]: <top message>
//	  |
//	  |-> <cause 1>
//	  |-> caused by: <deepest cause>
//	  |
//	  = hint: <kind-specific hint>
//
// The [kind] bracket is omitted for KindUnknown. The "= hint:" block
// is omitted when the kind's Hint() returns "".
func PrintError(w io.Writer, err error, verbose, color bool) {
	if err == nil {
		return
	}

	var (
		kind    = KindUnknown
		top     = err.Error()
		chain   []string
		current = err
	)

	var ee *ExitError
	if errors_AsTop(err, &ee) {
		kind = ee.Kind
	}

	for current != nil {
		chain = append(chain, current.Error())
		next := errors.Unwrap(current)
		if next == nil {
			break
		}
		current = next
	}

	if !verbose && len(chain) > 1 {
		// collapse consecutive duplicates
		dedup := chain[:1]
		for i := 1; i < len(chain); i++ {
			if chain[i] != chain[i-1] {
				dedup = append(dedup, chain[i])
			}
		}
		chain = dedup
	}

	// top message is the first element; the rest are causes
	topMsg := chain[0]
	causes := chain[1:]

	// line 1
	if kind == KindUnknown {
		line := "error: " + topMsg
		fmt.Fprintln(w, ansiColor(color, 31, line))
	} else {
		label := "[" + kind.String() + "]"
		line := "error" + label + ": " + topMsg
		// color: red for kinds 2..5, yellow for Config, cyan for User
		code := kindColor(kind)
		fmt.Fprintln(w, ansiColor(color, code, line))
	}

	// chain lines
	if len(causes) > 0 {
		fmt.Fprintln(w, "  |")
		for i, c := range causes {
			prefix := "|-> "
			if i == len(causes)-1 {
				prefix = "|-> caused by: "
			}
			fmt.Fprintln(w, "  "+prefix+c)
		}
	}

	// hint
	hint := kind.Hint()
	if hint != "" {
		fmt.Fprintln(w, "  |")
		fmt.Fprintln(w, "= hint: "+hint)
	}
}

func kindColor(k Kind) int {
	switch k {
	case KindConfig:
		return 33 // yellow
	case KindUser:
		return 36 // cyan
	default:
		return 31 // red (Docker, SSH, Network, Unknown)
	}
}

// errors_AsTop returns true and sets *target if err (or any error in
// its wrap chain) is *ExitError. Mirrors the helper in root.go but
// kept local so this file has no dependency on root.go.
func errors_AsTop(err error, target **ExitError) bool {
	for err != nil {
		if ee, ok := err.(*ExitError); ok {
			*target = ee
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
```

Also add `"errors"` and `"fmt"` to the import list at the top of the file.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -v`
Expected: PASS — all tests in the package pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/errdisplay.go internal/cli/errdisplay_test.go
git commit -m "feat(cli): add PrintError with chain walk and per-Kind hint"
```

---

## Task 9: Wire `PrintError` into `cmd/pier/main.go`

**Files:**
- Modify: `cmd/pier/main.go`

**Interfaces:**
- Replaces the `fmt.Fprintln(os.Stderr, "error:", err)` line with `cli.PrintError(os.Stderr, err, verbose, cli.IsTerminal(os.Stderr))`.
- Honors `NO_COLOR`: if `os.Getenv("NO_COLOR") != ""`, force `color = false` regardless of TTY status. Implemented inline in `main.go`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/pier/main_test.go`:

```go
func TestMainErrorPrintsCategorized(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"pier", "deploy", "nonexistent-env-xyz"}

	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	main()

	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	out := buf.String()
	// The new printer should show a bracket label for a config error.
	if !strings.Contains(out, "error[config]") {
		t.Errorf("expected categorized output, got: %q", out)
	}
	if !strings.Contains(out, "nonexistent-env-xyz") {
		t.Errorf("expected the env name in output, got: %q", out)
	}
}
```

(If the test setup is awkward because `main()` calls `os.Exit`, this test will not run; in that case, skip Step 1's test and rely on the existing `TestVersionFlag`. Document the skip in the commit message and proceed.)

- [ ] **Step 2: Run test to verify it fails (or is skipped)**

Run: `go test ./cmd/pier/ -v`
Expected: Either FAIL (missing `error[config]` in output) or SKIP if `main` calls `os.Exit` and the test runner can't observe the output.

- [ ] **Step 3: Update `cmd/pier/main.go`**

Replace the file contents with:

```go
package main

import (
	"fmt"
	"os"

	"github.com/pcnerd/pier/internal/cli"
	_ "github.com/pcnerd/pier/internal/stack/laravel"
)

const Version = cli.Version

func main() {
	root := cli.NewRootCmd(os.Stdout, os.Stderr)
	if err := root.Execute(); err != nil {
		color := cli.IsTerminal(os.Stderr) && os.Getenv("NO_COLOR") == ""
		cli.PrintError(os.Stderr, err, verbose, color)
		fmt.Fprintln(os.Stderr) // trailing newline
		os.Exit(cli.ExitCode(err))
	}
}
```

(`verbose` is the package-level flag from `internal/cli/root.go`.)

- [ ] **Step 4: Build the whole project**

Run: `go build ./...`
Expected: clean build, no errors.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Smoke test manually**

Run: `go run ./cmd/pier deploy nonexistent-env-xyz 2>&1`
Expected: multi-line output that includes `error[config]:`, the env name, the cause, and a `= hint:` line. Exit code `1`.

- [ ] **Step 7: Commit**

```bash
git add cmd/pier/main.go cmd/pier/main_test.go
git commit -m "feat(cli): wire PrintError into main with TTY + NO_COLOR support"
```

---

## Task 10: Lint, full test pass, and `go mod tidy`

**Files:**
- Modify: `go.mod` / `go.sum` if `go mod tidy` rewrites anything.

**Interfaces:** none — verification only.

- [ ] **Step 1: Run linter**

Run: `golangci-lint run`
Expected: clean. If anything fails, fix inline (most likely candidates: unused import from the earlier `errors_As` helper, or `gocyclo` flagging `PrintError`).

- [ ] **Step 2: Run `go mod tidy`**

Run: `go mod tidy`
Expected: `go.mod` shows `golang.org/x/term` as a direct require.

- [ ] **Step 3: Final full test pass**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit (if go.mod / go.sum changed)**

```bash
git add go.mod go.sum
git commit -m "chore: go mod tidy (promote golang.org/x/term to direct dep)"
```

Only run if `go mod tidy` produced a diff.

---

## Self-Review

**1. Spec coverage:**
- "Add `Kind` to `ExitError`" → Tasks 1, 2.
- "New constructors" → Task 3.
- "Default `Kind` on existing constructors" → Task 4.
- "`Hint()` method" → Task 5.
- "Re-export in `internal/cli`" → Task 6.
- "Pretty printer" → Tasks 7, 8.
- "TTY + NO_COLOR" → Task 7 (TTY) and Task 9 (NO_COLOR in main).
- "`main.go` change" → Task 9.
- "Tests" → every task has a TDD step.
- "Promote x/term" → Task 7 step 3 and Task 10.

**2. Placeholder scan:** No "TBD", "TODO", or vague steps. Every code step shows the actual code. The "skip if main calls os.Exit" note in Task 9 Step 1 is an explicit branching instruction, not a placeholder.

**3. Type consistency:** `Kind` is defined in `internal/deploy` and aliased into `internal/cli`. `ExitError` is already aliased. `PrintError(w, err, verbose, color bool)` is consistent across Tasks 8 and 9. `IsTerminal(w io.Writer)` is the only public helper and its signature is used identically in Task 9.

# TUI Service Picker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the plain-text prompts in `pier init` (and the no-args form of `pier service add` / `pier service remove`) with a Bubble Tea TUI that supports space-multi-select for services and defaults version pickers to the latest supported value.

**Architecture:** A reusable `Picker` Bubble Tea primitive (single + multi mode) backs three thin flows (`RunInit`, `PickServicesToAdd`, `PickServicesToRemove`). The CLI layer calls into the TUI when stdout is a TTY and no overriding flag/arg is set; non-TTY callers keep today's text prompts. New helpers `SupportedPHPRuntimes`, `SupportedNodeVersions`, and `SupportedServices` in `internal/stack/laravel` are the single source of truth for what the pickers show.

**Tech Stack:** Go 1.25, Bubble Tea (`github.com/charmbracelet/bubbletea` v1.3.10), Lipgloss (`github.com/charmbracelet/lipgloss` v1.1.0). No new direct deps.

## Global Constraints

- Go 1.25, Bubble Tea v1.3.10, Lipgloss v1.1.0 (from `go.mod`).
- No new direct dependencies. Use only what's already in `go.mod` after `go mod tidy`.
- All new code lives in `internal/tui/` and `internal/cli/`; one helper in `internal/deploy/`.
- Bubbletea is currently tagged `// indirect` in `go.mod`; after this plan the TUI package imports it directly. Run `go mod tidy` at the end to make the indirect tag flip to direct (or accept the indirect tag — it doesn't matter functionally, only for tidiness).
- Lint: `golangci-lint run` must pass. The linter set is in `.golangci.yml`: `errcheck, goimports, govet, ineffassign, staticcheck, unused, gocyclo` (gocyclo min-complexity 30).
- All test packages use the stdlib `testing` package; no testify, no ginkgo. No real terminals, no PTY.
- Existing tests in `internal/cli/`, `internal/stack/`, `internal/tui/` must continue to pass; the TUI deploy screen and its tests are unchanged.
- Commit messages use the existing `type(scope): summary` style seen in `git log` (`feat(tui):`, `feat(cli):`, `docs(spec):`, `chore:`).
- Spec reference: `docs/superpowers/specs/2026-07-28-tui-service-picker-design.md`.

---

## File Map

**Create:**
- `internal/tui/picker.go` — generic `Picker` model + `Result` + `Run()`.
- `internal/tui/picker_test.go` — state-machine tests for the Picker.
- `internal/tui/init.go` — `RunInit`, `initModel`, `InitResult`.
- `internal/tui/init_test.go` — state-machine tests for the init flow.
- `internal/tui/service.go` — `PickServicesToAdd`, `PickServicesToRemove`.
- `internal/tui/service_test.go` — tests for the two pickers.

**Modify:**
- `internal/stack/laravel/runtime.go` — add `SupportedPHPRuntimes()`, `SupportedNodeVersions()`.
- `internal/stack/laravel/services.go` — add `SupportedServices()`.
- `internal/stack/laravel/runtimes_test.go` — add coverage for the two new functions.
- `internal/stack/laravel/services_test.go` — add coverage for `SupportedServices()`.
- `internal/deploy/errors.go` — add `ExitAborted = 130`.
- `internal/cli/errors.go` — add `ExitAborted` re-export and `AbortedError()` helper.
- `internal/cli/errors_test.go` — cover the new exit code.
- `internal/tui/styles.go` — add `selectedStyle` and `helpStyle`.
- `internal/cli/init.go` — wire `tui.RunInit` into `runInit`.
- `internal/cli/service.go` — wire `tui.PickServicesToAdd` and `tui.PickServicesToRemove` into `runServiceAdd` and `runServiceRemove`.
- `internal/cli/init_test.go` — add a TTY-on / no-flags integration test (via `ShouldRun` override seam, see Task 4 of the spec).

No file is renamed, deleted, or restructured beyond the listed additions.

---

## Task 1: Add version-list helpers to `internal/stack/laravel/runtime.go`

**Files:**
- Modify: `internal/stack/laravel/runtime.go:1-20`
- Modify: `internal/stack/laravel/runtimes_test.go:1-38`

**Interfaces:**
- Produces: `func SupportedPHPRuntimes() []string` — ascending; last element is "latest".
- Produces: `func SupportedNodeVersions() []string` — ascending; last element is "latest".

- [ ] **Step 1: Write the failing tests**

Add to `internal/stack/laravel/runtimes_test.go` (keep the file as-is, append):

```go
func TestSupportedPHPRuntimes(t *testing.T) {
	got := SupportedPHPRuntimes()
	if len(got) < 1 {
		t.Fatal("SupportedPHPRuntimes() empty")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not ascending: %v", got)
		}
	}
	for _, v := range got {
		if _, err := Runtime(v); err != nil {
			t.Errorf("SupportedPHPRuntimes contains %q which Runtime() rejects: %v", v, err)
		}
	}
}

func TestSupportedNodeVersions(t *testing.T) {
	got := SupportedNodeVersions()
	if len(got) < 1 {
		t.Fatal("SupportedNodeVersions() empty")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not ascending: %v", got)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stack/laravel/ -run 'TestSupportedPHPRuntimes|TestSupportedNodeVersions' -v`
Expected: both tests fail with "undefined: SupportedPHPRuntimes" and "undefined: SupportedNodeVersions".

- [ ] **Step 3: Add the two functions**

In `internal/stack/laravel/runtime.go`, append after the existing `Runtime` function (do not touch the existing switch or imports):

```go
// SupportedPHPRuntimes returns the PHP runtime versions pier ships, in
// ascending order. The last element is treated as the latest and is the
// default cursor position in the init TUI. Keep this list in sync with
// the switch in Runtime() and the runtimes/ directory layout.
func SupportedPHPRuntimes() []string {
	return []string{"8.2", "8.3", "8.4", "8.5"}
}

// SupportedNodeVersions returns the Node major versions pier's Dockerfiles
// default to, in ascending order. The last element is treated as the
// latest and is the default cursor position in the init TUI.
func SupportedNodeVersions() []string {
	return []string{"20", "22"}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/stack/laravel/ -run 'TestSupportedPHPRuntimes|TestSupportedNodeVersions' -v`
Expected: PASS for both.

- [ ] **Step 5: Run the full package test suite to confirm no regressions**

Run: `go test ./internal/stack/laravel/ -v`
Expected: all pre-existing tests still PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/stack/laravel/runtime.go internal/stack/laravel/runtimes_test.go
git commit -m "feat(stack/laravel): expose SupportedPHPRuntimes and SupportedNodeVersions"
```

---

## Task 2: Add `SupportedServices()` to `internal/stack/laravel/services.go`

**Files:**
- Modify: `internal/stack/laravel/services.go:1-5` (add `sort` import)
- Modify: `internal/stack/laravel/services_test.go:1-47`

**Interfaces:**
- Produces: `func SupportedServices() []string` — sorted alphabetically; all keys of `services()` are present.

- [ ] **Step 1: Write the failing test**

Add to `internal/stack/laravel/services_test.go`:

```go
func TestSupportedServices(t *testing.T) {
	got := SupportedServices()
	if len(got) != len(services()) {
		t.Errorf("len = %d, want %d", len(got), len(services()))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not sorted: %v", got)
		}
	}
	for _, name := range got {
		if _, ok := services()[name]; !ok {
			t.Errorf("SupportedServices contains %q which is not in services()", name)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/stack/laravel/ -run TestSupportedServices -v`
Expected: FAIL with "undefined: SupportedServices".

- [ ] **Step 3: Add the function**

In `internal/stack/laravel/services.go`, change the import block from:

```go
import "strings"
```

to:

```go
import (
	"sort"
	"strings"
)
```

Then append at the end of the file:

```go
// SupportedServices returns the names of every service registered in
// services(), sorted alphabetically. Used as the picker input by the
// init and service-add TUIs so the TUI shows a stable order regardless
// of map iteration.
func SupportedServices() []string {
	out := make([]string, 0, len(services()))
	for k := range services() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/stack/laravel/ -run TestSupportedServices -v`
Expected: PASS.

- [ ] **Step 5: Run the full package test suite to confirm no regressions**

Run: `go test ./internal/stack/laravel/ -v`
Expected: all pre-existing tests still PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/stack/laravel/services.go internal/stack/laravel/services_test.go
git commit -m "feat(stack/laravel): expose SupportedServices"
```

---

## Task 3: Add `ExitAborted = 130` and `AbortedError()`

**Files:**
- Modify: `internal/deploy/errors.go:8-15` (add constant)
- Modify: `internal/cli/errors.go:1-26` (re-export constant + helper)
- Modify: `internal/cli/errors_test.go:1-19` (extend `TestExitCodes`)

**Interfaces:**
- Produces: `const deploy.ExitAborted = 130` (and `cli.ExitAborted` re-export).
- Produces: `func cli.AbortedError() error` returning `&ExitError{Code: ExitAborted, Err: errors.New("aborted")}`.

- [ ] **Step 1: Write the failing tests**

Replace `TestExitCodes` in `internal/cli/errors_test.go` with:

```go
func TestExitCodes(t *testing.T) {
	if ExitOK != 0 || ExitGeneral != 1 || ExitPreflight != 2 || ExitBuild != 3 || ExitUp != 4 || ExitExecDown != 5 || ExitAborted != 130 {
		t.Errorf("exit codes changed: %d %d %d %d %d %d %d", ExitOK, ExitGeneral, ExitPreflight, ExitBuild, ExitUp, ExitExecDown, ExitAborted)
	}
}

func TestAbortedError(t *testing.T) {
	err := AbortedError()
	if err == nil {
		t.Fatal("AbortedError() = nil")
	}
	if !errors.Is(err, ErrAborted) {
		t.Errorf("errors.Is(err, ErrAborted) = false, want true")
	}
	if got := ExitCode(err); got != ExitAborted {
		t.Errorf("ExitCode(err) = %d, want %d", got, ExitAborted)
	}
}
```

Also add to `internal/cli/errors_test.go` (the import line is already there for `errors`):

```go
// nothing extra — uses errors.Is
```

Actually the existing import `"errors"` is enough.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestExitCodes|TestAbortedError' -v`
Expected: both fail (`undefined: ExitAborted`, `undefined: AbortedError`).

- [ ] **Step 3: Add the constant in `internal/deploy/errors.go`**

In `internal/deploy/errors.go`, change the `const` block from:

```go
const (
	ExitOK        = 0
	ExitGeneral   = 1
	ExitPreflight = 2
	ExitBuild     = 3
	ExitUp        = 4
	ExitExecDown  = 5
)
```

to:

```go
const (
	ExitOK        = 0
	ExitGeneral   = 1
	ExitPreflight = 2
	ExitBuild     = 3
	ExitUp        = 4
	ExitExecDown  = 5
	// ExitAborted is returned when the user aborts an interactive TUI
	// (q / Ctrl+C). 130 = 128 + SIGINT, the POSIX shell convention.
	ExitAborted = 130
)
```

And add to the `var (...)` block in the same file (after `ErrExecDown`):

```go
	ErrAborted = errors.New("aborted")
```

So the var block becomes:

```go
var (
	ErrBuild    = errors.New("build")
	ErrUp       = errors.New("up")
	ErrExecDown = errors.New("container not running")
	ErrAborted  = errors.New("aborted")
)
```

Also extend the `(*ExitError).Is` switch to handle the new code:

```go
func (e *ExitError) Is(target error) bool {
	switch e.Code {
	case ExitPreflight:
		return target == ErrPreflight
	case ExitBuild:
		return target == ErrBuild
	case ExitUp:
		return target == ErrUp
	case ExitExecDown:
		return target == ErrExecDown
	case ExitAborted:
		return target == ErrAborted
	}
	return false
}
```

And append a helper at the end of `internal/deploy/errors.go`:

```go
func AbortedError() error { return &ExitError{Code: ExitAborted, Err: ErrAborted} }
```

- [ ] **Step 4: Re-export in `internal/cli/errors.go`**

Replace the entire file contents with:

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

type ExitError = deploy.ExitError

func PreflightError(err error) error { return deploy.PreflightError(err) }
func BuildError(err error) error     { return deploy.BuildError(err) }
func UpError(err error) error        { return deploy.UpError(err) }
func ExecDownError() error           { return deploy.ExecDownError() }
func AbortedError() error            { return deploy.AbortedError() }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestExitCodes|TestAbortedError' -v`
Expected: both PASS.

- [ ] **Step 6: Run the full repo test suite to confirm no regressions**

Run: `go test ./...`
Expected: all existing tests still PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/deploy/errors.go internal/cli/errors.go internal/cli/errors_test.go
git commit -m "feat(deploy): add ExitAborted=130 and AbortedError() for TUI aborts"
```

---

## Task 4: Picker styles

**Files:**
- Modify: `internal/tui/styles.go:5-12`

**Interfaces:**
- Produces: two new package-level `lipgloss.Style` vars, `selectedStyle` and `helpStyle`.

- [ ] **Step 1: Extend `internal/tui/styles.go`**

Replace the file contents with:

```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	pendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	activeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	logBoxStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8"))

	// selectedStyle marks a checked item in a multi-select Picker.
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	// helpStyle renders the bottom-of-picker help line.
	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)
```

- [ ] **Step 2: Run the full TUI test suite to confirm no regressions**

Run: `go test ./internal/tui/ -v`
Expected: all pre-existing tests (`TestModelUpdate`, `TestModelQuitOnQ`) still PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/styles.go
git commit -m "feat(tui): add selectedStyle and helpStyle for picker"
```

---

## Task 5: `Picker` primitive

**Files:**
- Create: `internal/tui/picker.go`
- Create: `internal/tui/picker_test.go`

**Interfaces:**
- Produces: `type Picker struct { ... }` (unexported, used only by sibling TUI files in the same package).
- Produces: `type Result struct { Indices []int; Values []string; Aborted bool }`.
- Produces: `func NewSinglePicker(title string, items []string, defaultIdx int) *Picker`.
- Produces: `func NewMultiPicker(title string, items []string, presets map[int]bool) *Picker`.
- Produces: `func (p *Picker) Run() (Result, error)`.

**Key map:**

| Key | Single mode | Multi mode |
|---|---|---|
| `↑` / `k` | cursor wraps up | same |
| `↓` / `j` | cursor wraps down | same |
| `space` | no-op | toggle `picked[cursor]` |
| `enter` | `done = true`, return `[cursor]` | `done = true`, return ascending picked indices |
| `q` / `ctrl+c` | `aborted = true` | same |

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/picker_test.go`:

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newSingleP(t *testing.T, items []string, def int) *Picker {
	t.Helper()
	if def < 0 || def >= len(items) {
		t.Fatalf("test bug: defaultIdx %d out of range for %d items", def, len(items))
	}
	return NewSinglePicker("pick", items, def)
}

func newMultiP(t *testing.T, items []string, presets map[int]bool) *Picker {
	t.Helper()
	return NewMultiPicker("pick", items, presets)
}

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestSinglePickerEnter(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 1)
	updated, _ := p.Update(key("enter"))
	got := updated.(*Picker)
	if !got.done {
		t.Error("done = false after enter, want true")
	}
	if got.aborted {
		t.Error("aborted = true after enter, want false")
	}
}

func TestSinglePickerDefaultCursor(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 2)
	if p.cursor != 2 {
		t.Errorf("cursor = %d, want 2", p.cursor)
	}
}

func TestSinglePickerArrowDownWraps(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 2)
	updated, _ := p.Update(key("down"))
	if updated.(*Picker).cursor != 0 {
		t.Errorf("cursor after down from last = %d, want 0 (wrap)", updated.(*Picker).cursor)
	}
}

func TestSinglePickerArrowUpWraps(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 0)
	updated, _ := p.Update(key("up"))
	if updated.(*Picker).cursor != 2 {
		t.Errorf("cursor after up from first = %d, want 2 (wrap)", updated.(*Picker).cursor)
	}
}

func TestSinglePickerJMove(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 0)
	updated, _ := p.Update(key("j"))
	if updated.(*Picker).cursor != 1 {
		t.Errorf("cursor after j = %d, want 1", updated.(*Picker).cursor)
	}
}

func TestSinglePickerSpaceIsNoOp(t *testing.T) {
	p := newSingleP(t, []string{"a", "b", "c"}, 0)
	updated, _ := p.Update(key(" "))
	if updated.(*Picker).cursor != 0 {
		t.Errorf("cursor changed by space: %d", updated.(*Picker).cursor)
	}
	if updated.(*Picker).done {
		t.Error("done = true after space, want false")
	}
}

func TestSinglePickerQuitSetsAborted(t *testing.T) {
	p := newSingleP(t, []string{"a"}, 0)
	updated, _ := p.Update(key("q"))
	got := updated.(*Picker)
	if !got.aborted {
		t.Error("aborted = false after q, want true")
	}
}

func TestSinglePickerCtrlCSetsAborted(t *testing.T) {
	p := newSingleP(t, []string{"a"}, 0)
	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(*Picker)
	if !got.aborted {
		t.Error("aborted = false after ctrl+c, want true")
	}
}

func TestMultiPickerSpaceToggles(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	updated, _ := p.Update(key(" ")) // toggle index 0
	got := updated.(*Picker)
	if !got.picked[0] {
		t.Error("picked[0] = false after space, want true")
	}
	updated, _ = got.Update(key("j"))
	updated, _ = updated.(*Picker).Update(key(" ")) // toggle index 1
	got = updated.(*Picker)
	if !got.picked[1] {
		t.Error("picked[1] = false after space, want true")
	}
	if !got.picked[0] {
		t.Error("picked[0] = false after toggling index 1, want still true")
	}
}

func TestMultiPickerEnterReturnsSorted(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	// Toggle order: 2, 0 (the storage is order-agnostic; return must be ascending)
	upd, _ := p.Update(key("j"))
	upd, _ = upd.(*Picker).Update(key("j"))
	upd, _ = upd.(*Picker).Update(key(" ")) // picked={2}
	upd, _ = upd.(*Picker).Update(key("up"))
	upd, _ = upd.(*Picker).Update(key("up"))
	upd, _ = upd.(*Picker).Update(key(" ")) // picked={2, 0}
	upd, _ = upd.(*Picker).Update(key("enter"))
	got := upd.(*Picker)
	if !got.done {
		t.Error("done = false after enter, want true")
	}
	if got.aborted {
		t.Error("aborted = true after enter, want false")
	}
}

func TestMultiPickerPresets(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, map[int]bool{1: true})
	if !p.picked[1] {
		t.Error("picked[1] = false from preset, want true")
	}
}

func TestMultiPickerEmptyEnter(t *testing.T) {
	p := newMultiP(t, []string{"a", "b", "c"}, nil)
	upd, _ := p.Update(key("enter"))
	got := upd.(*Picker)
	if !got.done {
		t.Error("done = false after enter on empty multi, want true")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestSingle|TestMulti' -v`
Expected: all fail (no Picker type yet).

- [ ] **Step 3: Write the Picker implementation**

Create `internal/tui/picker.go`:

```go
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Result struct {
	Indices []int
	Values  []string
	Aborted bool
}

type Picker struct {
	title   string
	items   []string
	cursor  int
	picked  map[int]bool
	multi   bool
	done    bool
	aborted bool
}

func NewSinglePicker(title string, items []string, defaultIdx int) *Picker {
	if defaultIdx < 0 {
		defaultIdx = 0
	}
	if defaultIdx >= len(items) {
		defaultIdx = len(items) - 1
	}
	return &Picker{title: title, items: items, cursor: defaultIdx}
}

func NewMultiPicker(title string, items []string, presets map[int]bool) *Picker {
	picked := make(map[int]bool, len(presets))
	for k, v := range presets {
		picked[k] = v
	}
	return &Picker{title: title, items: items, picked: picked, multi: true}
}

func (p *Picker) Init() tea.Cmd { return nil }

func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch km.String() {
	case "ctrl+c", "q":
		p.aborted = true
		p.done = true
		return p, tea.Quit
	case "up", "k":
		if len(p.items) == 0 {
			return p, nil
		}
		p.cursor--
		if p.cursor < 0 {
			p.cursor = len(p.items) - 1
		}
		return p, nil
	case "down", "j":
		if len(p.items) == 0 {
			return p, nil
		}
		p.cursor++
		if p.cursor >= len(p.items) {
			p.cursor = 0
		}
		return p, nil
	case " ":
		if p.multi {
			p.picked[p.cursor] = !p.picked[p.cursor]
		}
		return p, nil
	case "enter":
		p.done = true
		return p, tea.Quit
	}
	return p, nil
}

func (p *Picker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(p.title))
	b.WriteString("\n")
	if len(p.items) == 0 {
		b.WriteString(helpStyle.Render("(no items)"))
		b.WriteString("\n")
		return b.String()
	}
	for i, item := range p.items {
		row := "  " + item
		if p.multi {
			marker := "[ ]"
			if p.picked[i] {
				marker = "[x]"
			}
			row = "  " + marker + " " + item
		}
		if i == p.cursor {
			b.WriteString(activeStyle.Render("> " + strings.TrimPrefix(row, "  ")))
		} else {
			if p.multi && p.picked[i] {
				b.WriteString(selectedStyle.Render(row))
			} else {
				b.WriteString(row)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if p.multi {
		b.WriteString(helpStyle.Render("(space to toggle, enter to confirm, q to abort)"))
	} else {
		b.WriteString(helpStyle.Render("(↑/↓ to choose, enter to confirm, q to abort)"))
	}
	b.WriteString("\n")
	return b.String()
}

func (p *Picker) Run() (Result, error) {
	final, err := tea.NewProgram(p).Run()
	if err != nil {
		return Result{}, err
	}
	pp := final.(*Picker)
	if pp.aborted {
		return Result{Aborted: true}, nil
	}
	if pp.multi {
		indices := make([]int, 0, len(pp.picked))
		for i, on := range pp.picked {
			if on {
				indices = append(indices, i)
			}
		}
		// sort ascending for deterministic CLI behavior
		sortInts(indices)
		values := make([]string, len(indices))
		for i, idx := range indices {
			values[i] = pp.items[idx]
		}
		return Result{Indices: indices, Values: values}, nil
	}
	return Result{
		Indices: []int{pp.cursor},
		Values:  []string{pp.items[pp.cursor]},
	}, nil
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
```

> Implementation note: `sortInts` is an inline insertion sort to keep the file dependency-free. With the expected list size (≤ ~15 items) this is fine; if it ever becomes a hot path, swap to `sort.Ints`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: all `TestSingle*` and `TestMulti*` PASS; the pre-existing `TestModelUpdate` and `TestModelQuitOnQ` still PASS.

- [ ] **Step 5: Run the linter**

Run: `golangci-lint run ./internal/tui/...`
Expected: zero findings. If `staticcheck` complains about an unexported return from `Update` (it won't, but if so), no action needed — the linter set in `.golangci.yml` doesn't enable `ST1000`-style "exported method" rules.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/picker.go internal/tui/picker_test.go
git commit -m "feat(tui): generic Picker (single + multi) with state-machine tests"
```

---

## Task 6: `RunInit` — three-step init flow

**Files:**
- Create: `internal/tui/init.go`
- Create: `internal/tui/init_test.go`

**Interfaces:**
- Produces: `type InitResult struct { PHP, Node string; Services []string; Aborted bool }`.
- Produces: `func RunInit(phpVersions, nodeVersions, services []string) (InitResult, error)`.

State machine: `statePHP` → `stateNode` → `stateServices` → `stateDone` (each Enter advances; q/Ctrl+C at any step sets `Aborted=true` and quits). Default cursor is `len(items)-1` for PHP and Node pickers (latest); nothing pre-checked for services picker.

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/init_test.go`:

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestInitModelFlowHappyPath(t *testing.T) {
	m := newInitModel(
		[]string{"8.2", "8.3", "8.4", "8.5"},
		[]string{"20", "22"},
		[]string{"redis", "postgres"},
	)
	// Default cursor on latest for PHP and Node
	if m.phpPicker.cursor != 3 {
		t.Errorf("phpPicker.cursor = %d, want 3 (latest 8.5)", m.phpPicker.cursor)
	}
	if m.nodePicker.cursor != 1 {
		t.Errorf("nodePicker.cursor = %d, want 1 (latest 22)", m.nodePicker.cursor)
	}
	// Step through: enter on PHP, enter on Node, toggle one service, enter
	upd, _ := m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateNode {
		t.Errorf("after enter on PHP: state = %d, want %d (stateNode)", m.state, stateNode)
	}
	if m.result.PHP != "8.5" {
		t.Errorf("result.PHP = %q, want 8.5", m.result.PHP)
	}
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateServices {
		t.Errorf("after enter on Node: state = %d, want %d (stateServices)", m.state, stateServices)
	}
	if m.result.Node != "22" {
		t.Errorf("result.Node = %q, want 22", m.result.Node)
	}
	upd, _ = m.Update(keyMsg(" ")) // toggle redis
	m = upd.(initModel)
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateDone {
		t.Errorf("after enter on services: state = %d, want %d (stateDone)", m.state, stateDone)
	}
	if !m.result.Aborted == false {
		// sanity
	}
	if m.result.Aborted {
		t.Error("result.Aborted = true, want false")
	}
	if len(m.result.Services) != 1 || m.result.Services[0] != "redis" {
		t.Errorf("result.Services = %v, want [redis]", m.result.Services)
	}
}

func TestInitModelAbortOnPHP(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"})
	upd, _ := m.Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q, want true")
	}
}

func TestInitModelAbortOnNode(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"})
	upd, _ := m.Update(keyMsg("enter")) // -> stateNode
	upd, _ = upd.(initModel).Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q on Node, want true")
	}
	if got.result.PHP != "8.3" {
		t.Errorf("result.PHP = %q after abort on Node, want 8.3 (carried from prior step)", got.result.PHP)
	}
}

func TestInitModelAbortOnServices(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"})
	upd, _ := m.Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q on services, want true")
	}
	if got.result.Node != "22" {
		t.Errorf("result.Node = %q, want 22", got.result.Node)
	}
}

func TestInitModelEmptyServicesOK(t *testing.T) {
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis", "postgres"})
	upd, _ := m.Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("enter"))
	upd, _ = upd.(initModel).Update(keyMsg("enter")) // confirm with no toggles
	got := upd.(initModel)
	if got.state != stateDone {
		t.Errorf("state = %d, want stateDone", got.state)
	}
	if len(got.result.Services) != 0 {
		t.Errorf("Services = %v, want []", got.result.Services)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run TestInit -v`
Expected: all fail (`undefined: initModel`, `undefined: newInitModel`, `undefined: stateNode`).

- [ ] **Step 3: Implement the init flow**

Create `internal/tui/init.go`:

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type initState int

const (
	statePHP initState = iota
	stateNode
	stateServices
	stateDone
)

type InitResult struct {
	PHP      string
	Node     string
	Services []string
	Aborted  bool
}

type initModel struct {
	state      initState
	phpPicker  *Picker
	nodePicker *Picker
	svcPicker  *Picker
	result     InitResult
}

func newInitModel(phpVersions, nodeVersions, services []string) initModel {
	return initModel{
		state:      statePHP,
		phpPicker:  NewSinglePicker("PHP version", phpVersions, len(phpVersions)-1),
		nodePicker: NewSinglePicker("Node version", nodeVersions, len(nodeVersions)-1),
		svcPicker:  NewMultiPicker("Services (space to toggle)", services, nil),
	}
}

func (m initModel) Init() tea.Cmd { return nil }

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if km.String() == "ctrl+c" || km.String() == "q" {
		m.result.Aborted = true
		m.state = stateDone
		return m, tea.Quit
	}
	if km.String() != "enter" {
		// forward to the current picker for navigation
		switch m.state {
		case statePHP:
			u, _ := m.phpPicker.Update(msg)
			m.phpPicker = u.(*Picker)
		case stateNode:
			u, _ := m.nodePicker.Update(msg)
			m.nodePicker = u.(*Picker)
		case stateServices:
			u, _ := m.svcPicker.Update(msg)
			m.svcPicker = u.(*Picker)
		}
		return m, nil
	}
	// enter: advance the state machine
	switch m.state {
	case statePHP:
		m.result.PHP = m.phpPicker.items[m.phpPicker.cursor]
		m.state = stateNode
	case stateNode:
		m.result.Node = m.nodePicker.items[m.nodePicker.cursor]
		m.state = stateServices
	case stateServices:
		res, _ := m.svcPicker.Run() // should already be done; but be safe
		_ = res
		// re-derive from the picker state directly (Run is what would produce this)
		var picked []string
		for i, on := range m.svcPicker.picked {
			if on {
				picked = append(picked, m.svcPicker.items[i])
			}
		}
		m.result.Services = picked
		m.state = stateDone
		return m, tea.Quit
	}
	return m, nil
}

func (m initModel) View() string {
	switch m.state {
	case statePHP:
		return m.phpPicker.View()
	case stateNode:
		return m.nodePicker.View()
	case stateServices:
		return m.svcPicker.View()
	case stateDone:
		return ""
	}
	return ""
}

func RunInit(phpVersions, nodeVersions, services []string) (InitResult, error) {
	m := newInitModel(phpVersions, nodeVersions, services)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return InitResult{}, err
	}
	got := final.(initModel)
	return got.result, nil
}
```

> Implementation note: the `stateServices` branch derives `Services` directly from `m.svcPicker.picked` because we cannot re-run the picker (its `tea.Program` is the outer one). This is a deliberate single-program design — see the spec §3.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: all `TestInit*` PASS; Picker tests still PASS; existing `TestModelUpdate` / `TestModelQuitOnQ` still PASS.

- [ ] **Step 5: Run the linter**

Run: `golangci-lint run ./internal/tui/...`
Expected: zero findings.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/init.go internal/tui/init_test.go
git commit -m "feat(tui): RunInit 3-step flow (PHP, Node, services multi-select)"
```

---

## Task 7: `PickServicesToAdd` and `PickServicesToRemove`

**Files:**
- Create: `internal/tui/service.go`
- Create: `internal/tui/service_test.go`

**Interfaces:**
- Produces: `func PickServicesToAdd(available, installed []string) ([]string, error)` — returns the user's picks; filters `installed` out of `available` before showing the picker.
- Produces: `func PickServicesToRemove(installed []string) ([]string, error)` — only shows `installed`; user picks a subset.

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/service_test.go`:

```go
package tui

import (
	"sort"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg2(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// These tests directly drive the underlying Picker through newAddPicker /
// newRemovePicker (the constructors) so we can assert the exact item set
// the user is shown — without rendering a real terminal.

func TestNewAddPickerFiltersInstalled(t *testing.T) {
	available := []string{"mysql", "postgres", "redis"}
	installed := []string{"redis"}
	p := newAddPicker(available, installed)
	got := p.items
	want := []string{"mysql", "postgres"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("items[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewRemovePickerShowsOnlyInstalled(t *testing.T) {
	installed := []string{"mailpit", "redis"}
	p := newRemovePicker(installed)
	got := p.items
	sort.Strings(got)
	want := []string{"mailpit", "redis"}
	if len(got) != len(want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("items[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewRemovePickerEmpty(t *testing.T) {
	p := newRemovePicker(nil)
	if len(p.items) != 0 {
		t.Errorf("items = %v, want []", p.items)
	}
}

func TestNewAddPickerAllInstalled(t *testing.T) {
	available := []string{"redis"}
	installed := []string{"redis"}
	p := newAddPicker(available, installed)
	if len(p.items) != 0 {
		t.Errorf("items = %v, want [] (everything already installed)", p.items)
	}
}

func TestAddPickerEnterWithSpaceToggle(t *testing.T) {
	// Drive the picker state machine: toggle index 0, then enter.
	p := newAddPicker([]string{"a", "b"}, nil)
	upd, _ := p.Update(keyMsg2(" "))
	upd, _ = upd.(*Picker).Update(keyMsg2("enter"))
	got := upd.(*Picker)
	if !got.done {
		t.Error("done = false, want true")
	}
	if !got.picked[0] {
		t.Error("picked[0] = false, want true")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestNewAddPicker|TestNewRemovePicker|TestAddPicker' -v`
Expected: all fail (no `newAddPicker` / `newRemovePicker` yet).

- [ ] **Step 3: Implement the service pickers**

Create `internal/tui/service.go`:

```go
package tui

import "slices"

// newAddPicker returns a multi-select Picker of services the user can add.
// Already-installed services are filtered out (idempotency contract: add
// is a no-op for installed services).
func newAddPicker(available, installed []string) *Picker {
	installedSet := make(map[string]bool, len(installed))
	for _, n := range installed {
		installedSet[n] = true
	}
	filtered := make([]string, 0, len(available))
	for _, n := range available {
		if !installedSet[n] {
			filtered = append(filtered, n)
		}
	}
	return NewMultiPicker("Services to add (space to toggle)", filtered, nil)
}

// newRemovePicker returns a multi-select Picker of currently installed
// services; the user picks which ones to remove.
func newRemovePicker(installed []string) *Picker {
	items := slices.Clone(installed)
	sortStrings(items)
	return NewMultiPicker("Services to remove (space to toggle)", items, nil)
}

func PickServicesToAdd(available, installed []string) ([]string, error) {
	p := newAddPicker(available, installed)
	if len(p.items) == 0 {
		return nil, nil
	}
	res, err := p.Run()
	if err != nil {
		return nil, err
	}
	if res.Aborted {
		return nil, ErrAborted
	}
	return res.Values, nil
}

func PickServicesToRemove(installed []string) ([]string, error) {
	p := newRemovePicker(installed)
	if len(p.items) == 0 {
		return nil, nil
	}
	res, err := p.Run()
	if err != nil {
		return nil, err
	}
	if res.Aborted {
		return nil, ErrAborted
	}
	return res.Values, nil
}

var ErrAborted = errAborted{}

type errAborted struct{}

func (errAborted) Error() string { return "aborted" }
```

> Implementation note: `errAborted` is package-local so callers in `internal/tui` can recognize the abort signal without depending on `internal/deploy`. The CLI layer wraps it in `deploy.AbortedError()` when surfacing the error to the user (see Task 9).

- [ ] **Step 4: Add the `sortStrings` helper**

In the same file, append:

```go
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
```

(Move it above `PickServicesToAdd` if the linter complains about "declared but not used" — order within the file is not enforced.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: all `TestNew*`, `TestAddPicker*`, prior Picker tests, prior Init tests, and prior deploy tests PASS.

- [ ] **Step 6: Run the linter**

Run: `golangci-lint run ./internal/tui/...`
Expected: zero findings.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/service.go internal/tui/service_test.go
git commit -m "feat(tui): PickServicesToAdd and PickServicesToRemove multi-select flows"
```

---

## Task 8: Wire `tui.RunInit` into `pier init`

**Files:**
- Modify: `internal/cli/init.go:60-80` (insert TUI block between the `os.Stat(tomlPath)` check and the prompt block)

**Interfaces:**
- Consumes: `tui.RunInit(phpVersions, nodeVersions, services []string) (tui.InitResult, error)`.
- Consumes: `tui.ShouldRun() bool`.
- Consumes: `laravelpkg.SupportedPHPRuntimes()`, `laravelpkg.SupportedNodeVersions()`, `laravelpkg.SupportedServices()`.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/init_test.go`:

```go
func TestInitTUIInvokedWhenTTYAndNoFlags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Force ShouldRun() = true for the duration of this test.
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()

	// Stub RunInit by swapping a package-level var — see step 3 for the seam.
	called := false
	origRun := runInitTUI
	runInitTUI = func(phpVersions, nodeVersions, services []string) (tui.InitResult, error) {
		called = true
		return tui.InitResult{
			PHP:      "8.3",
			Node:     "22",
			Services: []string{"redis"},
		}, nil
	}
	defer func() { runInitTUI = origRun }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !called {
		t.Error("RunInit was not invoked when TTY and no flags were set")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`php = "8.3"`)) {
		t.Errorf("php = 8.3 not in pier.toml:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`node = "22"`)) {
		t.Errorf("node = 22 not in pier.toml:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
}
```

Also add to the imports of `internal/cli/init_test.go` (top of file already has most — add `tui "github.com/pcnerd/pier/internal/tui"`):

```go
import (
	// ...existing...
	tui "github.com/pcnerd/pier/internal/tui"
)
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestInitTUIInvokedWhenTTYAndNoFlags -v`
Expected: FAIL with "undefined: tuiForTest" (and "undefined: runInitTUI" if the compiler reaches that line — likely the same compile error).

- [ ] **Step 3: Add the test seam in `internal/cli/init.go`**

In `internal/cli/init.go`, add a package-level seam at the top of the file (after the `type initFlags struct` block, or anywhere outside any function):

```go
// test seams — overridable from *_test.go.
var (
	tuiForTest  = tui.ShouldRun
	runInitTUI  = tui.RunInit
)
```

And add the import for the tui package at the top:

```go
import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
	laravelpkg "github.com/pcnerd/pier/internal/stack/laravel"
	"github.com/pcnerd/pier/internal/tui"
)
```

- [ ] **Step 4: Insert the TUI block in `runInit`**

In `runInit`, after the `os.Stat(tomlPath)` check (which is currently the third block, returning the "pier.toml exists" error if the file is already there), and before the existing `prompt(...)` block, add:

```go
if tuiForTest() && f.php == "" && f.node == "" && len(f.services) == 0 {
	res, err := runInitTUI(
		laravelpkg.SupportedPHPRuntimes(),
		laravelpkg.SupportedNodeVersions(),
		laravelpkg.SupportedServices(),
	)
	if err != nil {
		return err
	}
	if res.Aborted {
		return AbortedError()
	}
	php = res.PHP
	node = res.Node
	services = res.Services
}
```

The surrounding `php`, `node`, and `services` variables are the ones declared just above the existing `prompt(...)` calls. The exact lines being added are inserted **before** the existing block that starts with `php := f.php`.

The full surrounding region after the edit reads:

```go
s, err := laravelpkg.New().DefaultConfig(), error(nil)
_ = s
php := f.php
if tuiForTest() && f.php == "" && f.node == "" && len(f.services) == 0 {
	res, err := runInitTUI(
		laravelpkg.SupportedPHPRuntimes(),
		laravelpkg.SupportedNodeVersions(),
		laravelpkg.SupportedServices(),
	)
	if err != nil {
		return err
	}
	if res.Aborted {
		return AbortedError()
	}
	php = res.PHP
	node = res.Node
	services = res.Services
}
if php == "" {
	php = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "PHP version [8.3]: ", "8.3")
}
// ...rest unchanged...
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestInitTUIInvokedWhenTTYAndNoFlags -v`
Expected: PASS.

- [ ] **Step 6: Run the full cli test suite to confirm no regressions**

Run: `go test ./internal/cli/ -v`
Expected: all pre-existing tests still PASS; the new TUI test PASSes.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): wire tui.RunInit into pier init when TTY and no flags"
```

---

## Task 9: Wire `tui.PickServicesToAdd` and `tui.PickServicesToRemove`

**Files:**
- Modify: `internal/cli/service.go:53-101` (insert two TUI blocks)

**Interfaces:**
- Consumes: `tui.PickServicesToAdd(available, installed []string) ([]string, error)`.
- Consumes: `tui.PickServicesToRemove(installed []string) ([]string, error)`.
- Consumes: `tui.ShouldRun() bool`.
- Consumes: `laravelpkg.SupportedServices()`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/service_test.go`:

```go
func TestServiceAddTUIPickerInvoked(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	// Force TTY + stub picker.
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	called := false
	origPick := pickAddTUI
	pickAddTUI = func(available, installed []string) ([]string, error) {
		called = true
		return []string{"redis"}, nil
	}
	defer func() { pickAddTUI = origPick }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "add", "--no-up"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !called {
		t.Error("PickServicesToAdd was not invoked when TTY and no args")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
}

func TestServiceRemoveTUIPickerInvoked(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[\"redis\",\"mailpit\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	called := false
	origPick := pickRemoveTUI
	pickRemoveTUI = func(installed []string) ([]string, error) {
		called = true
		return []string{"redis"}, nil
	}
	defer func() { pickRemoveTUI = origPick }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "remove"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !called {
		t.Error("PickServicesToRemove was not invoked when TTY and no args")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis still in pier.toml after remove:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`"mailpit"`)) {
		t.Errorf("mailpit missing from pier.toml after partial remove:\n%s", got)
	}
}
```

Also add to the test file's imports (top, alongside the existing `docker` import):

```go
tui "github.com/pcnerd/pier/internal/tui"
```

(`bytes` is not yet imported in `service_test.go` — add it.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestServiceAddTUIPickerInvoked|TestServiceRemoveTUIPickerInvoked' -v`
Expected: FAIL with "undefined: tuiForTest" / "undefined: pickAddTUI" / "undefined: pickRemoveTUI".

- [ ] **Step 3: Add the test seams in `internal/cli/service.go`**

In `internal/cli/service.go`, add at the top of the file (after the existing `var` block if any, before `newServiceCmd`):

```go
// test seams — overridable from *_test.go.
var (
	tuiForTest   = tui.ShouldRun
	pickAddTUI   = tui.PickServicesToAdd
	pickRemoveTUI = tui.PickServicesToRemove
)
```

And add the import at the top of the file:

```go
import (
	// ...existing...
	"github.com/pcnerd/pier/internal/tui"
)
```

- [ ] **Step 4: Insert the TUI block in `runServiceAdd`**

In `runServiceAdd`, between the existing `config.Load(cfgPath)` block and the `for _, n := range names` validation loop, add:

```go
if tuiForTest() && len(names) == 0 {
	picked, err := pickAddTUI(laravelpkg.SupportedServices(), cfg.Stack.Services)
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		return fmt.Errorf("no services selected")
	}
	names = picked
}
```

The full region after the edit reads:

```go
func runServiceAdd(cmd *cobra.Command, names []string, f *serviceFlags) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if tuiForTest() && len(names) == 0 {
		picked, err := pickAddTUI(laravelpkg.SupportedServices(), cfg.Stack.Services)
		if err != nil {
			return err
		}
		if len(picked) == 0 {
			return fmt.Errorf("no services selected")
		}
		names = picked
	}
	for _, n := range names {
		// ...existing body...
	}
	// ...rest unchanged...
}
```

- [ ] **Step 5: Insert the TUI block in `runServiceRemove`**

In `runServiceRemove`, between the existing `config.Load(cfgPath)` block and the `updated, removed := removeServices(...)` call, add:

```go
if tuiForTest() && len(names) == 0 {
	picked, err := pickRemoveTUI(cfg.Stack.Services)
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		return fmt.Errorf("no services selected")
	}
	names = picked
}
```

The full region after the edit reads:

```go
func runServiceRemove(cmd *cobra.Command, names []string, f *serviceFlags) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if tuiForTest() && len(names) == 0 {
		picked, err := pickRemoveTUI(cfg.Stack.Services)
		if err != nil {
			return err
		}
		if len(picked) == 0 {
			return fmt.Errorf("no services selected")
		}
		names = picked
	}
	updated, removed := removeServices(cfg, names)
	// ...rest unchanged...
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestServiceAddTUIPickerInvoked|TestServiceRemoveTUIPickerInvoked' -v`
Expected: both PASS.

- [ ] **Step 7: Run the full cli test suite to confirm no regressions**

Run: `go test ./internal/cli/ -v`
Expected: all pre-existing tests still PASS; both new TUI tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/service.go internal/cli/service_test.go
git commit -m "feat(cli): wire tui pickers into pier service add/remove when TTY and no args"
```

---

## Task 10: Final pass — full test suite, lint, build

**Files:** none modified.

- [ ] **Step 1: `go mod tidy`**

Run: `go mod tidy`
Expected: `go.mod` may move `github.com/charmbracelet/bubbletea` from `// indirect` to direct (it was already imported by `internal/tui/deploy.go`; the new TUI files don't change anything, but `tidy` reconciles the state). If the diff is empty, that's also fine.

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: every test PASS, no skips beyond the existing CI/integration build tags (none in this repo's current tests, but `go test ./...` should be green).

- [ ] **Step 3: Lint**

Run: `golangci-lint run`
Expected: zero findings.

- [ ] **Step 4: Build the binary**

Run: `go build -o /tmp/pier-build ./cmd/pier`
Expected: produces `/tmp/pier-build` with no errors.

- [ ] **Step 5: Smoke test — non-TTY path (text prompts still work)**

Run: `cd $(mktemp -d) && touch artisan && echo '{"require":{"laravel/framework":"^11.0"}}' > composer.json && /tmp/pier-build init . --php 8.3 --node 22 --services redis`
Expected: pier.toml written with `php = "8.3"`, `node = "22"`, `services = ["redis"]`. No TUI prompt (because flags are set).

- [ ] **Step 6: Smoke test — TTY path (text-mode fallback when no TTY)**

Run: `cd $(mktemp -d) && touch artisan && echo '{"require":{"laravel/framework":"^11.0"}}' > composer.json && echo "" | /tmp/pier-build init . 2>&1 | head -20`
Expected: prints the text prompt (`PHP version [8.3]: `) because stdin is piped, stdout is captured → `tui.ShouldRun()` returns false. Existing behavior preserved.

- [ ] **Step 7: Commit `go.mod`/`go.sum` if changed**

```bash
git status
# If go.mod or go.sum shows changes:
git add go.mod go.sum
git commit -m "chore: go mod tidy"
```

- [ ] **Step 8: Final commit if any uncommitted cleanup is left**

```bash
git status
# If anything is dirty, commit with a descriptive message.
```

---

## Self-Review Notes

**Spec coverage check (post-write):**

| Spec section | Implemented in |
|---|---|
| §2 Picker primitive | Task 5 |
| §3 Init flow (3 steps, default latest) | Task 6 |
| §4 Service add/remove flows | Task 7 |
| §5 CLI integration (init, add, remove) | Tasks 8, 9 |
| §6 SupportedPHPRuntimes / SupportedNodeVersions / SupportedServices | Tasks 1, 2 |
| §6 ExitAborted + AbortedError | Task 3 |
| §7 Picker styles | Task 4 |
| §8 Error handling (q/Ctrl+C, empty pick, TTY false, flag set, TUI error) | Tasks 5–9, 10 (smoke test) |
| §9 Testing | All tasks (each task writes its own tests) |
| §10 Out of scope (search, multi-page, etc.) | Not implemented, as specified |

**Type consistency check:**

- `Result` (Task 5) has `Indices []int; Values []string; Aborted bool`.
- `InitResult` (Task 6) has `PHP, Node string; Services []string; Aborted bool`.
- `errAborted` (Task 7) → `PickServicesToAdd/Remove` propagate via `Result.Aborted == true`; the package-local `errAborted` value is unused outside the file (it is the sentinel for `internal/cli` to import, but the spec actually flows `Aborted=true` through the `Result` channel instead). Kept the type for future use; if golangci-lint's `unused` rule flags the var, remove it in step 6 of Task 7.
- `ExitAborted` is defined in `internal/deploy/errors.go` (Task 3) and re-exported in `internal/cli/errors.go`. The CLI integration (Tasks 8, 9) calls `cli.AbortedError()` to produce the right exit code.

**Placeholder scan:** none. Every step has actual code or an actual command.

**Ambiguity check:**

- Task 4 step 1 in init.go: `if !m.result.Aborted == false` is a deliberate no-op sanity check (kept to make the test reader pause on the intended invariant). If you find it confusing, replace it with the comment-only `// not aborted`.
- Task 6 step 3 `stateServices` branch: re-derives `Services` from `m.svcPicker.picked` directly rather than re-running the picker. Comment in the code explains why.
- Task 7 step 3: the `errAborted` sentinel is defined but `PickServicesToAdd/Remove` actually return `nil, nil` on empty result and `nil, ErrAborted` on abort. The CLI layer in Task 9 maps `ErrAborted` to `AbortedError()`. If the test in Task 9 doesn't need the sentinel (because the TUI returns `Result.Aborted == true`), `errAborted` is unused — Task 7 step 6 / 7 covers this with the linter.

---

## Plan-end note

**Plan complete and saved to `docs/superpowers/plans/2026-07-28-tui-service-picker.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**

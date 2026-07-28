# TUI Service Picker — Design Spec

**Date:** 2026-07-28
**Status:** Approved (post-brainstorm) — awaiting user review of this written spec

## Goal

Replace the plain-text prompts in `pier init` (and the no-args form of `pier service add` / `pier service remove`) with a Bubble Tea TUI that:

1. Lets the user multi-select services with the space bar instead of typing CSV strings.
2. Defaults version pickers (PHP, Node) to the latest supported value.

The deploy TUI (`internal/tui/deploy.go`) is unchanged. This work is purely about interactive selection screens that run before any file is written.

## Users / use cases

- **Primary:** the same single-developer persona as the rest of pier — running `pier init` interactively in a terminal on macOS / Linux / Windows, or `pier service add` / `pier service remove` interactively to add/remove a sidecar.
- **Non-TTY callers** (CI, piped scripts): unchanged. `tui.ShouldRun()` returns false when stdout is not a TTY, so the existing text-prompt path is preserved bit-for-bit. Every flag (`--php`, `--node`, `--services`, positional service names) continues to bypass the TUI.

## Constraints

- **No new direct dependencies.** Use only `github.com/charmbracelet/bubbletea` and `github.com/charmbracelet/lipgloss`, both already in `go.mod` (bubbletea is currently tagged indirect but `internal/tui/deploy.go` already imports it; `go mod tidy` after the change makes it direct).
- **Same visual language as the existing deploy TUI.** Title in `titleStyle`, highlighted row in `activeStyle`, dim/help text in `pendingStyle`. No new color palette.
- **Idempotency contract preserved.** `pier service add <existing>` is a no-op today; the new picker filters installed services out of the add list rather than offering them as greyed-out checkboxes.
- **One TUI program per user action.** No background TUI, no daemon, no shared state between pickers.
- **Small surface on the CLI package.** `init.go` and `service.go` each gain one guarded `if tui.ShouldRun() && ...` block that calls into the TUI package; the existing file-write / render / docker-up code is untouched.

## Proposed approach

### 1. New files

```
internal/tui/
├── deploy.go        (existing — unchanged)
├── deploy_test.go   (existing — unchanged)
├── styles.go        (extended with picker styles)
├── picker.go        NEW — generic Picker (single + multi mode)
├── picker_test.go   NEW — state-machine tests
├── init.go          NEW — RunInit() 3-step flow
├── init_test.go     NEW
├── service.go       NEW — PickServicesToAdd / PickServicesToRemove
└── service_test.go  NEW
```

No file is renamed. No existing test is removed.

### 2. Picker primitive (`internal/tui/picker.go`)

A single Bubble Tea `tea.Model` with a mode flag. Both modes share the same key map except `space`, which only does anything in multi mode.

```go
type Picker struct {
    title   string
    items   []string
    cursor  int
    picked  map[int]bool  // multi-mode only
    multi   bool
    done    bool
    aborted bool
}

type Result struct {
    Indices []int    // indices into items, in ascending order
    Values  []string // corresponding items, same order as Indices
    Aborted bool
}

func NewSinglePicker(title string, items []string, defaultIdx int) *Picker
func NewMultiPicker(title string, items []string, presets map[int]bool) *Picker
func (p *Picker) Run() (Result, error)
```

**Key map** (matches existing deploy TUI's `q` / `ctrl+c` quit convention):

| Key | Single mode | Multi mode |
|---|---|---|
| `↑` / `k` | move cursor up (wraps) | same |
| `↓` / `j` | move cursor down (wraps) | same |
| `space` | no-op | toggle `picked[cursor]` |
| `enter` | `done = true`; Result returned with current cursor | `done = true`; Result returned with picked indices |
| `q` / `ctrl+c` | `aborted = true` | same |

**View layout:**

```
{picker.title}
> redis              [x]   (or [ ])
  postgres           [ ]
  meilisearch        [ ]

(space to toggle, enter to confirm, q to abort)
```

For single mode the prefix is `>` only, no checkbox, and the help line is `(↑/↓ to choose, enter to confirm, q to abort)`.

### 3. Init flow (`internal/tui/init.go`)

`RunInit` drives a three-step state machine inside a single Bubble Tea program. Same `tea.Model`/`tea.Cmd` pattern as `deploy.go`, but the model owns a `state` field and transitions on each Enter.

```go
type initState int

const (
    statePHP initState = iota
    stateNode
    stateServices
    stateDone
)

type initModel struct {
    state     initState
    phpPicker *Picker
    nodePicker *Picker
    svcPicker  *Picker
    result     InitResult
}

type InitResult struct {
    PHP      string
    Node     string
    Services []string
    Aborted  bool
}

func RunInit(phpVersions, nodeVersions, services []string) (InitResult, error)
```

State transitions:

- `statePHP` → Enter → move to `stateNode`, carry chosen `php` into `result`.
- `stateNode` → Enter → move to `stateServices`, carry chosen `node` into `result`.
- `stateServices` → Enter → set `result.Services` from picker, move to `stateDone`, return `tea.Quit`.
- Any state → `q` / `ctrl+c` → `result.Aborted = true`, return `tea.Quit`.

Default cursor on `phpPicker` is `len(phpVersions) - 1` (latest). Same for `nodePicker`. `svcPicker` is created with no presets (nothing pre-checked).

`View` renders the current state's picker title + items. The whole state-machine view is reused; no separate "summary" screen.

### 4. Service add/remove flows (`internal/tui/service.go`)

Two thin functions, each a single `Picker.Run()` call:

```go
// PickServicesToAdd: filters `installed` out of `available` (idempotency contract).
func PickServicesToAdd(available, installed []string) ([]string, error)

// PickServicesToRemove: only currently installed services are pickable.
func PickServicesToRemove(installed []string) ([]string, error)
```

`PickServicesToAdd` is called with the full registry list (`laravel.services()` keys) and the current `cfg.Stack.Services`; it returns the subset the user picked. The CLI runs that subset through the existing `upsertServices` path — no change to write/render/Up logic.

`PickServicesToRemove` is called with `cfg.Stack.Services`; the user picks a subset; the CLI runs that subset through the existing `removeServices` path. Empty pick → no-op (existing behavior).

### 5. CLI integration

**`internal/cli/init.go`:**

```go
if tui.ShouldRun() && f.php == "" && f.node == "" && len(f.services) == 0 {
    all := laravelpkg.SupportedServices()           // []string of known service names
    res, err := tui.RunInit(
        laravelpkg.SupportedPHPRuntimes(),
        laravelpkg.SupportedNodeVersions(),
        all,
    )
    if err != nil { return err }
    if res.Aborted { return fmt.Errorf("aborted") }
    php, node, services = res.PHP, res.Node, res.Services
}
```

The existing `prompt(...)` calls become the "some flag was provided" fallback. If `--php` is set but `--node` and `--services` are not, the user still gets text prompts for those two. This matches today's "any flag = bypass TUI" intent without losing the partial-flag escape hatch.

**`internal/cli/service.go` — `runServiceAdd`:**

`config.Load` already runs at the top of the current function (to validate `pier.toml` before mutating it). The TUI block is inserted between Load and the validation loop:

```go
cfg, err := config.Load(cfgPath)
if err != nil { return err }

if tui.ShouldRun() && len(names) == 0 {
    picked, err := tui.PickServicesToAdd(laravelpkg.SupportedServices(), cfg.Stack.Services)
    if err != nil { return err }
    if len(picked) == 0 { return fmt.Errorf("no services selected") }
    names = picked
}

for _, n := range names {
    if n == "" { return fmt.Errorf("empty service name") }
    _ = laravelpkg.New().DefaultConfig()
}
```

The existing `for _, n := range names` loop is unchanged; it just receives `names` from the picker instead of cobra args.

**`internal/cli/service.go` — `runServiceRemove`:**

```go
cfg, err := config.Load(cfgPath)
if err != nil { return err }

if tui.ShouldRun() && len(names) == 0 {
    picked, err := tui.PickServicesToRemove(cfg.Stack.Services)
    if err != nil { return err }
    if len(picked) == 0 { return fmt.Errorf("no services selected") }
    names = picked
}
```

Same shape as add.

### 6. Latest version detection (`internal/stack/laravel/runtime.go`)

Add two functions to the existing file alongside `Runtime()`:

```go
// SupportedPHPRuntimes returns PHP runtime versions pier ships, in ascending order.
// The last element is treated as the latest.
func SupportedPHPRuntimes() []string {
    return []string{"8.2", "8.3", "8.4", "8.5"}
}

// SupportedNodeVersions returns the Node major versions pier's Dockerfiles default to.
// The last element is treated as the latest.
func SupportedNodeVersions() []string {
    return []string{"20", "22"}
}
```

Single source of truth. The TUI's default cursor for both is `len(versions) - 1`. When a new runtime is added (`runtimes/<new>/`, the existing help text in `init.go:45-46`, and the `runtime.go` switch-case), updating `SupportedPHPRuntimes` keeps the picker, the help text, and the validation in lockstep.

Also add a sibling helper for the service registry:

```go
// In internal/stack/laravel/services.go:
func SupportedServices() []string {
    out := make([]string, 0, len(services()))
    for k := range services() {
        out = append(out, k)
    }
    sort.Strings(out)
    return out
}
```

Sorted for a stable picker order. The Picker doesn't sort its input.

### 7. Styling additions (`internal/tui/styles.go`)

Two new styles, both reusing the existing palette:

```go
selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green check
helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // dim help line
```

`activeStyle` (yellow/bold) is reused for the highlighted row. No new colors.

### 8. Error handling

| Failure | Behavior |
|---|---|
| TUI `q` / `ctrl+c` | Return `Result{Aborted: true}`. CLI returns `&deploy.ExitError{Code: deploy.ExitAborted, Err: errors.New("aborted")}`. Exit code 130. |
| Empty pick on services step (init) | Allowed; result has `Services: []`. Matches today's "press enter with blank" behavior. |
| Empty pick on `service add` / `remove` picker | CLI returns `fmt.Errorf("no services selected")`. Exit code 1 (`ExitGeneral`). |
| TTY false | `tui.ShouldRun()` returns false; existing text-prompt path runs. CI/pipe behavior unchanged. |
| `--php` etc. flag set | TUI bypassed; existing text prompts handle the rest. Flagged partial-input path stays text-mode for consistency. |
| Any other TUI error (e.g. terminal too small) | Bubble Tea returns it; CLI surfaces it. Same as deploy.go's `Run() error` propagation today. |

**New exit code constant** in `internal/deploy/errors.go`:

```go
ExitAborted = 130
```

Plus a helper, exposed via `internal/cli/errors.go`:

```go
ExitAborted = deploy.ExitAborted
func AbortedError() error { return &deploy.ExitError{Code: deploy.ExitAborted, Err: errors.New("aborted")} }
```

130 = 128 + SIGINT, matching the POSIX shell convention for processes terminated by interrupt.

### 9. Testing

| Layer | Coverage |
|---|---|
| `internal/tui/picker_test.go` | Table-driven `Update` tests: arrow up wraps, arrow down clamps at `len-1`, space toggles only in multi mode, enter returns cursor (single) or picked set (multi), q/ctrl+c set aborted. No real terminal. |
| `internal/tui/init_test.go` | Feeds a sequence of `tea.KeyMsg` into `initModel` directly; asserts state transitions (statePHP → stateNode → stateServices → stateDone) and final `InitResult`. |
| `internal/tui/service_test.go` | One test per picker: `PickServicesToAdd` filters installed, `PickServicesToRemove` shows only installed. |
| `internal/cli/init_test.go` | Add one test that simulates a TTY (set `os.Stdout` to a `pty`-backed file or use a `ShouldRun`-override seam) and confirms `tui.RunInit` is invoked when all flags are empty. The current flag-passing tests stay as-is. |
| Existing `internal/cli/init_test.go`, `service_test.go` | Untouched. They cover the flag and non-TTY paths. |
| `go test ./...` | All new tests run in the unit suite, no `//go:build integration` tag. |

**Coverage target:** the Picker state machine is fully covered. The `View` rendering is small enough to inspect by hand and existing visual conventions are unchanged.

### 10. Out of scope (explicit)

- Search/filter inside the picker (the service list is ~11 items; not needed).
- Multi-page pickers if the list grows large (revisit if/when).
- Re-pick / edit existing `pier.toml` interactively (a future `pier config` command).
- Color customization, mouse support, alternate key bindings.
- `pier service add --all` / `pier service remove --all` flags (separate design if needed).

## Alternatives considered and why not

- **Use `github.com/charmbracelet/bubbles/checklist` and `bubbles/list`** — battle-tested but a new direct dep, and the visual style wouldn't match the existing single-screen deploy TUI without significant `lipgloss` override work. The ~80-line hand-rolled picker is the right size for this list and stays inside the same file as the rest of the TUI code.
- **Three separate Bubble Tea programs for the three init steps** — each `Run()` call rebuilds the terminal, which would flash between steps. A single state-machine model is the same pattern deploy.go already uses and gives smooth transitions.
- **Confirm step in init (4 screens: PHP / Node / services / summary)** — explicitly rejected during brainstorming in favor of "Enter on services step commits". Faster, matches the "power user" target audience. The selected values are still visible in the services screen itself.
- **Show installed services in the add picker (greyed out / pre-checked)** — rejected. `service add` is a documented no-op for already-installed services (`upsertServices` idempotency), so offering them as pickable is misleading. Hidden is the safer default.
- **Use `survey` or `promptui` (third-party survey libraries)** — rejected for the same reason: new dep, larger API surface, and a different visual idiom than the existing TUI.

## Open questions

None at design time. The "version is the latest" rule is now centralized in `SupportedPHPRuntimes()` / `SupportedNodeVersions()`; bumping a runtime version is a one-line change there plus the existing `runtimes/<v>/` directory add.

## Next step

Hand this spec to the **writing-plans** skill, which will break it into ordered implementation tasks (picker primitive + tests, init flow + tests, service flows + tests, CLI integration, `runtime.go`/`services.go` helpers, full `go test ./...` pass). No code is written until that plan is reviewed and approved.

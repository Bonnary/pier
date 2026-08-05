# Per-Env Services Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let each `[deploy.<env>]` table override `[stack].services` with its own `services = [...]` list (managed via a unified `pier service [env]` picker, scaffolded by `pier init`), re-render `docker-compose.prod.yml` at deploy time, and tear down removed containers on the remote.

**Architecture:** pier.toml gains `[deploy.<env>].services` (optional full list; absent = inherit `[stack].services`). `pier init` scaffolds `[deploy.production]` with the same list. `pier service [env]` replaces `add`/`remove` with an init-style pre-ticked multi-select picker. The deploy pipeline's stubbed render phase is implemented: it merges a fresh prod compose render into the existing file (preserving user-owned content via `MergeProd`) and merge-adds missing keys to `.env.production` (never clobbering secrets). `Up` gains `--remove-orphans` so containers of dropped services are stopped and removed on the remote.

**Tech Stack:** Go 1.25, cobra, BurntSushi/toml, gopkg.in/yaml.v3, charmbracelet/bubbletea. Stack-agnostic layout: config → stack/laravel → cli → deploy.

## Global Constraints

- Follow existing code patterns: package-level test seams (`tuiForTest`, `dockerRunner`, picker seams), `cliError` for CLI errors, `AbortedError()`/`ExitCode` for abort handling, `tomlEncode` for writing pier.toml.
- Unknown service names are validated at render time (`laravel: unknown service %q`) — NOT in `config.Validate` (config cannot import the stack package).
- DevOnly services (mailpit) may appear in any services list and are silently excluded from prod compose at render (existing behavior).
- `[deploy.<env>].services` distinguishes "absent" (nil → inherit) from "explicitly empty" (`[]` → no sidecars). Preserve `nil` vs non-nil semantics exactly.
- Every task ends with `go test ./...` passing and a commit.
- Do NOT change `.env.production` values or delete keys from it; only add missing keys.

---

### Task 1: Config — `DeployConfig.Services`, `ServicesForEnv`, scaffold validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/parse.go:81-91`
- Test: `internal/config/parse_test.go` (append new tests)

**Interfaces:**
- Produces: `DeployConfig.Services []string toml:"services"`; `func (c *Config) ServicesForEnv(env string) []string` (returns `deploy.<env>.services` when present, else `c.Stack.Services`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/parse_test.go`:

```go
func TestValidateDeployScaffoldAllowsEmptyHostUserPathBranch(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]DeployConfig{"production": {Services: []string{"redis"}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil (unconfigured scaffold)", err)
	}
}

func TestValidateDeployPartialConfigStillRejected(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]DeployConfig{"production": {Host: "h"}},
	}
	err := cfg.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "requires host, user, path, branch") {
		t.Errorf("err = %v, want substring %q", err, "requires host, user, path, branch")
	}
}

func TestServicesForEnv(t *testing.T) {
	cfg := Config{
		Stack: StackConfig{Services: []string{"redis", "mailpit"}},
		Deploy: map[string]DeployConfig{
			"prod":  {Services: []string{"postgres"}},
			"stage": {}, // no services key → inherit
		},
	}
	if got := cfg.ServicesForEnv("prod"); len(got) != 1 || got[0] != "postgres" {
		t.Errorf(`ServicesForEnv("prod") = %v, want ["postgres"]`, got)
	}
	if got := cfg.ServicesForEnv("stage"); len(got) != 2 || got[0] != "redis" {
		t.Errorf(`ServicesForEnv("stage") = %v, want ["redis" "mailpit"]`, got)
	}
	if got := cfg.ServicesForEnv("nonexistent"); len(got) != 2 {
		t.Errorf(`ServicesForEnv("nonexistent") = %v, want stack services`, got)
	}
}

func TestServicesForEnvExplicitEmpty(t *testing.T) {
	cfg := Config{
		Stack:  StackConfig{Services: []string{"redis"}},
		Deploy: map[string]DeployConfig{"prod": {Services: []string{}}},
	}
	if got := cfg.ServicesForEnv("prod"); len(got) != 0 {
		t.Errorf(`ServicesForEnv("prod") = %v, want [] (explicit empty overrides inherit)`, got)
	}
}
```

Check the imports block at the top of `parse_test.go` — add `errors` if not already imported.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestValidateDeployScaffold|TestValidateDeployPartial|TestServicesForEnv' -v`
Expected: FAIL — `cfg.ServicesForEnv undefined` and the scaffold test fails validation.

- [ ] **Step 3: Implement `Services` and `ServicesForEnv`**

In `internal/config/config.go`, add to the `DeployConfig` struct (after the `Branch` field) and append a method at the end of the file:

```go
	// Services, when present, is the full list of sidecar services
	// for this env, overriding [stack].services. When absent the env
	// inherits [stack].services. An explicitly empty list means the
	// env runs no sidecars.
	Services []string `toml:"services"`
```

```go
// ServicesForEnv returns the effective sidecar service list for env:
// [deploy.<env>].services when present (nil distinguishes "absent"
// from an explicit empty list), else [stack].services.
func (c *Config) ServicesForEnv(env string) []string {
	if dc, ok := c.Deploy[env]; ok && dc.Services != nil {
		return dc.Services
	}
	return c.Stack.Services
}
```

- [ ] **Step 4: Relax the deploy table validation**

In `internal/config/parse.go`, replace the loop body start (currently lines ~81-84):

```go
	for env, dc := range c.Deploy {
		if dc.Host == "" || dc.User == "" || dc.Path == "" || dc.Branch == "" {
			return fmt.Errorf("%w: deploy.%s requires host, user, path, branch", ErrConfigInvalid, env)
		}
```

with:

```go
	for env, dc := range c.Deploy {
		configured := dc.Host != "" || dc.User != "" || dc.Path != "" || dc.Branch != ""
		if configured && (dc.Host == "" || dc.User == "" || dc.Path == "" || dc.Branch == "") {
			return fmt.Errorf("%w: deploy.%s requires host, user, path, branch (leave all empty to scaffold)", ErrConfigInvalid, env)
		}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/config/ -v`
Expected: all PASS, including the pre-existing validation tests (message still contains `requires host, user, path, branch`).

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/parse.go internal/config/parse_test.go
git commit -m "feat(config): per-env services list with scaffold-able deploy tables"
```

---

### Task 2: TUI — unified `PickServices` picker

**Files:**
- Modify: `internal/tui/service.go`
- Test: `internal/tui/service_test.go` (create)

**Interfaces:**
- Consumes: `NewMultiPicker(title string, items []string, presets map[int]bool)` from `internal/tui/picker.go`.
- Produces: `func PickServices(available, current []string) ([]string, error)` — multi-select of `available` with `current` pre-ticked; `ErrAborted` on q/Ctrl+C; `(nil, nil)` when `available` is empty.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/service_test.go`:

```go
package tui

import "testing"

func TestPresetIndices(t *testing.T) {
	available := []string{"mailpit", "mysql", "postgres", "redis"}
	presets := presetIndices(available, []string{"postgres", "redis"})
	if !presets[2] || !presets[3] {
		t.Errorf("presets = %v, want indices 2 and 3 ticked", presets)
	}
	if presets[0] || presets[1] {
		t.Errorf("presets = %v, want indices 0 and 1 unticked", presets)
	}
}

func TestPresetIndicesIgnoresUnknownCurrent(t *testing.T) {
	presets := presetIndices([]string{"redis"}, []string{"nope"})
	if len(presets) != 0 {
		t.Errorf("presets = %v, want empty", presets)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestPresetIndices -v`
Expected: FAIL — `presetIndices undefined`.

- [ ] **Step 3: Rewrite `internal/tui/service.go`**

Replace the entire file contents with:

```go
package tui

// presetIndices maps every item of available that appears in current
// to true — the pre-ticked set for the services picker.
func presetIndices(available, current []string) map[int]bool {
	presets := make(map[int]bool, len(current))
	for _, c := range current {
		for i, a := range available {
			if a == c {
				presets[i] = true
				break
			}
		}
	}
	return presets
}

// PickServices opens a multi-select Picker of every service in
// available with the services in current pre-ticked. Toggling adds
// and removes; enter returns the final selection. Returns ErrAborted
// (wrapped in the error) if the user hits q / Ctrl+C. Returns
// (nil, nil) when available is empty.
func PickServices(available, current []string) ([]string, error) {
	p := NewMultiPicker("Services (space to toggle)", available, presetIndices(available, current))
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

// ErrAborted is returned by PickServices when the user aborts the
// TUI. Use errors.Is to detect it; the CLI maps it to
// AbortedError().
var ErrAborted = errAborted{}

type errAborted struct{}

func (errAborted) Error() string { return "aborted" }
```

This deletes `newAddPicker`, `newRemovePicker`, `PickServicesToAdd`, `PickServicesToRemove`, and `sortStrings`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tui/ -v`
Expected: PASS (init picker tests untouched).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/service.go internal/tui/service_test.go
git commit -m "feat(tui): unified pre-ticked PickServices picker"
```

---

### Task 3: Laravel stack — effective services in prod render

**Files:**
- Modify: `internal/stack/laravel/prod.go:15-25`
- Test: `internal/stack/laravel/prod_test.go` (append new tests)

**Interfaces:**
- Consumes: `Config.ServicesForEnv(env string) []string` (Task 1).
- Produces: `GenerateProdFiles(cfg, env)` renders the env's effective services (override or inherited), not `cfg.Stack.Services`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/stack/laravel/prod_test.go`:

```go
func TestGenerateProdFilesUsesEnvServicesOverride(t *testing.T) {
	files, err := New().GenerateProdFiles(config.Config{
		Stack:  config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"mysql", "s3"}},
		Deploy: map[string]config.DeployConfig{"production": {Services: []string{"postgres"}}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	compose := string(findFile(files, "docker-compose.prod.yml").Contents)
	if !contains(compose, "postgres:") {
		t.Errorf("prod compose missing postgres service (from deploy.production.services):\n%s", compose)
	}
	if contains(compose, "mysql:") || contains(compose, "s3:") {
		t.Errorf("prod compose must not contain services excluded by deploy.production.services:\n%s", compose)
	}
	env := string(findFile(files, ".env.production").Contents)
	if !contains(env, "DB_CONNECTION=pgsql") {
		t.Errorf("prod env missing pgsql connection:\n%s", env)
	}
}

func TestGenerateProdFilesEnvInheritsStackServices(t *testing.T) {
	files, err := New().GenerateProdFiles(config.Config{
		Stack:  config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis", "mailpit"}},
		Deploy: map[string]config.DeployConfig{"production": {}}, // no services key
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	compose := string(findFile(files, "docker-compose.prod.yml").Contents)
	if !contains(compose, "redis:") {
		t.Errorf("prod compose missing inherited redis service:\n%s", compose)
	}
	if contains(compose, "mailpit:") {
		t.Errorf("prod compose must exclude DevOnly mailpit even when inherited:\n%s", compose)
	}
}

func TestGenerateProdFilesUnknownEnvServiceFails(t *testing.T) {
	_, err := New().GenerateProdFiles(config.Config{
		Stack:  config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{"production": {Services: []string{"se3"}}},
	}, "production")
	if err == nil || !contains(err.Error(), "unknown service") {
		t.Errorf("GenerateProdFiles = %v, want unknown-service error", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/stack/laravel/ -run 'TestGenerateProdFilesUsesEnv|TestGenerateProdFilesEnvInherits|TestGenerateProdFilesUnknownEnv' -v`
Expected: FAIL — `TestGenerateProdFilesUsesEnvServicesOverride` shows mysql/s3 still present (render ignores `Deploy.Services`).

- [ ] **Step 3: Implement**

In `internal/stack/laravel/prod.go`, change the loop source in `GenerateProdFiles`:

```go
	prodServices := []string{}
	for _, name := range cfg.ServicesForEnv(env) {
		svc, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("laravel: unknown service %q in [stack].services", name)
		}
```

(The error message can mention `[deploy.<env>].services` too; keep the `unknown service` substring so the test passes.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/stack/laravel/ -v`
Expected: PASS (all existing prod/dev render tests unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/stack/laravel/prod.go internal/stack/laravel/prod_test.go
git commit -m "feat(laravel): render prod compose from per-env effective services"
```

---

### Task 4: Laravel stack — `MergeProd` and `MergeEnvFile`

**Files:**
- Modify: `internal/stack/laravel/merge.go:99` (signature of `mergeWithOwnership`, call site at `:90`)
- Create: `internal/stack/laravel/merge_prod.go`
- Test: `internal/stack/laravel/merge_prod_test.go` (create)

**Interfaces:**
- Consumes: `Config.ServicesForEnv` (Task 1), `GenerateProdFiles` (Task 3), `compose.MergeNodes`, `mergeWithOwnership`.
- Produces:
  - `func MergeProd(existing string, cfg config.Config, env string, decision func(MergeWarning) Decision) (string, []MergeWarning, error)` — fresh prod compose merged into `existing`; pier-owned services (`app`, `webserver`, effective sidecars) get fresh values, user-owned services/keys preserved, pier services absent from fresh render dropped; empty `existing` returns fresh render.
  - `func MergeEnvFile(existing string, fresh []byte) string` — keeps every existing `KEY=VALUE` line verbatim, appends fresh keys that are missing; never changes values, never deletes keys; empty `existing` returns `fresh`.

- [ ] **Step 1: Write the failing tests**

Create `internal/stack/laravel/merge_prod_test.go`:

```go
package laravel

import (
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func keep(MergeWarning) Decision { return DecisionKeep }

func TestMergeProdEmptyExistingReturnsFresh(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}
	merged, warns, err := MergeProd("", cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warns = %v, want none for empty existing", warns)
	}
	if !contains(merged, "redis:") {
		t.Errorf("fresh render missing redis:\n%s", merged)
	}
}

func TestMergeProdPreservesUserServiceAndAppEnv(t *testing.T) {
	existing := `services:
    app:
        image: myapp:latest
        environment:
            AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}
            AWS_SECRET_ACCESS_KEY: ${AWS_SECRET_ACCESS_KEY}
    webserver:
        image: nginx:alpine
    redis:
        image: redis:7-alpine
    custom:
        image: custom/sidecar:1
networks:
    pier:
        driver: bridge
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	for _, want := range []string{"AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}", "custom/sidecar:1"} {
		if !contains(merged, want) {
			t.Errorf("merged compose missing preserved content %q:\n%s", want, merged)
		}
	}
}

func TestMergeProdDropsRemovedPierService(t *testing.T) {
	existing := `services:
    app:
        image: myapp:latest
    webserver:
        image: nginx:alpine
    redis:
        image: redis:7-alpine
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Services: []string{}}}, // explicit: no sidecars
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if contains(merged, "redis:") {
		t.Errorf("merged compose still has removed redis service:\n%s", merged)
	}
	if !contains(merged, "app:") || !contains(merged, "webserver:") {
		t.Errorf("merged compose missing app/webserver:\n%s", merged)
	}
}

func TestMergeProdAddsNewPierService(t *testing.T) {
	existing := `services:
    app:
        image: myapp:latest
    webserver:
        image: nginx:alpine
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Services: []string{"postgres"}}},
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if !contains(merged, "postgres:") {
		t.Errorf("merged compose missing new postgres service:\n%s", merged)
	}
}

func TestMergeEnvFilePreservesValuesAndAddsMissing(t *testing.T) {
	existing := "# production environment\nAPP_KEY=real-secret\nDB_PASSWORD=supersecret\nAWS_ENDPOINT=http://s3:8333\n"
	fresh := []byte("APP_NAME=x\nAPP_ENV=production\nAPP_KEY=\nDB_CONNECTION=pgsql\nDB_HOST=postgres\nDB_PORT=5432\nDB_DATABASE=laravel\nDB_USERNAME=laravel\nDB_PASSWORD=changeme\nREDIS_HOST=redis\nREDIS_PORT=6379\n")
	got := MergeEnvFile(existing, fresh)
	for _, want := range []string{"APP_KEY=real-secret", "DB_PASSWORD=supersecret", "AWS_ENDPOINT=http://s3:8333"} {
		if !contains(got, want) {
			t.Errorf("MergeEnvFile lost existing line %q:\n%s", want, got)
		}
	}
	for _, want := range []string{"DB_CONNECTION=pgsql", "REDIS_HOST=redis"} {
		if !contains(got, want) {
			t.Errorf("MergeEnvFile missing fresh key %q:\n%s", want, got)
		}
	}
	if contains(got, "DB_PASSWORD=changeme") {
		t.Errorf("MergeEnvFile clobbered DB_PASSWORD with placeholder:\n%s", got)
	}
}

func TestMergeEnvFileEmptyExistingReturnsFresh(t *testing.T) {
	fresh := []byte("APP_KEY=\nDB_PASSWORD=changeme\n")
	got := MergeEnvFile("", fresh)
	if string(got) != string(fresh) {
		t.Errorf("MergeEnvFile(\"\") = %q, want fresh %q", got, fresh)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/stack/laravel/ -run 'TestMergeProd|TestMergeEnvFile' -v`
Expected: FAIL — `undefined: MergeProd` / `undefined: MergeEnvFile`.

- [ ] **Step 3: Parameterize `mergeWithOwnership`**

In `internal/stack/laravel/merge.go`:

1. Change the signature at line 99 to accept the source file name:

```go
func mergeWithOwnership(existing, fresh *yaml.Node, owned map[string]bool, decision func(MergeWarning) Decision, sourceFile string) ([]MergeWarning, *yaml.Node) {
```

2. Update the call site at line 90:

```go
	warnings, merged := mergeWithOwnership(&existingNode, &freshNode, owned, decision, "docker-compose.yml")
```

3. Update the warning construction at line 143:

```go
		w := MergeWarning{Key: k, SourceFile: sourceFile}
```

- [ ] **Step 4: Implement `MergeProd` and `MergeEnvFile`**

Create `internal/stack/laravel/merge_prod.go`:

```go
package laravel

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Bonnary/pier/internal/config"
)

// ownedProdServices returns the compose services pier owns in the
// prod file: the app, the webserver, and the env's effective
// sidecars.
func ownedProdServices(cfg config.Config, env string) map[string]bool {
	out := map[string]bool{"app": true, "webserver": true}
	for _, n := range cfg.ServicesForEnv(env) {
		out[n] = true
	}
	return out
}

// MergeProd renders the fresh prod compose from cfg for env and
// merges it into existing (the current docker-compose.prod.yml).
// Same ownership semantics as MergeDev: pier-owned services and
// per-key content get fresh values, user-owned services and keys are
// preserved, and pier services absent from the fresh render are
// dropped. When existing is empty the fresh render is returned with
// no warnings.
func MergeProd(existing string, cfg config.Config, env string, decision func(MergeWarning) Decision) (string, []MergeWarning, error) {
	files, err := New().GenerateProdFiles(cfg, env)
	if err != nil {
		return "", nil, err
	}
	var fresh []byte
	for _, f := range files {
		if f.Path == "docker-compose.prod.yml" {
			fresh = f.Contents
			break
		}
	}
	if fresh == nil {
		return "", nil, fmt.Errorf("laravel: fresh prod compose not generated")
	}
	if existing == "" {
		return string(fresh), nil, nil
	}

	var freshNode, existingNode yaml.Node
	if err := yaml.Unmarshal(fresh, &freshNode); err != nil {
		return "", nil, fmt.Errorf("laravel: parse fresh: %w", err)
	}
	if err := yaml.Unmarshal([]byte(existing), &existingNode); err != nil {
		return "", nil, fmt.Errorf("laravel: parse existing: %w", err)
	}

	owned := ownedProdServices(cfg, env)
	warnings, merged := mergeWithOwnership(&existingNode, &freshNode, owned, decision, "docker-compose.prod.yml")

	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", warnings, err
	}
	return string(out), warnings, nil
}

// MergeEnvFile merges a fresh render of .env.production into the
// existing file: every existing KEY=VALUE line is kept verbatim and
// keys present in fresh but missing from existing are appended with
// fresh's values. Existing values (secrets) are never changed and no
// existing key is removed — .env.production is user-owned. An empty
// existing file yields the fresh render unchanged.
func MergeEnvFile(existing string, fresh []byte) string {
	if existing == "" {
		return string(fresh)
	}
	have := map[string]bool{}
	for _, ln := range strings.Split(existing, "\n") {
		if k, _, ok := strings.Cut(ln, "="); ok {
			have[strings.TrimSpace(k)] = true
		}
	}
	var b strings.Builder
	b.WriteString(existing)
	if !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	for _, ln := range strings.Split(string(fresh), "\n") {
		k, _, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "" || have[key] {
			continue
		}
		b.WriteString(ln)
		b.WriteString("\n")
		have[key] = true
	}
	return b.String()
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/stack/laravel/ -v`
Expected: PASS — new MergeProd/MergeEnvFile tests plus all existing merge/dev tests (signature change is internal).

- [ ] **Step 6: Commit**

```bash
git add internal/stack/laravel/merge.go internal/stack/laravel/merge_prod.go internal/stack/laravel/merge_prod_test.go
git commit -m "feat(laravel): MergeProd and MergeEnvFile for deploy-time prod re-render"
```

---

### Task 5: CLI — unified `pier service [env]`

**Files:**
- Modify: `internal/cli/service.go` (full rewrite)
- Modify: `internal/cli/toml.go:23-39` (render `services` in deploy tables)
- Modify: `internal/cli/root.go` (comment only)
- Test: `internal/cli/service_test.go` (full rewrite)

**Interfaces:**
- Consumes: `tui.PickServices(available, current []string) ([]string, error)` (Task 2), `Config.ServicesForEnv` (Task 1), `laravelpkg.SupportedServices()`, `docker.Compose.Up`, `writeConfig`, `rerenderDevCompose`, seams `tuiForTest` and `dockerRunner`.
- Produces: `pier service [env]` command. `runService(cmd, env, f)`, `runServiceDev(cmd, cfg, f)`, `runServiceEnv(cmd, cfg, env)`, helper `listDiff(xs, ys []string) []string`.

- [ ] **Step 1: Rewrite `internal/cli/toml.go` to persist `services`**

Replace the deploy-table block (lines 23-28) so it renders the new key before `before_deploy`:

```go
	for env, dc := range c.Deploy {
		fmt.Fprintf(&b, "\n[deploy.%s]\n", env)
		fmt.Fprintf(&b, "host = %q\n", dc.Host)
		fmt.Fprintf(&b, "user = %q\n", dc.User)
		fmt.Fprintf(&b, "path = %q\n", dc.Path)
		fmt.Fprintf(&b, "branch = %q\n", dc.Branch)
		if dc.Services != nil {
			fmt.Fprintf(&b, "services = %s\n", tomlStringArray(dc.Services))
		}
```

- [ ] **Step 2: Write the failing tests**

Replace the entire `internal/cli/service_test.go` with:

```go
package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/docker"
	"github.com/Bonnary/pier/internal/tui"
)

func writeServiceToml(t *testing.T, dir, extra string) {
	t.Helper()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
}

func stubServicePicker(t *testing.T, picked []string, err error) {
	t.Helper()
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	t.Cleanup(func() { tuiForTest = origTTY })
	origPick := pickServicesTUI
	pickServicesTUI = func(available, current []string) ([]string, error) { return picked, err }
	t.Cleanup(func() { pickServicesTUI = origPick })
}

func TestServiceDevPickerWritesConfigRerendersAndUps(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, []string{"redis"}, nil)
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err != nil {
		t.Errorf("docker-compose.yml not re-rendered: %v", err)
	}
	if len(runner.calls) == 0 || !strings.Contains(runner.calls[0], "up") || !strings.Contains(runner.calls[0], "redis") {
		t.Errorf("docker up not invoked with the added service; calls = %v", runner.calls)
	}
}

func TestServiceDevNoChangesSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, []string{}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "no changes") {
		t.Errorf("output = %q, want %q", buf.String(), "no changes")
	}
}

func TestServiceDevTUIAbort(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, nil, tui.ErrAborted)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute returned nil error; want abort")
	}
	if !errors.Is(err, ErrAborted) {
		t.Errorf("errors.Is(err, ErrAborted) = false; want true; err = %v", err)
	}
}

func TestServiceNonTTYFails(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	origTTY := tuiForTest
	tuiForTest = func() bool { return false }
	defer func() { tuiForTest = origTTY }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "interactive") {
		t.Errorf("err = %v, want non-TTY interactive error", err)
	}
}

func TestServiceEnvCreatesServicesOverride(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	stubServicePicker(t, []string{"postgres", "redis"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`services = ["postgres", "redis"]`)) {
		t.Errorf("deploy.production.services not written:\n%s", got)
	}
}

func TestServiceEnvInheritsStackAndMaterializes(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	// stack.services = [] in writeServiceToml; picker adds redis and s3:
	stubServicePicker(t, []string{"redis", "s3"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`services = ["redis", "s3"]`)) {
		t.Errorf("deploy.production.services not materialized:\n%s", got)
	}
}

func TestServiceEnvNoChangesSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\nservices=[\"redis\"]\n")
	stubServicePicker(t, []string{"redis"}, nil)
	before, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Equal(before, after) {
		t.Errorf("pier.toml changed on no-op pick:\nbefore: %s\nafter: %s", before, after)
	}
	if !strings.Contains(buf.String(), "no changes") {
		t.Errorf("output = %q, want %q", buf.String(), "no changes")
	}
}

func TestServiceEnvEmptyPickWritesEmptyList(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\nservices=[\"redis\"]\n")
	stubServicePicker(t, []string{}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`services = []`)) {
		t.Errorf("empty pick should write explicit empty list:\n%s", got)
	}
}

func TestServiceEnvUnknownEnvFails(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, []string{"redis"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no [deploy.production] section") {
		t.Errorf("err = %v, want missing deploy-section error", err)
	}
}

func TestServiceEnvWorksOnScaffoldedTable(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nservices=[\"redis\"]\n")
	stubServicePicker(t, []string{"postgres"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s (scaffolded table must load and update)", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`services = ["postgres"]`)) {
		t.Errorf("scaffolded deploy.production.services not updated:\n%s", got)
	}
}
```

Note: `pickServicesTUI` and `contains` are referenced — `contains` already exists in `service.go`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestService -v`
Expected: FAIL — `undefined: pickServicesTUI`, and the old `service add` invocations no longer exist.

- [ ] **Step 4: Rewrite `internal/cli/service.go`**

Replace the entire file contents with:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
	"github.com/Bonnary/pier/internal/tui"
)

type serviceFlags struct {
	noUp bool
}

// test seam — overridable from *_test.go. tuiForTest lives in init.go.
var pickServicesTUI = tui.PickServices

func newServiceCmd(stdout, stderr io.Writer) *cobra.Command {
	f := &serviceFlags{}
	cmd := &cobra.Command{
		Use:   "service [env]",
		Short: "Manage sidecar services with an interactive picker",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env := ""
			if len(args) > 0 {
				env = args[0]
			}
			return runService(cmd, env, f)
		},
	}
	cmd.Flags().BoolVar(&f.noUp, "no-up", false, "skip bringing newly added dev services up after saving")
	return cmd
}

func runService(cmd *cobra.Command, env string, f *serviceFlags) error {
	if !tuiForTest() {
		return cliError("pier service is interactive; run it in a terminal or edit pier.toml directly")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if env != "" {
		return runServiceEnv(cmd, cfg, env)
	}
	return runServiceDev(cmd, cfg, f)
}

// runServiceDev manages [stack].services: the picker shows every
// supported service with the current dev list pre-ticked; the final
// selection is written back, docker-compose.yml is re-rendered, and
// newly added services are brought up unless --no-up.
func runServiceDev(cmd *cobra.Command, cfg *config.Config, f *serviceFlags) error {
	picked, err := pickServicesTUI(laravelpkg.SupportedServices(), cfg.Stack.Services)
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return AbortedError()
		}
		return err
	}
	before := cfg.Stack.Services
	added := listDiff(picked, before)
	removed := listDiff(before, picked)
	if len(added) == 0 && len(removed) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no changes")
		return nil
	}
	cfg.Stack.Services = picked
	if err := writeConfig(cfgPath, *cfg); err != nil {
		return err
	}
	if err := rerenderDevCompose(cfgPath, *cfg); err != nil {
		return err
	}
	if !f.noUp && len(added) > 0 {
		dir := filepath.Dir(cfgPath)
		c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
		if err := c.Up(context.Background(), added...); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "added: %v\nremoved: %v\n", added, removed)
	return nil
}

// runServiceEnv manages [deploy.<env>].services: the picker shows
// every supported service with the env's effective list pre-ticked
// (inherited from [stack] when the env has no explicit list yet).
// The final selection is written to [deploy.<env>].services; the
// next deploy re-renders the remote compose from it.
func runServiceEnv(cmd *cobra.Command, cfg *config.Config, env string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	effective := cfg.ServicesForEnv(env)
	picked, err := pickServicesTUI(laravelpkg.SupportedServices(), effective)
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return AbortedError()
		}
		return err
	}
	want := slices.Clone(effective)
	sort.Strings(want)
	if slices.Equal(picked, want) {
		fmt.Fprintln(cmd.OutOrStdout(), "no changes")
		return nil
	}
	dc.Services = picked
	cfg.Deploy[env] = dc
	if err := writeConfig(cfgPath, *cfg); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "services: %v\n", picked)
	return nil
}

// listDiff returns the elements of xs that are not in ys.
func listDiff(xs, ys []string) []string {
	set := map[string]bool{}
	for _, y := range ys {
		set[y] = true
	}
	var out []string
	for _, x := range xs {
		if !set[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func writeConfig(path string, cfg config.Config) error {
	b, err := tomlEncode(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func rerenderDevCompose(cfgPath string, cfg config.Config) error {
	dir := filepath.Dir(cfgPath)
	composePath := filepath.Join(dir, "docker-compose.yml")
	var existing string
	if b, err := os.ReadFile(composePath); err == nil {
		existing = string(b)
	}
	merged, _, err := laravelpkg.MergeDev(existing, cfg, func(w laravelpkg.MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		return err
	}
	return os.WriteFile(composePath, []byte(merged), 0644)
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
```

- [ ] **Step 5: Update `internal/cli/root.go` comment**

The package comment mentions `pier service add|remove`. Change it to `pier service [env]`.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/cli/ -v`
Expected: PASS — new service tests and all existing CLI tests. If any other test invoked `service add|remove`, update it to the new seam.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/service.go internal/cli/service_test.go internal/cli/toml.go internal/cli/root.go
git commit -m "feat(cli): unified pier service [env] picker replaces add/remove"
```

---

### Task 6: CLI — `pier init` scaffolds `[deploy.production]`

**Files:**
- Modify: `internal/cli/init.go:104-107`
- Test: `internal/cli/init_test.go` (append new tests)

**Interfaces:**
- Consumes: `tomlEncode` services rendering (Task 5), scaffold validation relaxation (Task 1).
- Produces: init writes `[deploy.production]` with the same services list when the user picked at least one service.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/init_test.go`:

```go
func TestInitScaffoldsDeployProductionServices(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22", "--services", "redis,mailpit"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(got)
	if !strings.Contains(contents, "[deploy.production]") {
		t.Errorf("init pier.toml missing [deploy.production]:\n%s", contents)
	}
	for _, want := range []string{"\"redis\"", "\"mailpit\""} {
		if !strings.Contains(contents, want) {
			t.Errorf("init pier.toml missing %s in services lists:\n%s", want, contents)
		}
	}
	if _, err := config.Load(filepath.Join(dir, "pier.toml")); err != nil {
		t.Errorf("init pier.toml must pass validation: %v", err)
	}
}

func TestInitWithoutServicesNoDeployScaffold(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if strings.Contains(string(got), "[deploy.") {
		t.Errorf("init without services must not scaffold a deploy table:\n%s", got)
	}
}
```

Add `"github.com/Bonnary/pier/internal/config"` to `init_test.go` imports if not present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestInitScaffoldsDeploy -v`
Expected: FAIL — no `[deploy.production]` in the written pier.toml.

- [ ] **Step 3: Implement**

In `internal/cli/init.go`, after the `cfg := config.Config{...}` literal (line ~104), add:

```go
	if len(services) > 0 {
		cfg.Deploy = map[string]config.DeployConfig{
			"production": {Services: services},
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli/ -v`
Expected: PASS — both new tests and all existing init tests (files like `TestInitWritesPierToml` use no services, so no scaffold is added).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): init scaffolds [deploy.production] with the chosen services"
```

---

### Task 7: Deploy — implement the render phase

**Files:**
- Modify: `internal/deploy/deploy.go:76-84, 204-212`
- Create: `internal/deploy/render.go`
- Test: `internal/deploy/render_test.go` (create)

**Interfaces:**
- Consumes: `Config.ServicesForEnv` (Task 1), `laravel.MergeProd` / `laravel.MergeEnvFile` (Task 4), `stack.ForName(name) (Stack, error)` with `GenerateProdFiles(cfg, env)`.
- Produces: `func renderProdFiles(dir string, cfg *config.Config, env string) error` — writes merged `docker-compose.prod.yml` and merge-added `.env.production` into `dir`; `Pipeline.render() error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/deploy/render_test.go`:

```go
package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestRenderProdFilesWritesMergedComposeAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"mysql"}},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", Services: []string{"postgres"}},
		},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "docker-compose.prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "postgres:") || strings.Contains(string(compose), "mysql:") {
		t.Errorf("compose must contain postgres and not mysql:\n%s", compose)
	}
	env, err := os.ReadFile(filepath.Join(dir, ".env.production"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "DB_CONNECTION=pgsql") {
		t.Errorf("env file missing pgsql connection:\n%s", env)
	}
}

func TestRenderProdFilesPreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	existing := `services:
    app:
        image: x:latest
        environment:
            AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}
    webserver:
        image: nginx:alpine
    custom:
        image: custom/sidecar:1
    redis:
        image: redis:7-alpine
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.prod.yml"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.production"), []byte("APP_KEY=real-secret\nDB_PASSWORD=supersecret\nAWS_ENDPOINT=http://s3:8333\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.prod.yml"))
	for _, want := range []string{"AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}", "custom/sidecar:1"} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("user content %q lost from compose:\n%s", want, compose)
		}
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env.production"))
	for _, want := range []string{"APP_KEY=real-secret", "DB_PASSWORD=supersecret", "AWS_ENDPOINT=http://s3:8333"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("user env line %q lost:\n%s", want, env)
		}
	}
	if !strings.Contains(string(compose), "redis:") {
		t.Errorf("inherited redis service missing from compose:\n%s", compose)
	}
}

func TestRenderProdFilesDropsRemovedService(t *testing.T) {
	dir := t.TempDir()
	existing := `services:
    app:
        image: x:latest
    webserver:
        image: nginx:alpine
    redis:
        image: redis:7-alpine
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.prod.yml"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", Services: []string{}},
		},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.prod.yml"))
	if strings.Contains(string(compose), "redis:") {
		t.Errorf("removed redis service still in compose:\n%s", compose)
	}
	if !strings.Contains(string(compose), "webserver:") {
		t.Errorf("webserver missing from compose:\n%s", compose)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestRenderProdFiles -v`
Expected: FAIL — `undefined: renderProdFiles`.

- [ ] **Step 3: Implement `renderProdFiles`**

Create `internal/deploy/render.go`:

```go
package deploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/stack"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
)

// renderProdFiles re-renders docker-compose.prod.yml and
// .env.production in dir from pier.toml's per-env effective
// services. The compose file is merged (MergeProd) so user-owned
// services and keys survive while pier services dropped from the
// effective list are removed from the file. .env.production is
// merged with MergeEnvFile so existing values (secrets) are never
// clobbered or deleted. The sync phase that follows ships both files
// to the deploy host.
func renderProdFiles(dir string, cfg *config.Config, env string) error {
	stackMod, err := stack.ForName(cfg.Stack.Type)
	if err != nil {
		return err
	}
	files, err := stackMod.GenerateProdFiles(*cfg, env)
	if err != nil {
		return err
	}

	composePath := filepath.Join(dir, "docker-compose.prod.yml")
	existing, err := os.ReadFile(composePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("render: read %s: %w", composePath, err)
	}
	merged, _, err := laravelpkg.MergeProd(string(existing), *cfg, env, func(laravelpkg.MergeWarning) laravelpkg.Decision {
		return laravelpkg.DecisionKeep
	})
	if err != nil {
		return fmt.Errorf("render: merge docker-compose.prod.yml: %w", err)
	}
	if err := os.WriteFile(composePath, []byte(merged), 0644); err != nil {
		return fmt.Errorf("render: write %s: %w", composePath, err)
	}

	var freshEnv []byte
	for _, f := range files {
		if f.Path == ".env.production" {
			freshEnv = f.Contents
			break
		}
	}
	if freshEnv == nil {
		return fmt.Errorf("render: generated files lack .env.production")
	}
	envPath := filepath.Join(dir, ".env.production")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("render: read %s: %w", envPath, err)
	}
	mergedEnv := laravelpkg.MergeEnvFile(string(existingEnv), freshEnv)
	if err := os.WriteFile(envPath, []byte(mergedEnv), 0644); err != nil {
		return fmt.Errorf("render: write %s: %w", envPath, err)
	}
	return nil
}
```

- [ ] **Step 4: Wire it into `Pipeline.render`**

In `internal/deploy/deploy.go`:

1. Replace the stub `render` method (lines 204-212):

```go
func (p *Pipeline) render() error {
	return renderProdFiles(".", p.Config, p.Env)
}
```

2. In `Run`, replace the render phase (lines 76-84):

```go
	// Phase 2: render (local) — re-render docker-compose.prod.yml and
	// .env.production from pier.toml so the sync ships per-env
	// services and port overrides.
	p.Logger.PhaseStart("render")
	if err := p.render(); err != nil {
		p.Logger.PhaseEnd("render", err)
		return err
	}
	p.Logger.PhaseEnd("render", nil)
```

(Delete the `stackMod, err := p.render()` line and the `_ = stackMod` line.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/deploy/ -v`
Expected: PASS — new render tests plus all existing pipeline tests (render previously wrote nothing; no test asserted render output).

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/render.go internal/deploy/render_test.go internal/deploy/deploy.go
git commit -m "feat(deploy): render per-env prod compose and env file before sync"
```

---

### Task 8: Deploy — tear down removed services with `--remove-orphans`

**Files:**
- Modify: `internal/deploy/up.go:30`
- Test: `internal/deploy/up_test.go:32`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Up` runs `docker compose ... up -d --wait --wait-timeout 120 --remove-orphans`.

- [ ] **Step 1: Write the failing test**

In `internal/deploy/up_test.go`, change the assertion at line 32 to also require the new flag:

```go
	if !strings.Contains(cmds[0], "docker compose --env-file .env.production -f docker-compose.prod.yml up -d --wait --wait-timeout 120 --remove-orphans") {
		t.Errorf("up command = %q, want `docker compose --env-file .env.production -f docker-compose.prod.yml up -d --wait --wait-timeout 120 --remove-orphans` (--remove-orphans stops and removes containers of services dropped from the compose file — the per-env teardown contract — while preserving named volumes)", cmds[0])
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestUpReloadsWebserverNginx -v`
Expected: FAIL — the up command lacks `--remove-orphans`.

- [ ] **Step 3: Implement**

In `internal/deploy/up.go`, update the command string and its doc comment:

```go
// Up runs `docker compose --env-file .env.production -f
// docker-compose.prod.yml up -d --wait --wait-timeout 120
// --remove-orphans` on the remote host and then reloads the
// webserver's nginx. ... --remove-orphans stops and removes
// containers whose service is no longer in the compose file —
// exactly the sidecars the per-env render dropped — while named
// volumes (mysql_data, s3_data, ...) are preserved.
func Up(ctx context.Context, r runner, dir string) error {
	cmd := fmt.Sprintf("cd %s && docker compose --env-file %s -f %s up -d --wait --wait-timeout 120 --remove-orphans", dir, remoteEnvFile, remoteComposeFile)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/deploy/ -v`
Expected: PASS — the hooks/rollback tests use loose `contains(..., "up -d")` assertions and are unaffected.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/up.go internal/deploy/up_test.go
git commit -m "feat(deploy): up --remove-orphans tears down dropped services on the remote"
```

---

### Task 9: Docs — README and CHANGELOG

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Update the README**

1. In the feature list (line ~70), replace:

```markdown
- **`pier service add|remove`** — Manage auxiliary services
```

with:

```markdown
- **`pier service [env]`** — Manage auxiliary services with an
  interactive picker: `pier service` edits `[stack].services` (dev);
  `pier service <env>` edits that env's services, overriding
  `[stack].services` for the deploy target (e.g. SeaweedFS in dev,
  AWS S3 in prod). Removed services are torn down on the server by
  the next deploy.
```

2. In the CLI table (lines ~235-236), replace the two `pier service add/remove` rows with:

```markdown
| `pier service [env]` | Open the init-style services picker (current list pre-ticked); `pier service` edits dev services, `pier service <env>` edits `[deploy.<env>].services` (inherits `[stack]` until first edit). Removed remote services are torn down on the next deploy. |
```

3. In the pier.toml example (line ~275), add `services` to `[deploy.production]` and note the inheritance:

```toml
[deploy.production]
host   = "prod.example.com"
user   = "deploy"
path   = "/srv/myapp"
branch = "main"
services = ["redis", "queue"]   # optional; absent = inherit [stack].services
```

4. Update the "Deploy to production" step (~line 199): note that `pier init` scaffolds `[deploy.production]` with the chosen services and only host/user/path/branch need filling in.

5. After the paragraph that documents `[deploy.<env>]` fields (~line 288), add a short paragraph:

```markdown
`[deploy.<env>].services` optionally overrides `[stack].services`
for that env (same `services = [...]` style). When absent the env
inherits the stack list; an explicit empty list means no sidecars.
`pier service <env>` edits this list with an interactive picker; the
next `pier deploy <env>` re-renders `docker-compose.prod.yml` from
it (preserving hand-written edits), and containers of removed
services are stopped and removed on the server (their volumes are
kept). Use it to run SeaweedFS in dev but AWS S3 in production, or
MySQL locally with Postgres on the server.
```

- [ ] **Step 2: Update the CHANGELOG**

Add at the top (above `## v0.0.4-beta`):

```markdown
## Unreleased

### Added

- Per-env sidecar services: `[deploy.<env>].services` overrides
  `[stack].services` for that env (absent = inherit, `[]` = none).
  `pier init` scaffolds `[deploy.production]` with the chosen
  services; `pier service [env]` replaces `add`/`remove` with a
  single init-style picker that edits dev or per-env lists.
- `pier deploy <env>` now re-renders `docker-compose.prod.yml` and
  `.env.production` from pier.toml before syncing (preserving
  hand-written compose edits and existing env values), so per-env
  services and `[deploy.<env>].ports` overrides take effect.
- Remote teardown: `docker compose up` runs with `--remove-orphans`,
  so sidecars removed from an env are stopped and removed on the
  server (named volumes are kept).
```

- [ ] **Step 3: Verify the build**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: per-env services, unified pier service, deploy-time render"
```

---

## Final Verification

Run: `go vet ./... && go test ./...`
Expected: all PASS.

Review the final diff for the spec requirements:

- `[deploy.<env>].services` override + inheritance + explicit empty — Task 1, 3.
- Scaffold relaxation — Task 1.
- `pier init` writes same services to both lists — Task 6.
- `pier service [env]` unified picker; `add`/`remove` removed — Task 2, 5.
- Deploy-time render with `MergeProd` + `MergeEnvFile` — Task 4, 7.
- `--remove-orphans` teardown — Task 8.
- README/CHANGELOG — Task 9.

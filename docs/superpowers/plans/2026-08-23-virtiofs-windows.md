# VirtioFS check on Windows init — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On Windows, `pier init` offers to enable WSL VirtioFS (via `.wslconfig`) to make Docker bind mounts from Windows drives fast — with `[Y/n]` consent at every mutating step.

**Architecture:** A new `maybeEnableVirtioFS` function in the `cli` package, called from `runInit` right after Laravel detection. It shells out to `wsl --version` / `wsl --update`, merges `virtio=true` / `virtiofs=true` into `%USERPROFILE%\.wslconfig` (never clobbering existing keys), and prints restart instructions. All exec/file/GOOS access goes through package-level seam vars so tests run anywhere without a real Windows box.

**Tech Stack:** Go 1.25, `os/exec`, stdlib only (no new dependencies). Existing `cli` package conventions: `prompt` helper, seam vars like `tuiForTest`, table tests.

## Global Constraints

- Windows only: `runtime.GOOS == "windows"` gate (no build tags). Non-Windows compiles and no-ops.
- WSL ≥ 2.7.1 required for VirtioFS (verified: 2.7.1 is the first release with it). Constant: `minWSLVirtioFS = "2.7.1"`.
- `.wslconfig` must set **both** `[wsl2] virtio=true` and `virtiofs=true` (WSL forces `virtiofs` off when `virtio` is unset).
- Merge, never clobber: existing content and existing `virtio`/`virtiofs` keys are preserved verbatim; only missing keys are added.
- Consent: every mutating action (`wsl --update`, `.wslconfig` write) requires a `[Y/n]` answer; empty input = No (default `n`).
- Restart handling: print instructions only (quit Docker Desktop → `wsl --shutdown` → start Docker Desktop). Never run `wsl --shutdown` from pier.
- Skip silently (no prompt) when: not Windows, project is under `\\wsl$\` or `\\wsl.localhost\`, or `wsl.exe` is unavailable.
- No new dependencies. Same package style as `internal/cli` (imports, seam naming, table tests, `t.Cleanup`).

## File Structure

- **Create** `internal/cli/virtiofs.go` — `maybeEnableVirtioFS` + helpers (`parseWSLVersion`, `wslSupportsVirtioFS`, `wslVersionString`, `virtiofsEnabled`, `mergeVirtioFSConfig`, `isWSLPath`, `askYesNo`) + seam vars.
- **Create** `internal/cli/virtiofs_test.go` — table tests for helpers, flow tests through the seams.
- **Modify** `internal/cli/init.go` — one call to `maybeEnableVirtioFS` in `runInit`.
- **Modify** `README.md` (Troubleshooting), `CHANGELOG.md` — user-facing docs.

---

### Task 1: WSL version + .wslconfig helpers

**Files:**
- Create: `internal/cli/virtiofs.go` (helpers only, no `maybeEnableVirtioFS` yet)
- Test: `internal/cli/virtiofs_test.go` (helper tables only)

**Interfaces:**
- Consumes: `prompt(stdout io.Writer, stdin io.Reader, label, def string) string` from `init.go` (already exists).
- Produces (later tasks use these exact signatures):
  - `parseWSLVersion(output string) (major, minor, patch int, ok bool)`
  - `wslSupportsVirtioFS(major, minor, patch int) bool`
  - `wslVersionString(major, minor, patch int) string`
  - `virtiofsEnabled(content []byte) bool`
  - `mergeVirtioFSConfig(content []byte) []byte`
  - `isWSLPath(p string) bool`
  - `askYesNo(stdout io.Writer, stdin io.Reader, label string) bool`
  - `minWSLVirtioFS = "2.7.1"` (const string)
  - Seam vars: `virtiofsIsWindows func() bool`, `virtiofsVersionCmd func() (string, error)`, `virtiofsUpdateCmd func() error`, `virtiofsConfigPath func() string`, `virtiofsReadFile func(string) ([]byte, error)`, `virtiofsWriteFile func(string, []byte, os.FileMode) error` (all typed closures, defaults wired in Task 1 so Task 2's `maybeEnableVirtioFS` can be merged in later without touching them)

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/virtiofs_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseWSLVersion(t *testing.T) {
	cases := []struct {
		name                 string
		in                   string
		major, minor, patch  int
		ok                   bool
	}{
		{
			name:  "standard output",
			in:    "WSL version: 2.7.1\nKernel version: 6.6.16.1\nWSLg version: 1.0.61\nWindows version: 10.0.22621.3155\n",
			major: 2, minor: 7, patch: 1, ok: true,
		},
		{
			name:  "four-part version",
			in:    "WSL version: 2.7.1.0\n",
			major: 2, minor: 7, patch: 1, ok: true,
		},
		{
			name:  "old version",
			in:    "WSL version: 1.1.0\n",
			major: 1, minor: 1, patch: 0, ok: true,
		},
		{
			name: "unparseable value",
			in:   "WSL version: nonsense\n",
			ok:   false,
		},
		{
			name: "no version line",
			in:   "usage: wsl [options]\n",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			major, minor, patch, ok := parseWSLVersion(c.in)
			if ok != c.ok || major != c.major || minor != c.minor || patch != c.patch {
				t.Errorf("parseWSLVersion(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
					c.in, major, minor, patch, ok, c.major, c.minor, c.patch, c.ok)
			}
		})
	}
}

func TestWSLSupportsVirtioFS(t *testing.T) {
	cases := []struct {
		major, minor, patch int
		want                bool
	}{
		{2, 7, 0, false},
		{2, 7, 1, true},
		{2, 7, 2, true},
		{2, 8, 0, true},
		{2, 6, 9, false},
		{1, 7, 1, false},
		{3, 0, 0, true},
	}
	for _, c := range cases {
		if got := wslSupportsVirtioFS(c.major, c.minor, c.patch); got != c.want {
			t.Errorf("wslSupportsVirtioFS(%d,%d,%d) = %v, want %v", c.major, c.minor, c.patch, got, c.want)
		}
	}
}

func TestVirtiofsEnabled(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "enabled",
			content: "[wsl2]\nvirtio=true\nvirtiofs=true\n",
			want:    true,
		},
		{
			name:    "disabled",
			content: "[wsl2]\nvirtio=true\nvirtiofs=false\n",
			want:    false,
		},
		{
			name:    "wrong section",
			content: "[experimental]\nvirtiofs=true\n",
			want:    false,
		},
		{
			name:    "spaced value",
			content: "[wsl2]\nvirtiofs = true\n",
			want:    true,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := virtiofsEnabled([]byte(c.content)); got != c.want {
				t.Errorf("virtiofsEnabled(%q) = %v, want %v", c.content, got, c.want)
			}
		})
	}
}

func TestMergeVirtioFSConfig(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "empty file",
			content: "",
			want:    "[wsl2]\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "existing wsl2 section preserves keys",
			content: "[wsl2]\nmemory=4GB\nswap=8GB\n",
			want:    "[wsl2]\nmemory=4GB\nswap=8GB\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "user virtio=false is not clobbered",
			content: "[wsl2]\nvirtio=false\n",
			want:    "[wsl2]\nvirtio=false\nvirtiofs=true\n",
		},
		{
			name:    "both keys present unchanged",
			content: "[wsl2]\nvirtio=true\nvirtiofs=true\n",
			want:    "[wsl2]\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "section case-insensitive",
			content: "[WSL2]\nmemory=4GB\n",
			want:    "[WSL2]\nmemory=4GB\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "no wsl2 section appends one",
			content: "[network]\ndnsProxy=true\n",
			want:    "[network]\ndnsProxy=true\n\n[wsl2]\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "wsl2 before other sections",
			content: "[wsl2]\nmemory=4GB\n[network]\ndnsProxy=true\n",
			want:    "[wsl2]\nmemory=4GB\nvirtio=true\nvirtiofs=true\n[network]\ndnsProxy=true\n",
		},
		{
			name:    "wsl2 not first",
			content: "[network]\ndnsProxy=true\n[wsl2]\nmemory=4GB\n",
			want:    "[network]\ndnsProxy=true\n[wsl2]\nmemory=4GB\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "commented virtiofs ignored",
			content: "[wsl2]\n# virtiofs=true\n",
			want:    "[wsl2]\n# virtiofs=true\nvirtio=true\nvirtiofs=true\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(mergeVirtioFSConfig([]byte(c.content)))
			if got != c.want {
				t.Errorf("mergeVirtioFSConfig(%q) =\n%q\nwant\n%q", c.content, got, c.want)
			}
		})
	}
}

func TestIsWSLPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`\\wsl$\Ubuntu\home\user\app`, true},
		{`\\WSL$\Ubuntu\home\user\app`, true},
		{`\\wsl.localhost\Ubuntu\home\user`, true},
		{`C:\code\myapp`, false},
		{`/home/user/app`, false},
	}
	for _, c := range cases {
		if got := isWSLPath(c.path); got != c.want {
			t.Errorf("isWSLPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestAskYesNo(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"yes", "y\n", true},
		{"yes upper", "Y\n", true},
		{"no", "n\n", false},
		{"empty defaults no", "\n", false},
		{"whitespace defaults no", " \n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := askYesNo(&buf, strings.NewReader(c.input), "continue? "); got != c.want {
				t.Errorf("askYesNo(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run "TestParseWSLVersion|TestWSLSupportsVirtioFS|TestVirtiofsEnabled|TestMergeVirtioFSConfig|TestIsWSLPath|TestAskYesNo" -v`
Expected: FAIL — `undefined: parseWSLVersion` / `undefined: mergeVirtioFSConfig` etc.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/cli/virtiofs.go`:

```go
package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// minWSLVirtioFS is the first WSL release with VirtioFS support for Windows
// drive mounts.
const minWSLVirtioFS = "2.7.1"

// Test seams — overridable from *_test.go.
var (
	virtiofsIsWindows = func() bool { return runtime.GOOS == "windows" }
	virtiofsVersionCmd = func() (string, error) {
		out, err := exec.Command("wsl", "--version").Output()
		return string(out), err
	}
	virtiofsUpdateCmd = func() error { return exec.Command("wsl", "--update").Run() }
	virtiofsConfigPath = func() string {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".wslconfig")
	}
	virtiofsReadFile  = os.ReadFile
	virtiofsWriteFile = os.WriteFile
)

// parseWSLVersion extracts the "WSL version:" line from `wsl --version`
// output (e.g. "WSL version: 2.7.1.0") and returns its major/minor/patch.
func parseWSLVersion(output string) (major, minor, patch int, ok bool) {
	for _, line := range strings.Split(output, "\n") {
		rest, found := strings.CutPrefix(strings.TrimSpace(line), "WSL version:")
		if !found {
			continue
		}
		parts := strings.Split(strings.TrimSpace(rest), ".")
		if len(parts) < 3 {
			continue
		}
		var v [3]int
		for i, p := range parts[:3] {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				break
			}
			v[i] = n
			if i == 2 {
				return v[0], v[1], v[2], true
			}
		}
	}
	return 0, 0, 0, false
}

// wslSupportsVirtioFS reports whether the version is >= 2.7.1.
func wslSupportsVirtioFS(major, minor, patch int) bool {
	if major != 2 {
		return major > 2
	}
	if minor != 7 {
		return minor > 7
	}
	return patch >= 1
}

// wslVersionString formats a parsed version back to x.y.z.
func wslVersionString(major, minor, patch int) string {
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// virtiofsEnabled reports whether the [wsl2] section has virtiofs=true.
func virtiofsEnabled(content []byte) bool {
	section := ""
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.Trim(trimmed, "[]"))
			continue
		}
		if section != "wsl2" {
			continue
		}
		key, val, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		if strings.ToLower(strings.TrimSpace(key)) == "virtiofs" && strings.TrimSpace(val) == "true" {
			return true
		}
	}
	return false
}

// mergeVirtioFSConfig adds [wsl2] virtio=true and virtiofs=true when the
// keys are missing, preserving all existing content and existing keys.
func mergeVirtioFSConfig(content []byte) []byte {
	if len(content) == 0 {
		return []byte("[wsl2]\nvirtio=true\nvirtiofs=true\n")
	}
	var out []string
	wantVirtio, wantVirtiofs := true, true
	sawWSL2, inWSL2 := false, false
	missing := func() []string {
		var keys []string
		if wantVirtio {
			keys = append(keys, "virtio=true")
		}
		if wantVirtiofs {
			keys = append(keys, "virtiofs=true")
		}
		return keys
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inWSL2 {
				out = append(out, missing()...)
			}
			inWSL2 = strings.EqualFold(trimmed, "[wsl2]")
			sawWSL2 = sawWSL2 || inWSL2
			out = append(out, line)
			continue
		}
		if inWSL2 {
			key, _, found := strings.Cut(trimmed, "=")
			if found {
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "virtio":
					wantVirtio = false
				case "virtiofs":
					wantVirtiofs = false
				}
			}
		}
		out = append(out, line)
	}
	if inWSL2 {
		out = append(out, missing()...)
	}
	if !sawWSL2 {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "[wsl2]", "virtio=true", "virtiofs=true")
	}
	joined := strings.Join(out, "\n")
	if strings.HasSuffix(string(content), "\n") {
		joined += "\n"
	}
	return []byte(joined)
}

// isWSLPath reports whether p lives on a WSL filesystem (\\wsl$\ or
// \\wsl.localhost\), where mounts are already native.
func isWSLPath(p string) bool {
	lp := strings.ToLower(filepath.ToSlash(p))
	return strings.HasPrefix(lp, "//wsl$/") || strings.HasPrefix(lp, "//wsl.localhost/")
}

// askYesNo asks a [Y/n] question via prompt; an empty answer means no.
func askYesNo(stdout io.Writer, stdin io.Reader, label string) bool {
	return strings.EqualFold(strings.TrimSpace(prompt(stdout, stdin, label, "n")), "y")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run "TestParseWSLVersion|TestWSLSupportsVirtioFS|TestVirtiofsEnabled|TestMergeVirtioFSConfig|TestIsWSLPath|TestAskYesNo" -v`
Expected: PASS (all cases)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/virtiofs.go internal/cli/virtiofs_test.go
git commit -m "feat(cli): wsl version + wslconfig helpers for virtiofs check"
```

---

### Task 2: maybeEnableVirtioFS flow

**Files:**
- Modify: `internal/cli/virtiofs.go` (add `maybeEnableVirtioFS`)
- Test: `internal/cli/virtiofs_test.go` (add flow tests)

**Interfaces:**
- Consumes: helpers from Task 1 (`parseWSLVersion`, `wslSupportsVirtioFS`, `wslVersionString`, `virtiofsEnabled`, `mergeVirtioFSConfig`, `isWSLPath`, `askYesNo`, `minWSLVirtioFS`) and the seam vars (`virtiofsIsWindows`, `virtiofsVersionCmd`, `virtiofsUpdateCmd`, `virtiofsConfigPath`, `virtiofsReadFile`, `virtiofsWriteFile`). `prompt` from `init.go`.
- Produces:
  - `maybeEnableVirtioFS(stdout io.Writer, stdin io.Reader, projectAbs string) error` — no-op on non-Windows / WSL-path / missing WSL; prompts for `wsl --update` and `.wslconfig` enable; returns error only on write failure.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/virtiofs_test.go` (keep existing imports, add `"errors"` and `"path/filepath"`):

```go
// runVirtioFS wires the seams around maybeEnableVirtioFS and runs it.
// configPath is returned verbatim by the config-path seam; versions are
// served one per versionCmd call. Returns (output, updateRan, err).
func runVirtioFS(t *testing.T, isWindows bool, versions []string, versionErr, updateErr error, configPath, input string) (string, bool, error) {
	t.Helper()
	prevWin := virtiofsIsWindows
	virtiofsIsWindows = func() bool { return isWindows }
	t.Cleanup(func() { virtiofsIsWindows = prevWin })

	vi := 0
	prevVer := virtiofsVersionCmd
	virtiofsVersionCmd = func() (string, error) {
		if vi < len(versions) {
			ver := versions[vi]
			vi++
			return ver, versionErr
		}
		return "", versionErr
	}
	t.Cleanup(func() { virtiofsVersionCmd = prevVer })

	updated := false
	prevUpd := virtiofsUpdateCmd
	virtiofsUpdateCmd = func() error { updated = true; return updateErr }
	t.Cleanup(func() { virtiofsUpdateCmd = prevUpd })

	prevPath := virtiofsConfigPath
	virtiofsConfigPath = func() string { return configPath }
	t.Cleanup(func() { virtiofsConfigPath = prevPath })

	var buf bytes.Buffer
	err := maybeEnableVirtioFS(&buf, strings.NewReader(input), `C:\code\myapp`)
	return buf.String(), updated, err
}

func TestMaybeEnableVirtioFSFlow(t *testing.T) {
	t.Run("non-windows skips", func(t *testing.T) {
		out, updated, err := runVirtioFS(t, false, nil, nil, nil, filepath.Join(t.TempDir(), ".wslconfig"), "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update on non-windows")
		}
		if strings.Contains(out, "VirtioFS") {
			t.Errorf("output = %q, want no virtiofs prompt", out)
		}
	})

	t.Run("wsl-path project skips wsl version call", func(t *testing.T) {
		prevWin := virtiofsIsWindows
		virtiofsIsWindows = func() bool { return true }
		t.Cleanup(func() { virtiofsIsWindows = prevWin })
		versionCalls := 0
		prevVer := virtiofsVersionCmd
		virtiofsVersionCmd = func() (string, error) {
			versionCalls++
			return "WSL version: 2.7.1\n", nil
		}
		t.Cleanup(func() { virtiofsVersionCmd = prevVer })

		var buf bytes.Buffer
		if err := maybeEnableVirtioFS(&buf, strings.NewReader(""), `\\wsl$\Ubuntu\home\user\app`); err != nil {
			t.Fatalf("err: %v", err)
		}
		if versionCalls != 0 {
			t.Errorf("wsl --version must not run for WSL-path projects; calls = %d", versionCalls)
		}
	})

	t.Run("missing wsl skips silently", func(t *testing.T) {
		out, updated, err := runVirtioFS(t, true, nil, errors.New("wsl not found"), nil, filepath.Join(t.TempDir(), ".wslconfig"), "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update when wsl is missing")
		}
		if out != "" {
			t.Errorf("output = %q, want empty (silent skip)", out)
		}
	})

	t.Run("already enabled prints note and skips", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		if err := os.WriteFile(cfgPath, []byte("[wsl2]\nvirtio=true\nvirtiofs=true\n"), 0644); err != nil {
			t.Fatal(err)
		}
		out, updated, err := runVirtioFS(t, true, []string{"WSL version: 2.7.1\n"}, nil, nil, cfgPath, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update")
		}
		if !strings.Contains(out, "already enabled") {
			t.Errorf("output = %q, want 'already enabled'", out)
		}
		got, _ := os.ReadFile(cfgPath)
		if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
			t.Errorf("config changed: %q", got)
		}
	})

	t.Run("old wsl offers update, decline = nothing", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, updated, err := runVirtioFS(t, true, []string{"WSL version: 2.6.2\n"}, nil, nil, cfgPath, "\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update when declined")
		}
		if !strings.Contains(out, "2.7.1") {
			t.Errorf("output = %q, want the 2.7.1 requirement mentioned", out)
		}
		if _, err := os.Stat(cfgPath); err == nil {
			t.Errorf(".wslconfig must not be written when update is declined")
		}
	})

	t.Run("old wsl accepts update then enables", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, updated, err := runVirtioFS(t, true,
			[]string{"WSL version: 2.6.2\n", "WSL version: 2.7.1\n"}, nil, nil, cfgPath, "y\ny\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !updated {
			t.Errorf("wsl --update must run when accepted")
		}
		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read .wslconfig: %v", err)
		}
		if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
			t.Errorf("config = %q, want both keys", got)
		}
		if !strings.Contains(out, "wsl --shutdown") {
			t.Errorf("output = %q, want restart instructions", out)
		}
	})

	t.Run("update still old after update prints rerun note", func(t *testing.T) {
		out, updated, err := runVirtioFS(t, true,
			[]string{"WSL version: 2.6.2\n", "WSL version: 2.6.2\n"}, nil, nil, filepath.Join(t.TempDir(), ".wslconfig"), "y\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !updated {
			t.Errorf("wsl --update must run")
		}
		if !strings.Contains(out, "rerun pier init") {
			t.Errorf("output = %q, want rerun note", out)
		}
	})

	t.Run("enable declines = no write", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, _, err := runVirtioFS(t, true, []string{"WSL version: 2.7.1\n"}, nil, nil, cfgPath, "n\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, err := os.Stat(cfgPath); err == nil {
			t.Errorf(".wslconfig must not be written when declined")
		}
		if strings.Contains(out, "wsl --shutdown") {
			t.Errorf("output = %q, want no instructions on decline", out)
		}
	})

	t.Run("enable creates file with both keys", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, updated, err := runVirtioFS(t, true, []string{"WSL version: 2.7.1\n"}, nil, nil, cfgPath, "y\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update when version is sufficient")
		}
		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read .wslconfig: %v", err)
		}
		if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
			t.Errorf("config = %q, want both keys", got)
		}
		if !strings.Contains(out, "wsl --shutdown") {
			t.Errorf("output = %q, want restart instructions", out)
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestMaybeEnableVirtioFSFlow`
Expected: FAIL — `undefined: maybeEnableVirtioFS`

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/cli/virtiofs.go`:

```go
// maybeEnableVirtioFS checks the Windows WSL environment and offers to
// enable VirtioFS for faster Docker bind mounts from Windows drives. It
// only mutates after an explicit yes, and never runs wsl --shutdown; the
// restart is left to the printed instructions. No-ops (silently) when not
// on Windows, when the project already lives on a WSL filesystem, or when
// wsl.exe is unavailable.
func maybeEnableVirtioFS(stdout io.Writer, stdin io.Reader, projectAbs string) error {
	if !virtiofsIsWindows() {
		return nil
	}
	if isWSLPath(projectAbs) {
		return nil
	}
	out, err := virtiofsVersionCmd()
	if err != nil {
		return nil // no WSL installed
	}
	major, minor, patch, ok := parseWSLVersion(out)
	if !ok {
		fmt.Fprintln(stdout, "pier: could not read the WSL version; skipping the VirtioFS check")
		return nil
	}
	if !wslSupportsVirtioFS(major, minor, patch) {
		fmt.Fprintf(stdout, "WSL %s is installed; WSL %s+ is needed for VirtioFS (faster Docker file mounts)\n",
			wslVersionString(major, minor, patch), minWSLVirtioFS)
		if !askYesNo(stdout, stdin, "Run wsl --update now? [Y/n]: ") {
			return nil
		}
		if err := virtiofsUpdateCmd(); err != nil {
			return fmt.Errorf("run wsl --update: %w", err)
		}
		out, err = virtiofsVersionCmd()
		if err != nil {
			return nil
		}
		major, minor, patch, ok = parseWSLVersion(out)
		if !ok || !wslSupportsVirtioFS(major, minor, patch) {
			fmt.Fprintf(stdout, "pier: update WSL to %s+ and rerun pier init to finish the VirtioFS setup\n", minWSLVirtioFS)
			return nil
		}
	}
	path := virtiofsConfigPath()
	if path == "" {
		return nil
	}
	if existing, err := virtiofsReadFile(path); err == nil && virtiofsEnabled(existing) {
		fmt.Fprintf(stdout, "pier: VirtioFS already enabled in %s\n", path)
		return nil
	}
	fmt.Fprintln(stdout, "WSL VirtioFS makes Docker bind mounts from Windows drives much faster.")
	fmt.Fprintln(stdout, "It is experimental: expect file-permission quirks, and strict databases")
	fmt.Fprintln(stdout, "(PostgreSQL/MySQL) on host directories may fail to start (use named volumes).")
	if _, err := virtiofsReadFile(path); err == nil {
		fmt.Fprintf(stdout, "Will merge into %s (existing keys are kept).\n", path)
	} else {
		fmt.Fprintf(stdout, "Will create %s.\n", path)
	}
	if !askYesNo(stdout, stdin, "Enable VirtioFS now? [Y/n]: ") {
		return nil
	}
	var existing []byte
	if b, err := virtiofsReadFile(path); err == nil {
		existing = b
	}
	if err := virtiofsWriteFile(path, mergeVirtioFSConfig(existing), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(stdout, "Updated %s: [wsl2] virtio=true, virtiofs=true\n", path)
	fmt.Fprintln(stdout, "To apply, restart Docker Desktop:")
	fmt.Fprintln(stdout, "  1. Quit Docker Desktop")
	fmt.Fprintln(stdout, "  2. Run: wsl --shutdown")
	fmt.Fprintln(stdout, "  3. Start Docker Desktop")
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run "TestMaybeEnableVirtioFSFlow|TestParseWSLVersion|TestWSLSupportsVirtioFS|TestVirtiofsEnabled|TestMergeVirtioFSConfig|TestIsWSLPath|TestAskYesNo" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/virtiofs.go internal/cli/virtiofs_test.go
git commit -m "feat(cli): offer virtiofs enable during windows init"
```

---

### Task 3: Wire into runInit + init smoke test

**Files:**
- Modify: `internal/cli/init.go` (call `maybeEnableVirtioFS` in `runInit`)
- Test: `internal/cli/init_test.go` (add `TestInitAsksVirtioFS`)

**Interfaces:**
- Consumes: `maybeEnableVirtioFS(stdout io.Writer, stdin io.Reader, projectAbs string) error` from Task 2; `runInit`'s existing `abs`, `cmd.OutOrStdout()`, `cmd.InOrStdin()`.
- Produces: none — the init flow now runs the check before the prompt/TUI sequence.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/init_test.go`:

```go
func TestInitAsksVirtioFS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, ".wslconfig")
	prevWin := virtiofsIsWindows
	virtiofsIsWindows = func() bool { return true }
	t.Cleanup(func() { virtiofsIsWindows = prevWin })
	prevVer := virtiofsVersionCmd
	virtiofsVersionCmd = func() (string, error) { return "WSL version: 2.7.1\n", nil }
	t.Cleanup(func() { virtiofsVersionCmd = prevVer })
	prevPath := virtiofsConfigPath
	virtiofsConfigPath = func() string { return cfgPath }
	t.Cleanup(func() { virtiofsConfigPath = prevPath })

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	// y answers the VirtioFS prompt; the later init prompts hit EOF and take defaults.
	root.SetIn(strings.NewReader("y\n"))
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
		t.Errorf("config = %q, want both virtiofs keys", got)
	}
	if !strings.Contains(buf.String(), "VirtioFS") {
		t.Errorf("output = %q, want the VirtioFS prompt", buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestInitAsksVirtioFS`
Expected: FAIL — `.wslconfig` not created (the call site doesn't exist yet)

- [ ] **Step 3: Wire the call into runInit**

In `internal/cli/init.go`, replace:

```go
	if !laravelpkg.New().Detect(abs) {
		return fmt.Errorf("no Laravel project found at %s (missing composer.json with laravel/framework or artisan)", abs)
	}
	tomlPath := filepath.Join(abs, "pier.toml")
```

with:

```go
	if !laravelpkg.New().Detect(abs) {
		return fmt.Errorf("no Laravel project found at %s (missing composer.json with laravel/framework or artisan)", abs)
	}
	if err := maybeEnableVirtioFS(cmd.OutOrStdout(), cmd.InOrStdin(), abs); err != nil {
		return err
	}
	tomlPath := filepath.Join(abs, "pier.toml")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run "TestInit" -v`
Expected: PASS. On Linux CI `virtiofsIsWindows` is real and returns false, so the existing init tests skip the check; on a Windows dev machine the real `wsl --version` runs but empty stdin declines every prompt (no writes).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): ask for virtiofs enable in pier init on windows"
```

---

### Task 4: Docs

**Files:**
- Modify: `README.md` (Troubleshooting section), `CHANGELOG.md` (top, new Unreleased section)

**Interfaces:** none.

- [ ] **Step 1: Add the Troubleshooting entry**

In `README.md`, after the "Vite dev server unreachable" bullet (~line 692), add:

```markdown
- **Docker bind mounts slow on Windows dev** — container mounts from
  `C:\` / `D:\` cross into the WSL VM over 9P. Run `pier init` on
  Windows to enable WSL VirtioFS, or set it manually: add `virtio=true`
  and `virtiofs=true` under `[wsl2]` in `%USERPROFILE%\.wslconfig`,
  update WSL to 2.7.1+ (`wsl --update`), then run `wsl --shutdown` and
  restart Docker Desktop. VirtioFS is experimental: file permissions can
  behave oddly and strict databases (PostgreSQL/MySQL) on host bind
  mounts may fail to start — use a named volume for database data.
```

- [ ] **Step 2: Add the CHANGELOG entry**

At the top of `CHANGELOG.md`, before the `## v0.0.7-beta` line, add:

```markdown
## Unreleased

### Added

- `pier init` on Windows detects old WSL / missing VirtioFS config and
  offers (with `[Y/n]` consent) to run `wsl --update` and add
  `[wsl2] virtio=true` / `virtiofs=true` to `%USERPROFILE%\.wslconfig`,
  making Docker bind mounts from Windows drives much faster. Existing
  `.wslconfig` keys are never overwritten; the project must be on a
  Windows drive (WSL paths are already native).
```

- [ ] **Step 3: Run the full suite**

Run: `gofmt -l internal/cli/virtiofs.go internal/cli/virtiofs_test.go` then `go vet ./...` and `go test ./...`
Expected: gofmt prints nothing; vet clean; all tests pass

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: virtiofs windows troubleshooting + changelog entry"
```

---

## Verification

After all tasks:

1. `go build ./...`, `go vet ./...`, `go test ./...` all pass.
2. `gofmt -l .` reports nothing.
3. Grep confirms: `maybeEnableVirtioFS` is called from `runInit` exactly once; `wsl --shutdown` appears only in printed text, never in `exec.Command`.
4. Manual (Windows): `pier init` in a scratch Laravel dir with a populated `.wslconfig` shows the merge prompt and preserves `memory=4GB`; with `virtiofs=true` already set it prints "already enabled"; a `\\wsl$\` project path skips cleanly.

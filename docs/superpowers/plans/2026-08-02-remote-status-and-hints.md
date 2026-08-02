# Remote Status Probe and Remote-Aware Hints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pier status <env>` (a full remote health probe over SSH) and make error hints remote-aware so remote docker failures stop pointing at local `pier status`/`pier dev`.

**Architecture:** The `deploy` package gains a `RemoteHost` field on `ExitError` plus remote-aware constructors; `PrintError` resolves the hint from the error chain (disk-full → targeted prune hint, remote → remote-inspect hint, else kind hint). A new `deploy.RemoteStatus` function probes the remote host via a small `StatusRunner` interface; the CLI dials a real client, the tests inject a fake — the same seam pattern as the existing pipeline (`pipelineDial`, `pipelineProbe`).

**Tech Stack:** Go (no new dependencies), cobra CLI, existing `internal/deploy` SSH client (`golang.org/x/crypto/ssh`).

## Global Constraints

- Module is `github.com/Bonnary/pier`; run tests from the repo root with `go test ./...`.
- No new dependencies; stdlib only (`net/http/httptest` for health tests).
- No changes to exit codes, sentinel errors, or `Kind` values.
- Follow existing seam/fake test patterns (`pipelineDial`, `fakeSSHClient`).
- Comments match existing style: explain "why" at package/type level, keep line length reasonable.
- Plain `pier status` (no args) behavior must remain byte-identical to today.
- Spec: `docs/superpowers/specs/2026-08-02-remote-status-and-hints-design.md`

**Noted deviation from spec (approved in conversation):** the spec's `RemoteStatus` signature listed an `SSHConfig` parameter; the connection is carried by the `StatusRunner` parameter instead, so `RemoteStatus` does not take `SSHConfig` (the CLI dials and passes the client, exactly as the spec's CLI-wiring section describes).

---

### Task 1: Remote-aware ExitError constructors (deploy package)

**Files:**
- Modify: `internal/deploy/errors.go:51-55` (ExitError struct), `internal/deploy/errors.go:84-85` (constructors)
- Modify: `internal/deploy/deploy.go:90,183,185` (pipeline uses remote constructors)
- Test: `internal/deploy/errors_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `ExitError` gains field `RemoteHost string` (empty means "local error").
  - `func RemoteBuildError(host string, err error) error` → `*ExitError` with `Code: ExitBuild, Kind: KindDocker, RemoteHost: host`
  - `func RemoteUpError(host string, err error) error` → `*ExitError` with `Code: ExitUp, Kind: KindDocker, RemoteHost: host`
  - `func RemoteDockerError(host string, err error) error` → `*ExitError` with `Code: ExitGeneral, Kind: KindDocker, RemoteHost: host` (used by the status command in Task 4)

- [ ] **Step 1: Write the failing tests**

Add to `internal/deploy/errors_test.go`:

```go
func TestRemoteConstructorsSetHost(t *testing.T) {
	base := errors.New("base")
	cases := []struct {
		name string
		got  error
		want string
	}{
		{"RemoteBuildError", RemoteBuildError("prod.example.com", base), "prod.example.com"},
		{"RemoteUpError", RemoteUpError("prod.example.com", base), "prod.example.com"},
		{"RemoteDockerError", RemoteDockerError("prod.example.com", base), "prod.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ee *ExitError
			if !errors.As(c.got, &ee) {
				t.Fatalf("not *ExitError")
			}
			if ee.RemoteHost != c.want {
				t.Errorf("RemoteHost = %q, want %q", ee.RemoteHost, c.want)
			}
			if !errors.Is(c.got, base) {
				t.Errorf("errors.Is(base) = false, want true")
			}
		})
	}
}
```

Extend the `TestExistingConstructorsDefaultKind` table (errors_test.go:80-85) with three rows:

```go
		{"RemoteBuildError", RemoteBuildError("h", base), ExitBuild, KindDocker},
		{"RemoteUpError", RemoteUpError("h", base), ExitUp, KindDocker},
		{"RemoteDockerError", RemoteDockerError("h", base), ExitGeneral, KindDocker},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestRemoteConstructorsSetHost|TestExistingConstructorsDefaultKind'`
Expected: FAIL — compile error `RemoteBuildError undefined` and `RemoteHost` unknown field.

- [ ] **Step 3: Implement**

In `internal/deploy/errors.go`, add the field to `ExitError` (errors.go:51-55):

```go
// ExitError wraps a sentinel with a process exit code and a Kind
// (config / docker / ssh / network / user / unknown). The CLI's
// PrintError reads Kind to color the output and to look up a
// category-specific hint. RemoteHost, when non-empty, marks the
// error as a remote (SSH) command failure so PrintError renders a
// remote-aware hint instead of the local pier hint.
type ExitError struct {
	Code int
	Kind Kind
	// RemoteHost is the SSH host a failed remote command ran on.
	// Empty means the failure was local.
	RemoteHost string
	Err        error
}
```

Replace the constructors (errors.go:84-85) with:

```go
func BuildError(err error) error { return &ExitError{Code: ExitBuild, Kind: KindDocker, Err: err} }
func UpError(err error) error    { return &ExitError{Code: ExitUp, Kind: KindDocker, Err: err} }

// RemoteBuildError is BuildError stamped with the SSH host the build
// ran on, so the CLI can render a remote-aware hint.
func RemoteBuildError(host string, err error) error {
	return &ExitError{Code: ExitBuild, Kind: KindDocker, RemoteHost: host, Err: err}
}

// RemoteUpError is UpError stamped with the SSH host the up ran on.
func RemoteUpError(host string, err error) error {
	return &ExitError{Code: ExitUp, Kind: KindDocker, RemoteHost: host, Err: err}
}

// RemoteDockerError is a general docker failure stamped with the SSH
// host, used by `pier status <env>` when a remote probe command fails.
func RemoteDockerError(host string, err error) error {
	return &ExitError{Code: ExitGeneral, Kind: KindDocker, RemoteHost: host, Err: err}
}
```

In `internal/deploy/deploy.go`, point the pipeline's remote failures at the host-aware constructors:

- deploy.go:90 — `return BuildError(err)` → `return RemoteBuildError(p.SSH.Host, err)`
- deploy.go:183 — `return UpError(err)` → `return RemoteUpError(p.SSH.Host, err)`
- deploy.go:185 — `return UpError(fmt.Errorf("health check failed; rolled back"))` → `return RemoteUpError(p.SSH.Host, fmt.Errorf("health check failed; rolled back"))`

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/errors.go internal/deploy/errors_test.go internal/deploy/deploy.go
git commit -m "feat(deploy): remote-aware ExitError constructors stamp SSH host"
```

---

### Task 2: Remote-aware hint resolution in PrintError (cli package)

**Files:**
- Modify: `internal/cli/errdisplay.go:149` (hint resolution), imports
- Test: `internal/cli/errdisplay_test.go`

**Interfaces:**
- Consumes: `ExitError.RemoteHost` (Task 1).
- Produces: `func resolveHint(ee *ExitError, chain []string) string` — hint precedence: disk-full (any level of chain) → remote host → kind hint → empty.

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/errdisplay_test.go` (import `"github.com/Bonnary/pier/internal/deploy"` if not already imported):

```go
func TestPrintError_RemoteDiskFullHint(t *testing.T) {
	w := &bytes.Buffer{}
	base := fmt.Errorf("failed to update builder last activity time: write /home/host/.docker/buildx/activity/.tmp-default3263565124: no space left on device")
	err := deploy.RemoteBuildError("prod.example.com", base)
	PrintError(w, err, false, false)
	got := w.String()
	want := "host prod.example.com is out of disk space: ssh in and run 'docker builder prune -af', then check 'docker system df'"
	if !strings.Contains(got, want) {
		t.Errorf("output missing disk-full hint %q\nfull output:\n%s", want, got)
	}
	if strings.Contains(got, "pier dev") {
		t.Errorf("disk-full error must not show local pier hint:\n%s", got)
	}
}

func TestPrintError_RemoteGenericHint(t *testing.T) {
	w := &bytes.Buffer{}
	err := deploy.RemoteUpError("prod.example.com", errors.New("compose up failed"))
	PrintError(w, err, false, false)
	got := w.String()
	want := "command failed on prod.example.com: ssh in and run 'docker compose ps' / 'docker system df' to inspect"
	if !strings.Contains(got, want) {
		t.Errorf("output missing remote hint %q\nfull output:\n%s", want, got)
	}
	if strings.Contains(got, "pier dev") {
		t.Errorf("remote error must not show local pier hint:\n%s", got)
	}
}

func TestPrintError_RemoteDiskFullWithoutExitError(t *testing.T) {
	w := &bytes.Buffer{}
	PrintError(w, fmt.Errorf("write /var/lib/docker: no space left on device"), false, false)
	got := w.String()
	if !strings.Contains(got, "out of disk space") {
		t.Errorf("plain disk-full error should still get the disk-full hint:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestPrintError_Remote'`
Expected: FAIL — the disk-full output contains the old local hint (`run 'pier status' ... 'pier dev'`).

- [ ] **Step 3: Implement**

In `internal/cli/errdisplay.go`:
- Add `"strings"` to the imports.
- Add before `PrintError` (after `ansiColor`/`itoa` helpers):

```go
// diskFullNeedle is the classic ENOSPC message docker surfaces when a
// host runs out of disk space (e.g. the buildx activity write that
// fails during a remote build).
const diskFullNeedle = "no space left on device"

// resolveHint picks the remediation hint for an error render. A
// disk-full failure anywhere in the chain gets a targeted prune hint;
// a remote (SSH) command failure gets a remote-inspect hint; anything
// else falls back to the kind hint (local docker errors keep the
// `pier status` / `pier dev` hint).
func resolveHint(ee *ExitError, chain []string) string {
	for _, msg := range chain {
		if strings.Contains(msg, diskFullNeedle) {
			host := "the remote host"
			if ee != nil && ee.RemoteHost != "" {
				host = ee.RemoteHost
			}
			return fmt.Sprintf("host %s is out of disk space: ssh in and run 'docker builder prune -af', then check 'docker system df'", host)
		}
	}
	if ee != nil && ee.RemoteHost != "" {
		return fmt.Sprintf("command failed on %s: ssh in and run 'docker compose ps' / 'docker system df' to inspect", ee.RemoteHost)
	}
	if ee != nil {
		return ee.Kind.Hint()
	}
	return ""
}
```

- Replace errdisplay.go:149 (`hint := kind.Hint()`) with:

```go
	hint := resolveHint(ee, chain)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/`
Expected: PASS (existing tests unchanged: local `DockerError` has empty `RemoteHost`, so `resolveHint` falls through to `kind.Hint()`).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/errdisplay.go internal/cli/errdisplay_test.go
git commit -m "fix(cli): remote-aware hints for remote docker failures"
```

---

### Task 3: RemoteStatus probe (deploy package)

**Files:**
- Create: `internal/deploy/status.go`
- Test: `internal/deploy/status_test.go`

**Interfaces:**
- Consumes: `config.DeployConfig` (`Path`), `HealthConfig`, `Probe` (health.go), `State` + `stateFile` (state.go).
- Produces:
  - `type StatusRunner interface { Run(ctx context.Context, cmd string) ([]byte, []byte, error); Close() error }` — satisfied by `*Client`.
  - `type StatusReport struct { Containers, Disk, DockerDisk string; State *State; Healthy bool }`
  - `func RemoteStatus(ctx context.Context, de config.DeployConfig, health HealthConfig, r StatusRunner) (*StatusReport, error)` — runs `docker compose -f docker-compose.prod.yml ps` in `de.Path`, `df -h <de.Path>`, `docker system df`, `cat <de.Path>/.pier/state.json` (missing file is fine → nil State), and a single-attempt health probe.

- [ ] **Step 1: Write the failing tests**

Create `internal/deploy/status_test.go`:

```go
package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

// fakeStatusRunner answers each RemoteStatus command by substring
// match against resp keys, so tests don't need the exact interpolated
// command strings.
type fakeStatusRunner struct {
	cmds []string
	resp map[string]string
	err  error
}

func (f *fakeStatusRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	if f.err != nil {
		return nil, nil, f.err
	}
	for key, out := range f.resp {
		if strings.Contains(cmd, key) {
			return []byte(out), nil, nil
		}
	}
	return nil, nil, nil
}

func (f *fakeStatusRunner) Close() error { return nil }

func healthServer(code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
}

func TestRemoteStatusHappyPath(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{
		"compose ps": "abc  app  Up 2 hours",
		"df -h":      "/dev/sda  20G  15G",
		"system df":  "Images  5  3",
		"state.json": `{"current":"sha1","previous":"sha0","deployed_at":"2026-08-02T05:00:00Z","deployed_by":"u@h"}`,
	}}
	rep, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if !strings.Contains(rep.Containers, "app") {
		t.Errorf("Containers = %q, want to contain app", rep.Containers)
	}
	if !strings.Contains(rep.Disk, "20G") {
		t.Errorf("Disk = %q, want to contain 20G", rep.Disk)
	}
	if !strings.Contains(rep.DockerDisk, "Images") {
		t.Errorf("DockerDisk = %q, want to contain Images", rep.DockerDisk)
	}
	if rep.State == nil || rep.State.Current != "sha1" || rep.State.DeployedAt == "" {
		t.Errorf("State = %+v, want current=sha1 with deployed_at", rep.State)
	}
	if !rep.Healthy {
		t.Errorf("Healthy = false, want true")
	}
}

func TestRemoteStatusMissingState(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{
		"compose ps": "abc  app  Up",
		"df -h":      "Filesystem  Size  Used",
		"system df":  "Images  5  3",
	}}
	rep, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if rep.State != nil {
		t.Errorf("State = %+v, want nil for missing state.json", rep.State)
	}
}

func TestRemoteStatusHealthDown(t *testing.T) {
	srv := healthServer(500)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{"compose ps": "x"}}
	rep, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if rep.Healthy {
		t.Errorf("Healthy = true, want false for 500 response")
	}
}

func TestRemoteStatusCommandFailure(t *testing.T) {
	f := &fakeStatusRunner{err: errors.New("boom")}
	_, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: "http://127.0.0.1:1", Timeout: time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err == nil {
		t.Fatal("RemoteStatus = nil error, want non-nil on command failure")
	}
	if !strings.Contains(err.Error(), "docker compose ps") {
		t.Errorf("error = %q, want mention of the failing command", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestRemoteStatus`
Expected: FAIL — compile error `undefined: RemoteStatus`.

- [ ] **Step 3: Implement**

Create `internal/deploy/status.go`:

```go
package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Bonnary/pier/internal/config"
)

// StatusRunner is the subset of *Client that RemoteStatus needs:
// Run to capture command output and Close to release the connection.
// The CLI dials a real client and passes it; tests inject a fake.
type StatusRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	Close() error
}

var _ StatusRunner = (*Client)(nil)

// StatusReport is the result of a `pier status <env>` probe of a
// remote host: raw command output for containers and disk, the last
// deploy record, and a single HTTP health verdict.
type StatusReport struct {
	Containers string // `docker compose -f docker-compose.prod.yml ps` output
	Disk       string // `df -h <path>` output
	DockerDisk string // `docker system df` output
	State      *State // parsed .pier/state.json; nil when the file is absent
	Healthy    bool   // single HTTP GET of health.URL returned 2xx
}

// RemoteStatus probes the remote host behind r: container state,
// deploy-path and docker disk usage, the last deploy record, and one
// HTTP health check against health.URL. A missing state file is
// normal (a project with no deploys yet) and yields a nil State. A
// failed health probe sets Healthy=false instead of failing the
// probe; a failed probe command (compose, df, docker system df, or
// an unreadable state file) returns an error.
func RemoteStatus(ctx context.Context, de config.DeployConfig, health HealthConfig, r StatusRunner) (*StatusReport, error) {
	rep := &StatusReport{}

	out, _, err := r.Run(ctx, fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml ps", de.Path))
	if err != nil {
		return nil, fmt.Errorf("remote `docker compose ps` failed: %w", err)
	}
	rep.Containers = strings.TrimRight(string(out), "\n")

	out, _, err = r.Run(ctx, fmt.Sprintf("df -h %s", de.Path))
	if err != nil {
		return nil, fmt.Errorf("remote `df -h` failed: %w", err)
	}
	rep.Disk = strings.TrimRight(string(out), "\n")

	out, _, err = r.Run(ctx, "docker system df")
	if err != nil {
		return nil, fmt.Errorf("remote `docker system df` failed: %w", err)
	}
	rep.DockerDisk = strings.TrimRight(string(out), "\n")

	out, _, err = r.Run(ctx, fmt.Sprintf("cat %s", filepath.Join(de.Path, stateFile)))
	if err == nil {
		var s State
		if jerr := json.Unmarshal(out, &s); jerr != nil {
			return nil, fmt.Errorf("remote state.json parse: %w", jerr)
		}
		rep.State = &s
	}

	rep.Healthy = Probe(ctx, health) == nil
	return rep, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/status.go internal/deploy/status_test.go
git commit -m "feat(deploy): remote status probe over SSH"
```

---

### Task 4: `pier status <env>` CLI wiring (cli package)

**Files:**
- Modify: `internal/cli/status.go`
- Test: `internal/cli/status_test.go`

**Interfaces:**
- Consumes: `deploy.RemoteStatus`, `deploy.StatusRunner`, `deploy.Dial`, `deploy.SSHConfig`, `deploy.DefaultHealthConfig`, `deploy.ResolvedURL`, `deploy.RemoteDockerError` (Tasks 1, 3).
- Produces: `pier status [env]` — no arg = today's local output unchanged; one arg = remote probe printing project, env+host, services, containers, disk, docker disk, health, last deploy.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/status_test.go` (add imports: `"context"`, `"errors"`, `"net/http"`, `"net/http/httptest"`, `"github.com/Bonnary/pier/internal/deploy"`):

```go
type fakeStatusRunner struct {
	cmds []string
}

func (f *fakeStatusRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	switch {
	case strings.Contains(cmd, "compose"):
		return []byte("abc  app  Up 2 hours"), nil, nil
	case strings.Contains(cmd, "state.json"):
		return []byte(`{"current":"sha1","deployed_at":"2026-08-02T05:00:00Z","deployed_by":"u@h"}`), nil, nil
	}
	return []byte("out"), nil, nil
}

func (f *fakeStatusRunner) Close() error { return nil }

func TestStatusRemoteSuccess(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	origDial, origURL := statusDial, statusHealthURL
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		return &fakeStatusRunner{}, nil
	}
	statusHealthURL = func(cfg *config.Config, env string) string { return srv.URL }
	defer func() { statusDial, statusHealthURL = origDial, origURL }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"env:     production (host: h)",
		"containers:",
		"app  Up 2 hours",
		"health: OK",
		"last deploy: sha1, 2026-08-02T05:00:00Z by u@h",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestStatusRemoteNoEnvSection(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("status production with no [deploy.production] = nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "no [deploy.production] section in pier.toml") {
		t.Errorf("error = %q, want missing-section message", err)
	}
}

func TestStatusRemoteDialFailure(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	orig := statusDial
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		return nil, errors.New("boom")
	}
	defer func() { statusDial = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("dial failure = nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want to contain boom", err)
	}
}
```

Note: `TestStatusRemoteSuccess` references `config.Config` in the `statusHealthURL` stub — import `"github.com/Bonnary/pier/internal/config"` too.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestStatusRemote`
Expected: FAIL — compile errors (`statusDial undefined`).

- [ ] **Step 3: Implement**

Rewrite `internal/cli/status.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

// statusDial is a seam for tests to inject a fake StatusRunner into
// `pier status <env>` without a real SSH connection.
var statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
	return deploy.Dial(ctx, cfg)
}

// statusHealthURL is a seam for tests to point the remote health
// probe at a local test server instead of the project domain.
var statusHealthURL = func(cfg *config.Config, env string) string {
	return deploy.ResolvedURL(*cfg, env) + "/up"
}

func newStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status [env]",
		Short: "Show project and container status (add <env> to probe a remote host)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, args)
		},
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		return runRemoteStatus(cmd, cfg, args[0])
	}
	dir := filepath.Dir(cfgPath)
	c := &docker.Compose{Workdir: dir, File: filepath.Join(dir, "docker-compose.yml"), Runner: dockerRunner}
	ps, err := c.PS(context.Background())
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", cfg.Project.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "domain:  %s\n", cfg.Project.Domain)
	fmt.Fprintf(cmd.OutOrStdout(), "stack:   %s (php %s, node %s)\n", cfg.Stack.Type, cfg.Stack.PHP, cfg.Stack.Node)
	fmt.Fprintf(cmd.OutOrStdout(), "services: %v\n", cfg.Stack.Services)
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", string(ps))
	return nil
}

// runRemoteStatus probes the [deploy.<env>] host over SSH: container
// state, disk usage, last deploy record, and a single health check.
func runRemoteStatus(cmd *cobra.Command, cfg *config.Config, env string) error {
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	client, err := statusDial(cmd.Context(), deploy.SSHConfig{Host: dc.Host, User: dc.User, KeyPath: sshKeyPath()})
	if err != nil {
		return SSHError(err)
	}
	defer client.Close()

	health := deploy.DefaultHealthConfig(cfg.Project.Domain)
	health.URL = statusHealthURL(cfg, env)
	health.Timeout = 10 * time.Second
	health.Interval = 100 * time.Millisecond
	health.MaxAttempts = 1

	rep, err := deploy.RemoteStatus(cmd.Context(), dc, health, client)
	if err != nil {
		return deploy.RemoteDockerError(dc.Host, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", cfg.Project.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "env:     %s (host: %s)\n", env, dc.Host)
	fmt.Fprintf(cmd.OutOrStdout(), "services: %v\n", cfg.Stack.Services)
	fmt.Fprintf(cmd.OutOrStdout(), "containers:\n%s\n", indentBlock(rep.Containers))
	fmt.Fprintf(cmd.OutOrStdout(), "disk:\n%s\n", indentBlock(rep.Disk))
	fmt.Fprintf(cmd.OutOrStdout(), "docker disk:\n%s\n", indentBlock(rep.DockerDisk))
	if rep.Healthy {
		fmt.Fprintf(cmd.OutOrStdout(), "health: OK (%s)\n", health.URL)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "health: DOWN (%s)\n", health.URL)
	}
	if rep.State != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "last deploy: %s, %s by %s\n", rep.State.Current, rep.State.DeployedAt, rep.State.DeployedBy)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "last deploy: none yet\n")
	}
	return nil
}

// indentBlock prefixes every line of s with two spaces, showing
// "(none)" for empty probe output.
func indentBlock(s string) string {
	if s == "" {
		return "  (none)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
```

Keep `sshKeyPath()` and `homeDir()` in the file unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/`
Expected: PASS (the two pre-existing local-status tests still pass — no args takes the unchanged local branch).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/status.go internal/cli/status_test.go
git commit -m "feat(cli): pier status <env> probes remote host over SSH"
```

---

### Task 5: Documentation (README + CHANGELOG)

**Files:**
- Modify: `README.md` (line ~226 command table, line ~81 features list)
- Modify: `CHANGELOG.md` (v0.0.2-beta Added section)

- [ ] **Step 1: Update the README command table**

In `README.md`, change the status row (~line 226):

```markdown
| `pier status` | Show project and container status for the current env. |
```
to:
```markdown
| `pier status [env]` | Show project and container status; pass an env name to probe the remote host over SSH (containers, disk, health, last deploy). |
```

In the features list (~line 81), change:

```markdown
- **`pier status`** — One-glance project + container status.
```
to:
```markdown
- **`pier status [env]`** — One-glance project + container status, locally or on a remote deploy host (containers, disk, health, last deploy).
```

- [ ] **Step 2: Update the CHANGELOG**

In `CHANGELOG.md`, under the v0.0.2-beta `### Added` section, add:

```markdown
- `pier status <env>` probes a remote deploy host over SSH: container state, deploy-path and docker disk usage, a one-shot health check, and the last deploy record from `.pier/state.json`. `pier status` with no env still shows local status only.
```

- [ ] **Step 3: Verify build and full test suite**

Run: `go build ./... && go test ./...`
Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: README and changelog for remote status and remote-aware hints"
```

---

## Self-Review Notes

- **Spec coverage:** Section 1 (hint fix) → Tasks 1–2. Section 2 (remote status) → Tasks 3–4. Docs → Task 5. All covered.
- **Deviations from spec:** `RemoteStatus` drops the `SSHConfig` parameter (runner carries the connection; documented in plan header and in the code comment). `RemoteDockerError` added so a failed remote status probe renders the remote hint instead of the local pier hint — direct application of the spec's hint intent.
- **Type consistency:** `StatusRunner` (Run + Close) is defined once in Task 3 and consumed by Task 4's `statusDial` seam; `resolveHint` signature matches usage in Task 2; `RemoteHost` field name consistent across Tasks 1–2.
- **Verification commands:** every task runs `go test` scoped to its packages; Task 5 runs the full suite.

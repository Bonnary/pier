# Per-Env `tls` Flag (HTTP-only production mode) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make production deployments work over plain HTTP by default by adding a per-env `tls` flag (`[deploy.<env>].tls`, default `false`) that flips every URL surface (health probe, `APP_URL`, displayed URL, webserver port mapping) between http:80 and https:443, and make the health probe target the deploy host IP instead of the public domain.

**Architecture:** The laravel stack package gains two shared helpers (`WebScheme`, `WebPort`) that resolve scheme and port from the env's `tls` flag and `laravel` port override. The deploy package's `ResolvedURL` and a new `HealthURL` use them; `DefaultHealthConfig` builds its probe URL from `HealthURL`. `renderProdCompose`/`renderProdEnvExample` thread the flag through webserver port mapping and `APP_URL`. Nginx render is unchanged (listens on 80 only); HTTPS certs are a later feature.

**Tech Stack:** Go 1.25+, cobra CLI, gopkg.in/yaml.v3, TOML via the `toml` package (see `internal/config/parse.go`), stdlib `net/http`.

## Global Constraints

- `[deploy.<env>].tls` defaults to `false` (absent = false; no validation needed — it's a bool).
- `tls = false` → scheme `http`, laravel container port 80, laravel default host port 80; `webserver_http` not published by default (explicit override still honored).
- `tls = true` → scheme `https`, laravel container port 443, laravel default host port 443; `webserver_http` published (container 80). HTTPS is NOT yet served by nginx — certs come later; do not add cert files or nginx 443 blocks in this plan.
- Health probe URL = `scheme://<deploy.<env>.host>:<port>/up` (host IP, not the domain). `ResolvedURL` stays domain-based for display.
- `laravel` port override `0` (don't expose) falls back to the scheme default (80/443) in URLs, matching today's behavior.
- Do not mutate the shared `ProdPortDefaults` map.
- No version constant bump; CHANGELOG gets an `## Unreleased` section.
- Every code change ships with its tests (TDD: failing test first), then `gofmt` + `go test ./...` must pass before commit.
- Commit messages follow repo style: `feat(config): ...`, `feat(deploy): ...`, `feat(laravel): ...`, `docs: ...`.

## File Structure

| File | Responsibility | Change |
| --- | --- | --- |
| `internal/config/config.go` | pier.toml schema | add `TLS` field to `DeployConfig` (Task 1) |
| `internal/config/testdata/full-ports.toml` | config test fixture | add `tls = true` (Task 1) |
| `internal/config/parse_test.go` | config parse tests | assert TLS round-trip + default (Task 1) |
| `internal/stack/laravel/ports.go` | port/scheme helpers | add `WebScheme`, `WebPort` (Task 2) |
| `internal/stack/laravel/ports_test.go` | helper tests | tests for both helpers (Task 2) |
| `internal/deploy/health.go` | health probe config | `DefaultHealthConfig(cfg, env)` (Task 3) |
| `internal/deploy/deploy.go` | URL builders | `ResolvedURL` scheme-aware; new `HealthURL` (Task 3) |
| `internal/cli/deploy.go` | deploy command | new `DefaultHealthConfig` call (Task 3) |
| `internal/cli/status.go` | status command | new `DefaultHealthConfig` call + seam body (Task 3) |
| `internal/deploy/deploy_unit_test.go` | deploy tests | update ResolvedURL tests; add TLS/HealthURL tests (Task 3) |
| `internal/deploy/health_test.go` | health tests | add `DefaultHealthConfig` test (Task 3) |
| `internal/stack/laravel/prod.go` | prod render | `webserverPorts(..., tls)`, `renderProdEnvExample(cfg, env, services)` (Task 4) |
| `internal/stack/laravel/prod_test.go` | prod render tests | update + new port/APP_URL tests (Task 4) |
| `README.md`, `CHANGELOG.md` | docs | document `tls`, health URL change (Task 5) |

---

### Task 1: Config — `tls` field on `DeployConfig`

**Files:**
- Modify: `internal/config/config.go:77-85`
- Modify: `internal/config/testdata/full-ports.toml:15-19`
- Test: `internal/config/parse_test.go:10-25, 54-67`

**Interfaces:**
- Produces: `config.DeployConfig` gains field `TLS bool` with tag `toml:"tls"`. Absent in TOML ⇒ zero value `false`.

- [ ] **Step 1: Add the TOML fixture value**

In `internal/config/testdata/full-ports.toml`, change the `[deploy.production]` table (lines 15-19) to:

```toml
[deploy.production]
host   = "prod.example.com"
user   = "deploy"
path   = "/srv/myapp"
branch = "main"
tls    = true
```

- [ ] **Step 2: Write the failing tests**

In `internal/config/parse_test.go`, extend `TestLoadFullWithPorts` (after the existing `prod.Ports["laravel"]` check at line 24):

```go
	if !prod.TLS {
		t.Error("Deploy[production].TLS = false, want true (tls = true in full-ports.toml)")
	}
```

In `TestLoadFull`, extend the `staging` block (after line 67):

```go
	if staging.TLS {
		t.Errorf("staging.TLS = true, want false (tls absent in full.toml → default false)")
	}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL — `Deploy[production].TLS = false, want true` (field doesn't exist yet).

- [ ] **Step 4: Add the `TLS` field**

In `internal/config/config.go`, replace lines 77-85:

```go
// DeployConfig is one [deploy.<env>] table: SSH target, remote path,
// branch to build from, and per-env host-port overrides.
type DeployConfig struct {
	Host   string         `toml:"host"`
	User   string         `toml:"user"`
	Path   string         `toml:"path"`
	Branch string         `toml:"branch"`
	Ports  map[string]int `toml:"ports"`
	TLS    bool           `toml:"tls"`
}
```

with:

```go
// DeployConfig is one [deploy.<env>] table: SSH target, remote path,
// branch to build from, per-env host-port overrides, and the TLS
// toggle. TLS is false by default (plain HTTP; SSL certificate
// provisioning is not shipped yet).
type DeployConfig struct {
	Host   string         `toml:"host"`
	User   string         `toml:"user"`
	Path   string         `toml:"path"`
	Branch string         `toml:"branch"`
	Ports  map[string]int `toml:"ports"`
	TLS    bool           `toml:"tls"`
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/testdata/full-ports.toml internal/config/parse_test.go
git commit -m "feat(config): add tls flag to deploy env config"
```

---

### Task 2: Laravel helpers — `WebScheme` and `WebPort`

**Files:**
- Modify: `internal/stack/laravel/ports.go` (append after `ResolvePort`, line 85)
- Test: `internal/stack/laravel/ports_test.go` (append)

**Interfaces:**
- Consumes: `config.DeployConfig{TLS bool, Ports map[string]int}` from Task 1.
- Produces: `func WebScheme(cfg config.Config, env string) string` and `func WebPort(cfg config.Config, env string) int` — used by Tasks 3 and 4.

- [ ] **Step 1: Write the failing tests**

Append to `internal/stack/laravel/ports_test.go`:

```go
func TestWebScheme(t *testing.T) {
	base := func(tls bool) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {TLS: tls}},
		}
	}
	if got := WebScheme(base(false), "production"); got != "http" {
		t.Errorf("WebScheme(tls=false) = %q, want http", got)
	}
	if got := WebScheme(base(true), "production"); got != "https" {
		t.Errorf("WebScheme(tls=true) = %q, want https", got)
	}
	if got := WebScheme(config.Config{}, "missing"); got != "http" {
		t.Errorf("WebScheme(missing env) = %q, want http (zero-value default)", got)
	}
}

func TestWebPort(t *testing.T) {
	cfgWith := func(tls bool, ports map[string]int) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {TLS: tls, Ports: ports}},
		}
	}
	cases := []struct {
		name string
		cfg  config.Config
		want int
	}{
		{"no tls, no override", cfgWith(false, nil), 80},
		{"no tls, override 8383", cfgWith(false, map[string]int{"laravel": 8383}), 8383},
		{"tls, no override", cfgWith(true, nil), 443},
		{"tls, override 8443", cfgWith(true, map[string]int{"laravel": 8443}), 8443},
		{"laravel=0 falls back to default", cfgWith(false, map[string]int{"laravel": 0}), 80},
	}
	for _, c := range cases {
		if got := WebPort(c.cfg, "production"); got != c.want {
			t.Errorf("%s: WebPort = %d, want %d", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/stack/laravel/ -run 'TestWebScheme|TestWebPort'`
Expected: FAIL — undefined: WebScheme / WebPort.

- [ ] **Step 3: Implement the helpers**

Append to `internal/stack/laravel/ports.go`:

```go
// WebScheme returns the URL scheme for the env's primary web
// endpoint: "https" when [deploy.<env>].tls is set, else "http"
// (the default; TLS certificate provisioning is not shipped yet).
func WebScheme(cfg config.Config, env string) string {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	if deployCfg.TLS {
		return "https"
	}
	return "http"
}

// WebPort returns the host port for the env's primary web endpoint:
// the [deploy.<env>.ports.laravel] override when set (0 = don't
// expose falls back to the default), else 443 when TLS is enabled or
// 80 for the plain-HTTP default.
func WebPort(cfg config.Config, env string) int {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	if v, set := deployCfg.Ports["laravel"]; set && v != 0 {
		return v
	}
	if deployCfg.TLS {
		return 443
	}
	return 80
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/stack/laravel/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stack/laravel/ports.go internal/stack/laravel/ports_test.go
git commit -m "feat(laravel): add web scheme/port helpers honoring tls flag"
```

---

### Task 3: Deploy URLs — `ResolvedURL` scheme, new `HealthURL`, `DefaultHealthConfig(cfg, env)`

**Files:**
- Modify: `internal/deploy/health.go:20-31`
- Modify: `internal/deploy/deploy.go:204-217`
- Modify: `internal/cli/deploy.go:40`
- Modify: `internal/cli/status.go:25-29, 79`
- Test: `internal/deploy/deploy_unit_test.go:48-74`
- Test: `internal/deploy/health_test.go`

**Interfaces:**
- Consumes: `WebScheme`, `WebPort` from Task 2.
- Produces: `func HealthURL(cfg config.Config, env string) string` — `scheme://<host>:<port>/up`; `DefaultHealthConfig(cfg config.Config, env string) HealthConfig`; `ResolvedURL` keeps its signature but returns `scheme://<domain>:<port>`.
- Replaces the old `DefaultHealthConfig(domain string)` — its two call sites (`cli/deploy.go:40`, `cli/status.go:79`) MUST change in this task or the build breaks.

- [ ] **Step 1: Write the failing tests**

In `internal/deploy/deploy_unit_test.go`, replace `TestDeployFinalStateURL` and `TestDeployFinalStateURLDefault` (lines 48-74) with:

```go
func TestDeployFinalStateURL(t *testing.T) {
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}},
		},
	}, "production")
	want := "http://myapp.example.com:8383"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q", url, want)
	}
}

func TestDeployFinalStateURLDefault(t *testing.T) {
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b"},
		},
	}, "production")
	want := "http://myapp.example.com:80"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q (no override → default 80 over plain HTTP)", url, want)
	}
}

func TestDeployFinalStateURLTLS(t *testing.T) {
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"tls on, no override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", TLS: true}, "https://myapp.example.com:443"},
		{"tls on, override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", TLS: true, Ports: map[string]int{"laravel": 8443}}, "https://myapp.example.com:8443"},
		{"tls on, laravel=0 falls back to 443", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", TLS: true, Ports: map[string]int{"laravel": 0}}, "https://myapp.example.com:443"},
	}
	for _, c := range cases {
		url := ResolvedURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: ResolvedURL = %q, want %q", c.name, url, c.want)
		}
	}
}

func TestHealthURL(t *testing.T) {
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"plain http default", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b"}, "http://192.168.1.10:80/up"},
		{"laravel override", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.1.10:8383/up"},
		{"tls on", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", TLS: true}, "https://192.168.1.10:443/up"},
	}
	for _, c := range cases {
		url := HealthURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: HealthURL = %q, want %q", c.name, url, c.want)
		}
	}
}
```

Append to `internal/deploy/health_test.go`:

```go
func TestDefaultHealthConfig(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "192.168.1.10", User: "u", Path: "p", Branch: "b"}},
	}
	h := DefaultHealthConfig(cfg, "production")
	if h.URL != "http://192.168.1.10:80/up" {
		t.Errorf("URL = %q, want http://192.168.1.10:80/up (host IP, plain HTTP)", h.URL)
	}
	if h.Timeout != 60*time.Second || h.Interval != 2*time.Second || h.MaxAttempts != 30 {
		t.Errorf("DefaultHealthConfig = %+v, want 60s timeout / 2s interval / 30 attempts", h)
	}
}
```

`health_test.go` needs the `config` import. Check its current import block; if `github.com/Bonnary/pier/internal/config` is missing, add it:

```go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)
```

(Adjust to the file's actual current imports — keep everything already there, add only the config line.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestDeployFinalStateURL|TestHealthURL|TestDefaultHealthConfig'`
Expected: FAIL — build errors (undefined HealthURL, wrong DefaultHealthConfig signature) and assertion failures (`want http://myapp.example.com:80`).

- [ ] **Step 3: Update `health.go`**

In `internal/deploy/health.go`, replace the `DefaultHealthConfig` function and its doc comment (lines 20-31):

```go
// DefaultHealthConfig returns a sensible default HealthConfig for a
// deploy env: GET to the deploy host's web endpoint (scheme and port
// resolved from [deploy.<env>].tls and the "laravel" port), 60 s
// total timeout, 2 s base interval, 30 attempts (interval doubles
// each attempt up to a 10 s cap).
func DefaultHealthConfig(cfg config.Config, env string) HealthConfig {
	return HealthConfig{
		URL:         HealthURL(cfg, env),
		Timeout:     60 * time.Second,
		Interval:    2 * time.Second,
		MaxAttempts: 30,
	}
}
```

Add the config import to `health.go`:

```go
import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Bonnary/pier/internal/config"
)
```

- [ ] **Step 4: Update `deploy.go` — `ResolvedURL` + new `HealthURL`**

In `internal/deploy/deploy.go`, replace `ResolvedURL` (lines 204-217):

```go
// ResolvedURL returns the public URL for the deployed env: scheme
// and port resolved from [deploy.<env>].tls and the "laravel" port
// (default 443 when TLS is enabled, 80 for plain HTTP, or the
// per-env override from [deploy.<env>.ports.laravel]).
func ResolvedURL(cfg config.Config, env string) string {
	return fmt.Sprintf("%s://%s:%d", laravelpkg.WebScheme(cfg, env), cfg.Project.Domain, laravelpkg.WebPort(cfg, env))
}

// HealthURL returns the URL the health probe GETs for env: the
// deploy host IP from [deploy.<env>].host with the resolved scheme
// and "laravel" port, plus "/up". Probing the host IP instead of the
// public domain means health checks pass before DNS/hosts entries
// point the domain at the server.
func HealthURL(cfg config.Config, env string) string {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	return fmt.Sprintf("%s://%s:%d/up", laravelpkg.WebScheme(cfg, env), deployCfg.Host, laravelpkg.WebPort(cfg, env))
}
```

- [ ] **Step 5: Update the CLI call sites**

In `internal/cli/deploy.go`, line 40:

```go
		Health:    deploy.DefaultHealthConfig(*cfg, env),
```

In `internal/cli/status.go`:

- Replace the seam body (lines 27-29):

```go
var statusHealthURL = func(cfg *config.Config, env string) string {
	return deploy.HealthURL(*cfg, env)
}
```

- Replace line 79:

```go
	health := deploy.DefaultHealthConfig(*cfg, env)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS across all packages. (`internal/cli/status_test.go` overrides the `statusHealthURL` seam, so its tests are unaffected.)

- [ ] **Step 7: Commit**

```bash
git add internal/deploy/health.go internal/deploy/deploy.go internal/deploy/deploy_unit_test.go internal/deploy/health_test.go internal/cli/deploy.go internal/cli/status.go
git commit -m "feat(deploy): probe host IP over http by default; scheme-aware URLs"
```

---

### Task 4: Prod render — webserver ports and `APP_URL` follow the `tls` flag

**Files:**
- Modify: `internal/stack/laravel/prod.go:30, 69, 113-134, 165-197`
- Test: `internal/stack/laravel/prod_test.go:145-245`

**Interfaces:**
- Consumes: `WebScheme` from Task 2; `DeployConfig.TLS` from Task 1.
- Produces: `webserverPorts(bind string, override map[string]int, tls bool) []string` (internal — only call site is `renderProdCompose`); `renderProdEnvExample(cfg config.Config, env string, services []string) []byte` (internal — only call site is `GenerateProdFiles`).

- [ ] **Step 1: Update the failing tests**

In `internal/stack/laravel/prod_test.go`, replace the assertion block of `TestGenerateProdFilesWebserverDefaultPorts` (lines 173-186) with:

```go
	found80, found443 := false, false
	for _, p := range web.Ports {
		if p == "80:80" {
			found80 = true
		}
		if p == "443:443" {
			found443 = true
		}
	}
	if !found80 {
		t.Errorf("webserver ports = %v, want it to include 80:80 (plain-HTTP default: laravel → container 80)", web.Ports)
	}
	if found443 {
		t.Errorf("webserver ports = %v, must not include 443:443 when tls is off (nginx serves HTTP on 80 only)", web.Ports)
	}
```

In `TestGenerateProdFilesPortPartialOverride`, replace lines 220-234 (the `found8383, found80 := false, false` block through the second `t.Errorf`):

```go
	found8383, found80 := false, false
	for _, p := range web.Ports {
		if p == "8383:80" {
			found8383 = true
		}
		if p == "80:80" {
			found80 = true
		}
	}
	if !found8383 {
		t.Errorf("webserver ports = %v, want it to include 8383:80 (laravel override → container 80 when tls is off)", web.Ports)
	}
	if found80 {
		t.Errorf("webserver ports = %v, must not publish the webserver_http default 80:80 when tls is off", web.Ports)
	}
```

Append these three new tests at the end of `internal/stack/laravel/prod_test.go`:

```go
func TestGenerateProdFilesWebserverTLSPorts(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", TLS: true},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	web, ok := doc.Services["webserver"]
	if !ok {
		t.Fatal("webserver missing")
	}
	wantPorts := map[string]bool{
		"443:443": false,
		"80:80":   false,
	}
	for _, p := range web.Ports {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for p, found := range wantPorts {
		if !found {
			t.Errorf("webserver ports missing %q; got %v (tls on: laravel=443, webserver_http=80)", p, web.Ports)
		}
	}
}

func TestGenerateProdFilesWebserverHTTPOverrideWhenNoTLS(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"webserver_http": 8080}},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	web, ok := doc.Services["webserver"]
	if !ok {
		t.Fatal("webserver missing")
	}
	wantPorts := map[string]bool{
		"80:80":   false,
		"8080:80": false,
	}
	for _, p := range web.Ports {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for p, found := range wantPorts {
		if !found {
			t.Errorf("webserver ports missing %q; got %v (tls off: laravel default 80:80 + explicit webserver_http 8080:80)", p, web.Ports)
		}
	}
}

func TestGenerateProdEnvExampleAPPURL(t *testing.T) {
	s := New()
	httpFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b"}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (http): %v", err)
	}
	httpEnv := findFile(httpFiles, ".env.production.example")
	if httpEnv == nil {
		t.Fatal(".env.production.example missing (http)")
	}
	if !contains(string(httpEnv.Contents), "APP_URL=http://myapp.example.com") {
		t.Errorf("env example missing plain-HTTP APP_URL:\n%s", httpEnv.Contents)
	}

	httpsFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b", TLS: true}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (https): %v", err)
	}
	httpsEnv := findFile(httpsFiles, ".env.production.example")
	if httpsEnv == nil {
		t.Fatal(".env.production.example missing (https)")
	}
	if !contains(string(httpsEnv.Contents), "APP_URL=https://myapp.example.com") {
		t.Errorf("env example missing HTTPS APP_URL:\n%s", httpsEnv.Contents)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/stack/laravel/`
Expected: FAIL — old renderer emits `443:443`, `8383:443`, and `APP_URL=https://...`.

- [ ] **Step 3: Update `renderProdCompose` call site**

In `internal/stack/laravel/prod.go`, line 69:

```go
				Ports:     webserverPorts("", deployCfg.Ports, deployCfg.TLS),
```

- [ ] **Step 4: Update `webserverPorts`**

Replace the function and its doc comment (lines 113-134):

```go
// webserverPorts assembles the `ports:` slice for the webserver service.
// The "laravel" key is the primary visible port: container 443 when TLS
// is enabled, container 80 for the plain-HTTP default. "webserver_http"
// (the HTTP→HTTPS redirect listener) is only published when TLS is
// enabled, unless the user explicitly set it while TLS is off. Either
// key may be 0 in the override to opt out. bind is the host-side bind
// prefix ("" = no prefix, host firewall restricts access; the deploy
// path always passes "").
func webserverPorts(bind string, override map[string]int, tls bool) []string {
	laravelDefault, laravelContainer := 80, 80
	if tls {
		laravelDefault, laravelContainer = 443, 443
	}
	defaults := map[string]int{"laravel": laravelDefault, "webserver_http": 80}
	var out []string
	if host, ok := ResolvePort("laravel", override, defaults); ok {
		out = append(out, PortBinding(bind, host, laravelContainer))
	}
	if tls {
		if host, ok := ResolvePort("webserver_http", override, defaults); ok {
			out = append(out, PortBinding(bind, host, 80))
		}
	} else if v, set := override["webserver_http"]; set && v != 0 {
		out = append(out, PortBinding(bind, v, 80))
	}
	return out
}
```

- [ ] **Step 5: Update `GenerateProdFiles` and `renderProdEnvExample`**

In `internal/stack/laravel/prod.go`, line 30:

```go
	envExample := renderProdEnvExample(cfg, env, prodServices)
```

Replace `renderProdEnvExample` signature and APP_URL line (lines 165, 173):

```go
func renderProdEnvExample(cfg config.Config, env string, services []string) []byte {
```

and:

```go
	fmt.Fprintf(&b, "APP_URL=%s://%s\n\n", WebScheme(cfg, env), cfg.Project.Domain)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `gofmt -w internal/stack/laravel/prod.go internal/stack/laravel/prod_test.go && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 7: Commit**

```bash
git add internal/stack/laravel/prod.go internal/stack/laravel/prod_test.go
git commit -m "feat(laravel): render http-only prod compose and APP_URL by default"
```

---

### Task 5: Docs — README and CHANGELOG

**Files:**
- Modify: `README.md:266-274`
- Modify: `CHANGELOG.md` (after line 1, `# Changelog`)

- [ ] **Step 1: Update the README pier.toml example**

In `README.md`, replace the `[deploy.production]` example block (lines 266-274):

```toml
[deploy.production]
host   = "prod.example.com"
user   = "deploy"
path   = "/srv/myapp"
branch = "main"
tls    = false   # false (default): plain HTTP. true: HTTPS URLs + 443 — requires the upcoming cert feature

[deploy.production.ports]
laravel = 443   # only the keys the user writes are applied
```

- [ ] **Step 2: Add a short `[deploy.<env>]` paragraph after the example**

In `README.md`, immediately after the closing fence of the example block (line 274) and before the `### `[dev.services.<name>]` heading, add:

```markdown
`[deploy.<env>]` fields: `host`, `user`, `path`, `branch`, optional
`tls`, and optional `ports` overrides. `tls = false` (the default)
serves plain HTTP end-to-end: the deploy health check probes
`http://<host-ip>:<laravel-port>/up` directly on the deploy host IP,
so it passes before DNS or `/etc/hosts` entries point the domain at
the server. `tls = true` renders HTTPS URLs and the 443 mapping, but
SSL certificate provisioning is not shipped yet — keep it `false`
for now.
```

- [ ] **Step 3: Add the CHANGELOG entry**

In `CHANGELOG.md`, insert after line 1 (`# Changelog`):

```markdown
## Unreleased

### Added

- `[deploy.<env>].tls` flag (default `false`): production serves plain
  HTTP end-to-end. `tls = true` renders HTTPS URLs and the 443 port
  mapping; SSL certificate provisioning ships in a later release.

### Changed

- Deploy and `pier status <env>` health probes now target
  `http://<host-ip>:<port>/up` (the `[deploy.<env>].host` address)
  instead of the public domain, so health checks pass without DNS or
  `/etc/hosts` entries. `APP_URL` and the displayed deploy URL now
  follow the env's scheme.
```

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document tls flag and host-IP health probe"
```

---

### Task 6: Final verification

- [ ] **Step 1: Full test run + lint**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt -l` prints nothing; `go vet` clean; all tests PASS.

- [ ] **Step 2: Confirm the user's scenario**

In `/media/pcnerd/New Volume/Code/php/test_web/pier.toml`, `[deploy.production]` already has `tls = false`. With the new code:
- `pier status production` health line shows `health: OK/DOWN (http://192.168.122.126:80/up)`.
- `pier deploy production` probes `http://192.168.122.126:80/up` (nginx on container 80) and the done event shows `URL: http://test_web.example.com:80`.

If anything in the user's project needs regenerating (`pier deploy` re-renders `docker-compose.prod.yml` automatically), no manual step is required.

- [ ] **Step 3: Final commit if anything was left over**

Run: `git status --short`
Expected: clean working tree (no stray changes).

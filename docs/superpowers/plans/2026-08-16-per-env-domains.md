# Per-Env Domains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the domain config model (`[project].domain` + per-env override + inheritance via `DomainForEnv`) with a single per-env model: each `[deploy.<env>]` carries its own `domain`, and `extra_domains` is renamed to `redirect_domains`.

**Architecture:** Remove `ProjectConfig.Domain` and `Config.DomainForEnv` from the config package; every consumer reads `cfg.Deploy[env].Domain` directly (approach A from the spec). The rename is mechanical; the behavior change is that envs no longer inherit a project-wide domain.

**Tech Stack:** Go 1.25, BurntSushi/toml, cobra CLI. Spec: `docs/superpowers/specs/2026-08-16-per-env-domains-design.md`.

## Global Constraints

- Old configs keep loading: BurntSushi/toml silently ignores unknown keys, so `[project] domain` and `extra_domains` in an existing `pier.toml` are dropped, not rejected. Envs that relied on inheritance switch to plain HTTP. README/CHANGELOG document this.
- No remaining references to `extra_domains`, `ExtraDomains`, `DomainForEnv`, `Project.Domain`, or `project.domain` in `internal/`, `cmd/`, `README.md`, `CHANGELOG.md`, or `skills/pier/SKILL.md` after Task 5.
- Validation messages rename to `deploy.<env>.redirect_domains` and "must not contain the domain" (no "primary").
- Keep Go doc comments in sync with the new wording ("effective domain" → "the env's domain") wherever the removed helper is mentioned.
- Commit style follows the repo: lowercase type prefix (`feat(config):`, `refactor(deploy):`, `docs:`).
- Each task's test command scopes to its package; `go build ./...` compiles green only after Task 4.

---

### Task 1: Config model — drop `[project].domain`, rename to `redirect_domains`, delete `DomainForEnv`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/parse.go`
- Modify: `internal/config/parse_test.go`
- Modify: `internal/config/testdata/minimal.toml`
- Modify: `internal/config/testdata/full.toml`
- Modify: `internal/config/testdata/hooks.toml`
- Modify: `internal/config/testdata/full-ports.toml`

**Interfaces:**
- Produces: `ProjectConfig{Name string}` (no `Domain` field), `DeployConfig{...; Domain string; RedirectDomains []string}`, no `Config.DomainForEnv`. Consumers in Tasks 2–4 read `cfg.Deploy[env].Domain` and `dc.RedirectDomains`.

- [ ] **Step 1: Update testdata tomls (tests go red against the new schema later)**

`internal/config/testdata/minimal.toml` — remove the `domain` line from `[project]`:

```toml
[project]
name = "myapp"

[stack]
type = "laravel"
php = "8.3"
node = "22"
services = []
```

`internal/config/testdata/hooks.toml` — remove the `domain` line from `[project]` (rest unchanged).

`internal/config/testdata/full.toml` — remove the `domain` line from `[project]`; add a domain to both deploy sections:

```toml
[project]
name = "myapp"

[stack]
type = "laravel"
php = "8.3"
node = "22"
services = ["redis", "mailpit", "s3"]

[dev.ports]
laravel = 8000
vite = 5173
redis = 6379

[deploy.production]
host = "prod.example.com"
user = "deploy"
path = "/srv/myapp"
branch = "main"
domain = "myapp.example.com"

[deploy.production.ports]
laravel = 8383

[deploy.staging]
host = "staging.example.com"
user = "deploy"
path = "/srv/myapp-staging"
branch = "develop"
domain = "staging.myapp.example.com"

[deploy.staging.ports]
laravel = 8443
```

`internal/config/testdata/full-ports.toml` — remove the `domain` line from `[project]`; rename the deploy key:

```toml
[project]
name = "myapp"

[stack]
type = "laravel"
php = "8.3"
node = "22"
services = ["redis", "mailpit", "s3"]

[dev.ports]
laravel = 8080
vite = 5174

[deploy.production]
host   = "prod.example.com"
user   = "deploy"
path   = "/srv/myapp"
branch = "main"
domain = "myapp.example.com"
redirect_domains = ["www.myapp.example.com"]

[deploy.production.ports]
laravel = 8383
```

- [ ] **Step 2: Update `internal/config/parse_test.go` to the new API (red: compile errors until Step 3)**

Make these exact edits:

(a) In `TestLoadFullWithPorts`, replace the `ExtraDomains` block:

```go
	if got := prod.RedirectDomains; len(got) != 1 || got[0] != "www.myapp.example.com" {
		t.Errorf("Deploy[production].RedirectDomains = %v, want [www.myapp.example.com]", got)
	}
```

(b) In `TestLoadFull`, replace the trailing `DomainForEnv` assertion:

```go
	if got := staging.Domain; got != "staging.myapp.example.com" {
		t.Errorf("staging.Domain = %q, want staging.myapp.example.com (each env carries its own domain)", got)
	}
```

(c) Replace ALL occurrences (28 sites) of:

`Project: ProjectConfig{Name: "x", Domain: "x.example.com"},`

with:

`Project: ProjectConfig{Name: "x"},`

(In files of this package the literal always appears exactly once per test body; use `replaceAll` on the exact string above.)

(d) Delete `TestDomainForEnv` entirely (lines 524–545 of the current file).

(e) Delete `TestValidateEmptyProjectDomainAllowed` entirely.

(f) Delete `TestValidateDomainSyntax` entirely (its cases move into `TestValidateDeployDomainSyntax` below).

(g) Expand `TestValidateDeployDomainSyntax` to cover the full case list on the deploy field:

```go
func TestValidateDeployDomainSyntax(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{"deploy domain with scheme", "https://prod.example.com"},
		{"deploy domain with port", "prod.example.com:8443"},
		{"deploy domain with path", "prod.example.com/app"},
		{"deploy domain with tab", "prod.example.com\t"},
		{"deploy domain with bare IP", "192.168.122.30"},
	}
	for _, c := range cases {
		cfg := &Config{
			Project: ProjectConfig{Name: "x"},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy: map[string]DeployConfig{
				"production": {Host: "h", User: "u", Path: "p", Branch: "b", Domain: c.field},
			},
		}
		err := cfg.Validate()
		if !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("%s: Validate = %v, want ErrConfigInvalid", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), "deploy.production.domain") {
			t.Errorf("%s: err = %q, want it to mention deploy.production.domain", c.name, err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("%q", c.field)) {
			t.Errorf("%s: err = %q, want it to mention the bad domain", c.name, err)
		}
	}
}
```

(h) Rename `TestValidateExtraDomains` to `TestValidateRedirectDomains` and rewrite it:

```go
func TestValidateRedirectDomains(t *testing.T) {
	base := func(extra []string) *Config {
		return &Config{
			Project: ProjectConfig{Name: "x"},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy: map[string]DeployConfig{
				"production": {Host: "h", User: "u", Path: "p", Branch: "b", Domain: "x.example.com", RedirectDomains: extra},
			},
		}
	}
	if err := base([]string{"www.x.example.com"}).Validate(); err != nil {
		t.Errorf("Validate(valid redirect_domains) = %v, want nil", err)
	}
	if err := base([]string{"bad domain"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(redirect_domains with space) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"www.x.example.com", "www.x.example.com"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(duplicate redirect_domains) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"X.EXAMPLE.COM"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(redirect_domains case-variant of domain) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"www.x.example.com", "WWW.X.EXAMPLE.COM"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(duplicate redirect_domains case-variant) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"x.example.com"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(redirect_domains containing the domain) = %v, want ErrConfigInvalid", err)
	}
}
```

(i) Add the migration pin test at the end of the file:

```go
func TestLoadIgnoresProjectDomain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pier.toml")
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"p\"\nbranch=\"b\"\n"
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Deploy["production"].Domain; got != "" {
		t.Errorf("Deploy[production].Domain = %q, want \"\" (old [project].domain is ignored; envs serve plain HTTP)", got)
	}
}
```

Add `"os"` to the import block of `parse_test.go`:

```go
import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

- [ ] **Step 3: Run the config tests to confirm they fail**

Run: `go test ./internal/config/`
Expected: compile errors (`undefined: RedirectDomains`, `unknown field Domain in struct literal of type ProjectConfig`, `cfg.DomainForEnv undefined`).

- [ ] **Step 4: Change `internal/config/config.go`**

Replace `ProjectConfig` and its comment:

```go
// ProjectConfig is the [project] table: the project's friendly name.
// Public domains are configured per deploy env in [deploy.<env>].domain.
type ProjectConfig struct {
	Name string `toml:"name"`
}
```

Replace the `Domain` / `ExtraDomains` fields of `DeployConfig`:

```go
	// Domain is the env's public domain. A non-empty domain means
	// Caddy serves HTTPS with an automatic Let's Encrypt certificate;
	// an empty one means plain HTTP by IP.
	Domain string `toml:"domain"`
	// RedirectDomains lists additional domains Caddy serves,
	// redirecting each to this env's domain (e.g. www.example.com for
	// example.com).
	RedirectDomains []string `toml:"redirect_domains"`
```

Delete the entire `DomainForEnv` method (its doc comment included).

- [ ] **Step 5: Change `internal/config/parse.go`**

Delete the project-domain validation block from `Validate`:

```go
	if c.Project.Domain != "" && !validHostname(c.Project.Domain) {
		return fmt.Errorf("%w: project.domain %q is not a valid hostname (no scheme, port, path, whitespace, @, or bare IP)", ErrConfigInvalid, c.Project.Domain)
	}
```

In `validateDeployEnv`, replace the extra-domains loop and primary check:

```go
	seen := map[string]bool{}
	for _, d := range dc.RedirectDomains {
		if !validHostname(d) {
			return fmt.Errorf("%w: deploy.%s.redirect_domains entry %q is not a valid hostname (no scheme, port, path, whitespace, @, or bare IP)", ErrConfigInvalid, env, d)
		}
		lowered := strings.ToLower(d)
		if seen[lowered] {
			return fmt.Errorf("%w: deploy.%s.redirect_domains has duplicate %q", ErrConfigInvalid, env, d)
		}
		seen[lowered] = true
	}
	if seen[strings.ToLower(dc.Domain)] {
		return fmt.Errorf("%w: deploy.%s.redirect_domains must not contain the domain %q", ErrConfigInvalid, env, dc.Domain)
	}
```

Update the `validateDeployEnv` doc comment ("plus the domain and extra_domains hostname syntax" → "plus the domain and redirect_domains hostname syntax") and the `validHostname` doc comment ("project and deploy domains" → "deploy domains").

- [ ] **Step 6: Run the config tests**

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/
git commit -m "feat(config): per-env domains; drop [project].domain, rename extra_domains to redirect_domains"
```

---

### Task 2: Laravel stack — read `dc.Domain` directly

**Files:**
- Modify: `internal/stack/laravel/ports.go:87-115`
- Modify: `internal/stack/laravel/prod.go:101,305-329,370-392`
- Modify: `internal/stack/laravel/ports_test.go:173-215`
- Modify: `internal/stack/laravel/prod_test.go`
- Modify: `internal/stack/laravel/dev_test.go`
- Modify: `internal/stack/laravel/dev_services_test.go`
- Modify: `internal/stack/laravel/merge_prod_test.go`

**Interfaces:**
- Consumes: `DeployConfig.Domain`, `DeployConfig.RedirectDomains` from Task 1.
- Produces: unchanged signatures — `WebScheme(cfg, env)`, `WebPort(cfg, env)`, `renderCaddyfile(cfg, env)`, `renderProdCompose`, `renderProdEnv`.

- [ ] **Step 1: Update `ports_test.go` (red)**

Replace `TestWebScheme` and `TestWebPort` helpers so the domain lives on the deploy env:

```go
func TestWebScheme(t *testing.T) {
	base := func(domain string) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {Domain: domain}},
		}
	}
	if got := WebScheme(base(""), "production"); got != "http" {
		t.Errorf("WebScheme(domain=\"\") = %q, want http (no domain = plain HTTP)", got)
	}
	if got := WebScheme(base("myapp.example.com"), "production"); got != "https" {
		t.Errorf("WebScheme(domain set) = %q, want https (domain presence enables Caddy HTTPS)", got)
	}
	if got := WebScheme(config.Config{}, "missing"); got != "http" {
		t.Errorf("WebScheme(missing env) = %q, want http (zero-value default)", got)
	}
}

func TestWebPort(t *testing.T) {
	cfgWith := func(domain string, ports map[string]int) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {Domain: domain, Ports: ports}},
		}
	}
	cases := []struct {
		name string
		cfg  config.Config
		want int
	}{
		{"no domain, no override", cfgWith("", nil), 80},
		{"no domain, override 8383", cfgWith("", map[string]int{"laravel": 8383}), 8383},
		{"domain, no override", cfgWith("myapp.example.com", nil), 443},
		{"domain, override 8443", cfgWith("myapp.example.com", map[string]int{"laravel": 8443}), 8443},
	}
	for _, c := range cases {
		if got := WebPort(c.cfg, "production"); got != c.want {
			t.Errorf("%s: WebPort = %d, want %d", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Update `prod_test.go` (red)**

(a) Replace ALL 24 occurrences of:

`Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},`

with:

`Project: config.ProjectConfig{Name: "myapp"},`

(b) Tests whose assertions need a domain now get it on the env. Rewrite these four:

`TestGenerateProdFilesCaddyfileProxiesToAppHTTPServer` (line ~173):

```go
func TestGenerateProdFilesCaddyfileProxiesToAppHTTPServer(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Domain: "myapp.example.com"}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	conf := findFile(files, "docker/caddy/Caddyfile")
	if conf == nil {
		t.Fatal("docker/caddy/Caddyfile missing")
	}
	body := string(conf.Contents)
	if strings.Contains(body, "fastcgi") {
		t.Errorf("Caddyfile must not use fastcgi: the app container runs `artisan serve` (PHP built-in server on port 80), not php-fpm — there is no php-fpm binary in the runtime image:\n%s", body)
	}
	if !strings.Contains(body, "reverse_proxy app:80") {
		t.Errorf("Caddyfile must proxy requests verbatim to the app's HTTP listener (reverse_proxy app:80), matching the runtime's `artisan serve --host=0.0.0.0 --port=80`:\n%s", body)
	}
	if !strings.Contains(body, "myapp.example.com") {
		t.Errorf("Caddyfile missing the site block for the env's domain:\n%s", body)
	}
}
```

`TestRenderCaddyfileExtraDomains` → rename to `TestRenderCaddyfileRedirectDomains`:

```go
func TestRenderCaddyfileRedirectDomains(t *testing.T) {
	body := renderCaddyfile(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Domain: "myapp.example.com", RedirectDomains: []string{"www.myapp.example.com"}},
		},
	}, "production")
	if !strings.Contains(string(body), "www.myapp.example.com {\n    redir https://myapp.example.com{uri}\n}") {
		t.Errorf("redirect_domains entry must redirect to the env's domain:\n%s", body)
	}
}
```

`TestRenderCaddyfilePerEnvDomainOverride` → rename to `TestRenderCaddyfilePerEnvDomain`:

```go
func TestRenderCaddyfilePerEnvDomain(t *testing.T) {
	body := renderCaddyfile(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"staging": {Domain: "staging.myapp.example.com"},
		},
	}, "staging")
	if !strings.Contains(string(body), "staging.myapp.example.com {") {
		t.Errorf("each env must serve its own domain:\n%s", body)
	}
}
```

`TestGenerateProdFilesPortPartialOverride` (line ~462): add `Domain: "myapp.example.com",` to the production `DeployConfig` literal (after `Ports: map[string]int{"laravel": 8383},`).

`TestGenerateProdFilesWebserverTLSPorts` (line ~520): add `Domain: "myapp.example.com",` to the production `DeployConfig` literal (`{Host: "h", User: "u", Path: "p", Branch: "b"}`).

`TestGenerateProdFilesEnvExampleAPPURL` (line ~636): in the `httpsFiles` and `overrideFiles` cases, add `Domain: "myapp.example.com",` to their `Deploy: map[string]config.DeployConfig{"production": {Host: "h", ...}}` literals (the `httpFiles` case stays domain-less).

(c) In `internal/stack/laravel/dev_test.go`, `dev_services_test.go`, and `merge_prod_test.go`, replace ALL occurrences of:

`Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},`

with:

`Project: config.ProjectConfig{Name: "myapp"},`

(These tests never assert on the domain.)

- [ ] **Step 3: Run laravel tests to confirm they fail**

Run: `go test ./internal/stack/laravel/`
Expected: compile errors (`cfg.DomainForEnv undefined`, `unknown field Domain in struct literal of type config.ProjectConfig`, `undefined: ExtraDomains`).

- [ ] **Step 4: Change `ports.go`**

Replace `WebScheme`:

```go
// WebScheme returns the URL scheme for the env's primary web
// endpoint: "https" when the env has a domain (Caddy provisions a
// Let's Encrypt certificate automatically, proving ownership via the
// ACME HTTP-01 challenge on ports 80/443), else "http" (no domain,
// plain HTTP by IP).
func WebScheme(cfg config.Config, env string) string {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	if deployCfg.Domain != "" {
		return "https"
	}
	return "http"
}
```

In `WebPort`, replace the domain check (`deployCfg` is already loaded there):

```go
	if deployCfg.Domain != "" {
		return 443
	}
```

- [ ] **Step 5: Change `prod.go`**

In `renderProdCompose` (line ~101), replace:

```go
				Ports:   webserverPorts("", deployCfg.Ports, cfg.DomainForEnv(env) != ""),
```

with:

```go
				Ports:   webserverPorts("", deployCfg.Ports, deployCfg.Domain != ""),
```

In `renderProdEnv` (lines ~305–329), load the deploy config once and read its domain:

```go
func renderProdEnv(cfg config.Config, env string, services []string) []byte {
	var b bytes.Buffer
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	fmt.Fprintf(&b, "# %s production environment\n", cfg.Project.Name)
	fmt.Fprintf(&b, "# Fill in real values before deploying.\n\n")
	fmt.Fprintln(&b, "APP_NAME="+cfg.Project.Name)
	fmt.Fprintln(&b, "APP_ENV=production")
	fmt.Fprintln(&b, "APP_KEY=")
	fmt.Fprintln(&b, "APP_DEBUG=false")
	if deployCfg.Domain != "" {
		if WebPort(cfg, env) == 443 {
			fmt.Fprintf(&b, "APP_URL=%s://%s\n\n", WebScheme(cfg, env), deployCfg.Domain)
		} else {
			fmt.Fprintf(&b, "APP_URL=%s://%s:%d\n\n", WebScheme(cfg, env), deployCfg.Domain, WebPort(cfg, env))
		}
	} else {
		host := "localhost"
		if deployCfg.Host != "" {
			host = deployCfg.Host
		}
		fmt.Fprintf(&b, "APP_URL=http://%s:%d\n\n", host, WebPort(cfg, env))
	}
```

Replace `renderCaddyfile`:

```go
// renderCaddyfile renders the production Caddyfile for env. With a
// domain, Caddy serves HTTPS with an automatic Let's Encrypt
// certificate — ownership is proven by the ACME HTTP-01 challenge on
// ports 80/443, so the domain's A record must point at the deploy
// host — and every redirect_domains entry redirects to the env's
// domain. Without a domain it serves plain HTTP on container port 80.
func renderCaddyfile(cfg config.Config, env string) []byte {
	dc, ok := cfg.Deploy[env]
	if !ok {
		dc = config.DeployConfig{}
	}
	if dc.Domain == "" {
		return []byte(":80 {\n    encode gzip\n    reverse_proxy app:80\n}\n")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s {\n    encode gzip\n    reverse_proxy app:80\n}\n", dc.Domain)
	for _, extra := range dc.RedirectDomains {
		fmt.Fprintf(&b, "\n%s {\n    redir https://%s{uri}\n}\n", extra, dc.Domain)
	}
	return b.Bytes()
}
```

- [ ] **Step 6: Run laravel tests**

Run: `go test ./internal/stack/laravel/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/stack/laravel/
git commit -m "refactor(laravel): read the env's domain directly; render redirect_domains"
```

---

### Task 3: Deploy package — read `dc.Domain` directly

**Files:**
- Modify: `internal/deploy/dns.go:9-25`
- Modify: `internal/deploy/deploy.go:425-472`
- Modify: `internal/deploy/health.go:24` (comment only)
- Modify: `internal/deploy/dns_test.go`
- Modify: `internal/deploy/health_test.go:55-68`
- Modify: `internal/deploy/deploy_unit_test.go`
- Modify: `internal/deploy/hooks_test.go`
- Modify: `internal/deploy/render_test.go`

**Interfaces:**
- Consumes: `DeployConfig.Domain` from Task 1; `laravel.WebScheme` / `laravel.WebPort` (unchanged from Task 2).
- Produces: unchanged signatures — `checkDomainDNS(cfg, env)`, `ResolvedURL(cfg, env)`, `HealthURL(cfg, env)`.

- [ ] **Step 1: Update `dns_test.go` (red)**

In `TestCheckDomainDNSFailsWhenDomainDoesNotResolve`, `TestCheckDomainDNSFailsOnMismatch`, `TestCheckDomainDNSPassesOnMatch`, and `TestCheckDomainDNSPassesWhenHostDoesNotResolve`, move the domain from the project into the production deploy entry:

Replace:

```go
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
```

with:

```go
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com"}},
```

(In `TestCheckDomainDNSPassesWhenHostDoesNotResolve` the deploy literal is `{Host: "hidden.example.com", ...}` — add `Domain: "myapp.example.com",` to it the same way.)

`TestCheckDomainDNSSkipsWithoutDomain` is already correct (no domain anywhere) — leave it.

- [ ] **Step 2: Update `health_test.go` (red)**

```go
func TestDefaultHealthConfig(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Domain: "x.example.com"}},
	}
	h := DefaultHealthConfig(cfg, "production")
	if h.URL != "https://x.example.com:443/up" {
		t.Errorf("URL = %q, want https://x.example.com:443/up (domain set: probe the domain over https)", h.URL)
	}
	if h.Timeout != 60*time.Second || h.Interval != 2*time.Second || h.MaxAttempts != 30 {
		t.Errorf("DefaultHealthConfig = %+v, want 60s timeout / 2s interval / 30 attempts", h)
	}
}
```

- [ ] **Step 3: Update `deploy_unit_test.go` (red)**

(a) In `TestPipelineDryRun` and all later fixtures where the domain is inert (lines ~28, 191, 255, 308, 351, 450, 514), replace:

`Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},`

with:

`Project: config.ProjectConfig{Name: "x"},`

(b) `TestDeployFinalStateURL` and `TestDeployFinalStateURLDefault`: add `Domain: "myapp.example.com",` to the production `DeployConfig` literal and drop `Domain` from `ProjectConfig`:

```go
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com", Ports: map[string]int{"laravel": 8383}},
		},
	}, "production")
```

(`TestDeployFinalStateURLDefault`'s entry is the same without the `Ports` map.)

(c) `TestDeployFinalStateURLDomain`: put the domain into each case's `DeployConfig`:

```go
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"domain, no override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com"}, "https://myapp.example.com:443"},
		{"domain, override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com", Ports: map[string]int{"laravel": 8443}}, "https://myapp.example.com:8443"},
		{"domain, laravel=0 falls back to 443", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com", Ports: map[string]int{"laravel": 0}}, "https://myapp.example.com:443"},
	}
	for _, c := range cases {
		url := ResolvedURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: ResolvedURL = %q, want %q", c.name, url, c.want)
		}
	}
```

(d) `TestHealthURL`: remove the `domain string` struct field and fold the domain into each case's `DeployConfig`:

```go
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"no domain, plain http default", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b"}, "http://192.168.1.10:80/up"},
		{"no domain, laravel override", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.1.10:8383/up"},
		{"domain, probe the domain over https", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com"}, "https://myapp.example.com:443/up"},
		{"domain, laravel override, probe carries the port", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com", Ports: map[string]int{"laravel": 8383}}, "https://myapp.example.com:8383/up"},
	}
	for _, c := range cases {
		url := HealthURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: HealthURL = %q, want %q", c.name, url, c.want)
		}
	}
```

(e) `TestResolvedURLFallsBackToHostIPWhenDomainDoesNotResolve`: same restructure — remove the `domain string` field and fold the domain into the cases:

```go
	cases := []struct {
		name string
		dc   config.DeployConfig
		want string
	}{
		{"no domain, plain http default", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b"}, "http://192.168.122.30:80"},
		{"no domain, laravel override", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.122.30:8383"},
		{"domain, falls back to host IP over https", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b", Domain: "myapp.example.com"}, "https://192.168.122.30:443"},
	}
	for _, c := range cases {
		url := ResolvedURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: ResolvedURL = %q, want %q", c.name, url, c.want)
		}
	}
```

(f) `TestResolvedURLKeepsDomainWithoutDeployHost`: the domain now lives in the env section (a domain without an env section no longer exists):

```go
func TestResolvedURLKeepsDomainWithoutDeployHost(t *testing.T) {
	pinLookup(t, false)
	url := ResolvedURL(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Domain: "myapp.example.com"}},
	}, "production")
	want := "https://myapp.example.com:443"
	if url != want {
		t.Errorf("ResolvedURL = %q, want %q (no deploy host to fall back to)", url, want)
	}
}
```

(g) `TestResolvedURLHostnamePassesThrough`: add `Domain: "bare.example.com",` to the production `DeployConfig` literal and drop `Domain` from `ProjectConfig`.

(h) In `hooks_test.go` (6 sites) and `render_test.go` (3 sites), replace ALL occurrences of:

`Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},`

with:

`Project: config.ProjectConfig{Name: "x"},`

- [ ] **Step 4: Run deploy tests to confirm they fail**

Run: `go test ./internal/deploy/`
Expected: compile errors (`cfg.DomainForEnv undefined`, `unknown field Domain in struct literal of type config.ProjectConfig`).

- [ ] **Step 5: Change `dns.go`**

Update the doc comment ("the env's effective domain" → "the env's domain") and the start of the function:

```go
func checkDomainDNS(cfg config.Config, env string) error {
	dc := cfg.Deploy[env]
	if dc.Domain == "" {
		return nil
	}
	domain := dc.Domain
	domainIPs, err := lookupHost(domain)
```

(Remove the now-duplicate `dc := cfg.Deploy[env]` line that followed the old domain lookup; the rest of the function keeps using `dc.Host`.)

- [ ] **Step 6: Change `deploy.go`**

Replace `ResolvedURL` (and update its doc comment: "the env's effective domain" → "the env's domain"):

```go
// ResolvedURL returns the public URL for the deployed env: scheme
// and port resolved from the env's domain and the "laravel" port
// (default 443 with a domain, 80 for the plain-HTTP default, or the
// per-env override from [deploy.<env>.ports.laravel]). The host is
// the env's domain when it resolves (DNS or /etc/hosts); otherwise
// it falls back to the deploy host IP, so the printed URL is usable
// before DNS entries point the domain at the server.
func ResolvedURL(cfg config.Config, env string) string {
	dc, _ := cfg.Deploy[env]
	host := dc.Domain
	if host == "" || !hostResolvable(host) {
		if dc.Host != "" {
			host = dc.Host
		}
	}
	return fmt.Sprintf("%s://%s:%d", laravelpkg.WebScheme(cfg, env), host, laravelpkg.WebPort(cfg, env))
}
```

Replace `HealthURL` (and update its doc comment the same way):

```go
// HealthURL returns the URL the health probe GETs for env. With a
// domain the probe targets https://<domain>:<port>/up (port resolved
// via laravelpkg.WebPort, default 443) with normal TLS verification —
// an end-to-end check that exercises the real Let's Encrypt
// certificate (the DNS preflight already verified the domain points
// at the deploy host). Without a domain it probes the deploy host IP
// with the resolved "laravel" port, so health checks pass before DNS
// or /etc/hosts entries exist.
func HealthURL(cfg config.Config, env string) string {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	if deployCfg.Domain != "" {
		return fmt.Sprintf("https://%s:%d/up", deployCfg.Domain, laravelpkg.WebPort(cfg, env))
	}
	return fmt.Sprintf("http://%s:%d/up", deployCfg.Host, laravelpkg.WebPort(cfg, env))
}
```

- [ ] **Step 7: Update the `health.go` doc comment**

Replace `resolved from the env's effective domain (see HealthURL))` with `resolved from the env's domain (see HealthURL)`.

- [ ] **Step 8: Run deploy tests**

Run: `go test ./internal/deploy/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/deploy/
git commit -m "refactor(deploy): read the env's domain directly for DNS preflight and URLs"
```

---

### Task 4: CLI — toml encoder, init prompt, status output, test literals

**Files:**
- Modify: `internal/cli/toml.go:12,29-34`
- Modify: `internal/cli/init.go:127,174-184`
- Modify: `internal/cli/status.go:56`
- Modify: `internal/cli/toml_test.go`
- Modify: `internal/cli/init_test.go:52,360-383`
- Modify: `internal/cli/status_test.go:36-56`
- Modify: `internal/cli/shell_test.go:17`, `internal/cli/service_test.go:17`, `internal/cli/deploy_test.go:15`, `internal/cli/exec_test.go:32,58,91,106,134`, `internal/cli/dev_test.go:38,73,120,150`, `internal/cli/bootstrap_test.go:20`

**Interfaces:**
- Consumes: `DeployConfig.Domain` / `RedirectDomains` from Task 1.
- Produces: `tomlEncode` emits `redirect_domains`; `pier init` prompt reads "Production domain"; local `pier status` prints no domain line.

- [ ] **Step 1: Update `toml_test.go` (red)**

Replace the three fixture lines with:

`Project: config.ProjectConfig{Name: "x"},`

Rewrite `TestTomlEncodeRendersDeployDomain`:

```go
func TestTomlEncodeRendersDeployDomain(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "/srv/x", Branch: "main",
				Domain: "prod.example.com", RedirectDomains: []string{"www.prod.example.com"},
			},
			"staging": {Host: "s", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`domain = "prod.example.com"`,
		`redirect_domains = ["www.prod.example.com"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tomlEncode output missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[project]\nname = \"x\"\ndomain") {
		t.Errorf("[project] section must not emit a domain key; got:\n%s", got)
	}
	for _, sec := range strings.Split(got, "\n[deploy.") {
		if strings.HasPrefix(sec, "staging]") && (strings.Contains(sec, "domain") || strings.Contains(sec, "redirect_domains")) {
			t.Errorf("staging section must not emit domain keys (empty = plain HTTP); got:\n%s", sec)
		}
	}
	path := filepath.Join(t.TempDir(), "pier.toml")
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v\nencoded:\n%s", err, b)
	}
	if got := loaded.Deploy["production"].Domain; got != "prod.example.com" {
		t.Errorf("Deploy[production].Domain = %q, want prod.example.com after round trip", got)
	}
	if got := loaded.Deploy["staging"].Domain; got != "" {
		t.Errorf("Deploy[staging].Domain = %q, want \"\" (no domain = plain HTTP) after round trip", got)
	}
}
```

- [ ] **Step 2: Update `init_test.go` and `status_test.go` (red)**

`init_test.go` line 52: replace the literal with:

```go
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte("[project]\nname=\"x\"\n"), 0644); err != nil {
```

In `TestInitPromptsForDomain` (line ~360), add the project-section pin after the existing `domain = "myapp.com"` assertion:

```go
	projectSection := strings.Split(string(got), "\n[deploy.")[0]
	if strings.Contains(projectSection, "domain") {
		t.Errorf("[project] section must not contain a domain key:\n%s", got)
	}
```

`status_test.go` `TestStatusReadsConfig`: replace the toml literal with:

```go
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[\"redis\"]\n"
```

and after the existing "x" assertion add:

```go
	if strings.Contains(buf.String(), "domain:") {
		t.Errorf("local status output must not print a project domain line (domain is per env): %q", buf.String())
	}
```

- [ ] **Step 3: Clean the remaining cli TOML literals and fixtures**

In each of these, replace `[project]\nname=\"x\"\ndomain=\"x.example.com\"\n` with `[project]\nname=\"x\"\n` (the exact literal `domain=\"x.example.com\"\n` may be deleted from the line):

- `internal/cli/shell_test.go:17` (one literal)
- `internal/cli/service_test.go:17` (one literal)
- `internal/cli/deploy_test.go:15` (one literal)
- `internal/cli/exec_test.go:32,58,91,106,134` (five literals)
- `internal/cli/dev_test.go:38,73` (two literals)
- `internal/cli/bootstrap_test.go:20` (delete the `domain = "x.example.com"` line from the multi-line literal)

In `internal/cli/dev_test.go:120,150`, replace:

`Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},`

with:

`Project: config.ProjectConfig{Name: "x"},`

- [ ] **Step 4: Run cli tests to confirm they fail**

Run: `go test ./internal/cli/`
Expected: compile errors (`unknown field Domain in struct literal of type config.ProjectConfig`, `undefined: ExtraDomains`, `cfg.Project.Domain undefined`).

- [ ] **Step 5: Change `toml.go`**

Replace the project line and the extra-domains emission:

```go
	fmt.Fprintf(&b, "[project]\nname = %q\n\n", c.Project.Name)
```

```go
	if len(dc.RedirectDomains) > 0 {
		fmt.Fprintf(&b, "redirect_domains = %s\n", tomlStringArray(dc.RedirectDomains))
	}
```

- [ ] **Step 6: Change `init.go`**

Replace the prompt (line 127):

```go
	domain := prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Production domain (e.g. myapp.com; blank = plain HTTP by IP): ", "")
```

Assign the domain to the production deploy env (after the `dc.Branch = "main"` block, before the `builder == "build_server"` block):

```go
	if host != "" && user != "" && path != "" {
		dc.Branch = "main"
	}
	dc.Domain = domain
```

Replace the `Project` literal (line ~181):

```go
		Project: config.ProjectConfig{Name: filepath.Base(abs)},
```

- [ ] **Step 7: Change `status.go`**

Delete this line from `runStatus`:

```go
	fmt.Fprintf(cmd.OutOrStdout(), "domain:  %s\n", cfg.Project.Domain)
```

- [ ] **Step 8: Run cli tests**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/
git commit -m "refactor(cli): per-env domain in init prompt and toml encoder; drop status domain line"
```

---

### Task 5: Docs, changelog, and full verification

**Files:**
- Modify: `README.md:88-91,275-278,302-303,311-325,404,406-419,434-437`
- Modify: `CHANGELOG.md:3-25`
- Modify: `skills/pier/SKILL.md:75,78`

- [ ] **Step 1: Update README**

Feature bullet (lines 88–91):

```markdown
- **Custom domains + HTTPS** — set `domain` in a `[deploy.<env>]`
  section and production serves HTTPS through Caddy with an
  automatic Let's Encrypt certificate (plus `redirect_domains` such
  as `www.example.com`). Leave the domain empty for plain HTTP by IP.
```

Config example: remove `domain = "myapp.example.com"` from the `[project]` block (lines 276–278 become just `name = "myapp"`).

Deploy example comments (lines 302–303):

```toml
# domain = "myapp.example.com"   # optional: serves HTTPS (Let's Encrypt); absent = plain HTTP by IP
# redirect_domains = ["www.myapp.example.com"]   # optional: served and redirected to the domain
```

Fields paragraph (lines 311–325): replace the inheritance prose:

```markdown
`[deploy.<env>]` fields: `host`, `user`, `path`, `branch`, optional
`domain` / `redirect_domains`, and optional `ports` overrides. HTTPS is
implied by domain presence: when `domain` is
non-empty, Caddy serves HTTPS with an automatic Let's Encrypt
certificate (and redirects HTTP to HTTPS), and the deploy health
check probes `https://<domain>/up` — or, with a custom `ports.laravel`
value, `https://<domain>:<port>/up`. When no domain is set the env
serves plain HTTP end-to-end: the deploy health check probes
`http://<host-ip>:<laravel-port>/up` directly on the deploy host IP,
so it passes before DNS or `/etc/hosts` entries point the domain at
the server. The deploy "done" URL prints the env's domain, but
falls back to the deploy host IP when the domain does not resolve
yet, so the printed URL is always usable. The old `tls = true/false`
key is removed — delete it and set (or blank) the domain instead.
```

Custom-domain walkthrough (lines 404, 406–415, 434–437):

- Line 404: `- `www` → your server IP (optional; pair it with `extra_domains`)` → `pair it with `redirect_domains``.
- Lines 406–415 — replace the toml block:

```markdown
4. **Set the domain in `pier.toml`:**

   ```toml
   [deploy.production]
   domain = "myapp.com"
   redirect_domains = ["www.myapp.com"]
   ```
```

- Lines 434–437:

```markdown
Multiple domains: `redirect_domains = ["www.myapp.com"]` serves
`www.myapp.com` and redirects it to the env's domain. Staging:
add a `[deploy.staging]` section with its own `domain =
"staging.myapp.com"` (same A record step for that hostname).
```

- [ ] **Step 2: Update CHANGELOG**

Replace the first "Added" bullet (lines 7–13):

```markdown
- Custom domains + HTTPS: the production webserver is now Caddy
  (`caddy:2-alpine`) with a pier-rendered `docker/caddy/Caddyfile`.
  A non-empty `[deploy.<env>].domain` enables HTTPS with automatic
  Let's Encrypt certificates; `[deploy.<env>].redirect_domains`
  (e.g. `www`) are served and redirected to the env's domain. Empty
  domain = plain HTTP by IP.
```

Replace the init bullet (lines 18–19):

```markdown
- `pier init` prompts for the production domain, written to
  `[deploy.production].domain` (blank = plain HTTP by IP).
```

Add a "Changed" section after the existing "Added" section (before `### Removed`):

```markdown
### Changed

- Domains are per deploy env: `[project].domain` is removed and
  `[deploy.<env>].extra_domains` is renamed to
  `[deploy.<env>].redirect_domains`. Existing configs keep loading
  (unknown keys are ignored), but envs no longer inherit a
  project-wide domain — set `domain` in each `[deploy.<env>]`
  section.
```

- [ ] **Step 3: Update `skills/pier/SKILL.md`**

Replace the `[project]` table row:

```markdown
| `[project]` | `name`. |
```

Replace the `[deploy.<env>]` table row:

```markdown
| `[deploy.<env>]` | `host`, `user`, `path`, `branch`, optional `services` override, `domain` (HTTPS via Caddy; empty = plain HTTP), `redirect_domains` (served and redirected to `domain`), `ports` overrides, optional `queue_workers` (absent = inherit `[stack]`), `before_deploy` / `after_deploy` hook lists. |
```

- [ ] **Step 4: Grep for stragglers**

Run:

```bash
grep -rn "extra_domains\|ExtraDomains\|DomainForEnv\|Project\.Domain\|project\.domain" --include="*.go" --include="*.md" --include="*.toml" internal/ cmd/ README.md CHANGELOG.md skills/pier/
```

Expected: no matches. (Historical references may remain only under `docs/superpowers/` and `.superpowers/`, which are excluded from the path list above.)

- [ ] **Step 5: Full verification**

Run:

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./...
```

Expected: `gofmt -l .` prints nothing; vet, build, and all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add README.md CHANGELOG.md skills/pier/SKILL.md
git commit -m "docs: per-env domains — redirect_domains rename, drop [project].domain"
```

---

## Post-plan notes

- The `docs/superpowers/specs/2026-08-16-caddy-https-custom-domains-design.md` file describes the *old* inheritance model; it is a historical record and stays untouched.
- Manual smoke test (optional, not automated): `pier init` in a scratch Laravel dir — the written `pier.toml` must have `domain` under `[deploy.production]`, and `[project]` must contain only `name`.

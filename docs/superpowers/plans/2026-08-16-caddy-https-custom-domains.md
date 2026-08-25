# Caddy + HTTPS + Custom Domains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the production nginx webserver with Caddy so deploy envs serve HTTPS with automatic Let's Encrypt certificates whenever a domain is configured, with per-env domain overrides and extra (www) domains.

**Architecture:** HTTPS is implied by domain presence: `[deploy.<env>].domain` (falling back to `[project].domain`) turns on Caddy HTTPS; an empty effective domain serves plain HTTP by IP. Pier renders a Caddyfile bind-mounted into `caddy:2-alpine`, reloads it after each deploy, and the deploy pipeline gains a DNS preflight (domain must resolve to the deploy host — the ACME HTTP-01 ownership proof) plus a domain-based HTTPS health probe.

**Tech Stack:** Go 1.25+, BurntSushi/toml, gopkg.in/yaml.v3, Caddy 2 (caddy:2-alpine image), existing cobra CLI and Bubble Tea TUI.

**Spec:** `docs/superpowers/specs/2026-08-16-caddy-https-custom-domains-design.md`

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-16-caddy-https-custom-domains-design.md` — this plan implements it exactly.
- Go 1.25+; run `go test ./...` (unit tests run on macOS, Linux, Windows) and `golangci-lint run` before every commit.
- Boundary rules (README): `cli` never calls Docker directly; `stack/laravel` never imports SSH/Docker; `deploy` never knows about Laravel.
- Every exported type/function/method has a Go doc comment; comments stay in sync with code (AGENTS.md).
- `tls` config key is removed; old `pier.toml` files with `tls = ...` keep loading (BurntSushi/toml ignores unknown keys) — no migration error.
- New webserver image: `caddy:2-alpine`. Rendered file: `docker/caddy/Caddyfile` (replaces `docker/nginx/default.conf`).
- Port keys keep their meaning: `laravel` = primary visible port (container 443 with domain, 80 without), `webserver_http` = HTTP listener (container 80).
- Empty `[project].domain` is now valid (plain HTTP by IP). Validation rejects domains with scheme/port/path/whitespace/`@`.
- Copy rules: error messages use "point an A record for <domain> at the deploy host IP".

---

### Task 1: Config — domain presence replaces `tls`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/parse.go`
- Modify: `internal/config/parse_test.go`
- Modify: `internal/config/testdata/full-ports.toml`

**Interfaces:**
- Consumes: nothing new.
- Produces: `DeployConfig.Domain string` (`toml:"domain"`), `DeployConfig.ExtraDomains []string` (`toml:"extra_domains"`), `Config.DomainForEnv(env string) string`, `validHostname(s string) bool`. `DeployConfig.TLS` no longer exists.

- [ ] **Step 1: Write the failing tests**

In `internal/config/parse_test.go`:

1. Replace the TLS block in `TestLoadFullWithPorts` (lines 25–27):

```go
	if got := prod.Domain; got != "myapp.example.com" {
		t.Errorf("Deploy[production].Domain = %q, want myapp.example.com (domain = ... in full-ports.toml)", got)
	}
	if got := prod.ExtraDomains; len(got) != 1 || got[0] != "www.myapp.example.com" {
		t.Errorf("Deploy[production].ExtraDomains = %v, want [www.myapp.example.com]", got)
	}
```

2. Replace the staging TLS block in `TestLoadFull` (lines 71–73):

```go
	if got := cfg.DomainForEnv("staging"); got != "myapp.example.com" {
		t.Errorf("DomainForEnv(staging) = %q, want myapp.example.com (no [deploy.staging].domain → inherit [project].domain)", got)
	}
```

3. Append new tests at the end of the file:

```go
func TestDomainForEnv(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Deploy: map[string]DeployConfig{
			"prod":  {Domain: "prod.example.com"},
			"stage": {},
		},
	}
	if got := cfg.DomainForEnv("prod"); got != "prod.example.com" {
		t.Errorf(`DomainForEnv("prod") = %q, want prod.example.com (env override wins)`, got)
	}
	if got := cfg.DomainForEnv("stage"); got != "x.example.com" {
		t.Errorf(`DomainForEnv("stage") = %q, want x.example.com (inherit project domain)`, got)
	}
	if got := cfg.DomainForEnv("missing"); got != "x.example.com" {
		t.Errorf(`DomainForEnv("missing") = %q, want x.example.com`, got)
	}
	empty := Config{Project: ProjectConfig{Name: "x"}}
	if got := empty.DomainForEnv("prod"); got != "" {
		t.Errorf("DomainForEnv(prod) = %q, want \"\" (no domain anywhere = plain HTTP)", got)
	}
}

func TestValidateEmptyProjectDomainAllowed(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(empty domain) = %v, want nil (empty domain = plain HTTP by IP)", err)
	}
}

func TestValidateDomainSyntax(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{"project domain with scheme", "https://myapp.example.com"},
		{"project domain with port", "myapp.example.com:8443"},
		{"project domain with path", "myapp.example.com/app"},
	}
	for _, c := range cases {
		cfg := &Config{
			Project: ProjectConfig{Name: "x", Domain: c.field},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		}
		err := cfg.Validate()
		if !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("%s: Validate = %v, want ErrConfigInvalid", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: err = %q, want it to mention the bad domain", c.name, err)
		}
	}
}

func TestValidateDeployDomainSyntax(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Domain: "https://prod.example.com"},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "deploy.production.domain") {
		t.Errorf("err = %q, want it to mention deploy.production.domain", err)
	}
}

func TestValidateExtraDomains(t *testing.T) {
	base := func(extra []string) *Config {
		return &Config{
			Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy: map[string]DeployConfig{
				"production": {Host: "h", User: "u", Path: "p", Branch: "b", ExtraDomains: extra},
			},
		}
	}
	if err := base([]string{"www.x.example.com"}).Validate(); err != nil {
		t.Errorf("Validate(valid extra_domains) = %v, want nil", err)
	}
	if err := base([]string{"bad domain"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(extra_domains with space) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"www.x.example.com", "www.x.example.com"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(duplicate extra_domains) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"x.example.com"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(extra_domains containing primary) = %v, want ErrConfigInvalid", err)
	}
}
```

4. Update `internal/config/testdata/full-ports.toml`: replace the line `tls    = true` with:

```toml
domain = "myapp.example.com"
extra_domains = ["www.myapp.example.com"]
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/...`
Expected: compile errors (`dc.TLS` undefined is not one — parse_test still compiles against the old struct; instead `DeployConfig.Domain` is undefined → compile failure) and the new validation tests fail.

- [ ] **Step 3: Implement**

In `internal/config/config.go`:

1. Update the `DeployConfig` struct — remove the `TLS` field, add `Domain` and `ExtraDomains`:

```go
// DeployConfig is one [deploy.<env>] table: SSH target, remote path,
// branch to build from, per-env host-port overrides, the public
// domain(s) Caddy serves, and the pre/post deploy hook commands.
// A non-empty effective domain means Caddy serves HTTPS with an
// automatic Let's Encrypt certificate; an empty one means plain
// HTTP by IP.
type DeployConfig struct {
	Host   string `toml:"host"`
	User   string `toml:"user"`
	Path   string `toml:"path"`
	Branch string `toml:"branch"`
	// Domain overrides [project].domain for this env. See
	// Config.DomainForEnv for the effective-domain resolution.
	Domain string `toml:"domain"`
	// ExtraDomains lists additional domains Caddy serves, redirecting
	// each to the effective domain (e.g. www.example.com for
	// example.com).
	ExtraDomains []string `toml:"extra_domains"`
	// Builder selects where the production image is built for this
	// env: "host_server" (default, empty means this) builds on the
	// deploy host itself, "local_machine" builds on the machine
	// running pier, "build_server" builds on a dedicated remote
	// machine. The image-mode values stream the finished image to the
	// host over SSH; host_server builds in place.
	Builder string `toml:"builder"`
	// BuildHost, BuildUser, and BuildPath configure the build server
	// used when Builder is "build_server": SSH target and the path
	// where the source tree is synced and built.
	BuildHost string `toml:"build_host"`
	BuildUser string `toml:"build_user"`
	BuildPath string `toml:"build_path"`
	// Services, when present, is the full list of sidecar services
	// for this env, overriding [stack].services. When absent the env
	// inherits [stack].services. An explicitly empty list means the
	// env runs no sidecars.
	Services []string       `toml:"services"`
	Ports    map[string]int `toml:"ports"`
	// QueueWorkers overrides [stack].queue_workers for this env.
	// 0 means inherit the stack value.
	QueueWorkers int `toml:"queue_workers"`
	// BeforeDeploy runs inside the app container on the deploy host
	// after the image build, while the old release is still serving.
	// Commands run in order and stop at the first failure; a failing
	// command aborts the deploy (the old release keeps serving). The
	// phase is skipped on a first deploy, when no app container
	// exists yet.
	BeforeDeploy []string `toml:"before_deploy"`
	// AfterDeploy runs inside the app container on the deploy host
	// after `docker compose up` (and the caddy reload), before the
	// health probe. Commands run in order and stop at the first
	// failure; a failing command aborts the deploy and rolls back to
	// the previous image.
	AfterDeploy []string `toml:"after_deploy"`
}
```

2. Update the `ProjectConfig` doc comment to note empty means HTTP:

```go
// ProjectConfig is the [project] table: a friendly name and the
// public domain the app will be served from in production. An empty
// domain means deploy envs serve plain HTTP by IP (no TLS); a
// non-empty domain means Caddy serves HTTPS with a Let's Encrypt
// certificate.
type ProjectConfig struct {
	Name   string `toml:"name"`
	Domain string `toml:"domain"`
}
```

3. Append `DomainForEnv` next to `ServicesForEnv`:

```go
// DomainForEnv returns the effective public domain for env:
// [deploy.<env>].domain when set, else [project].domain. An empty
// result means the env serves plain HTTP (no TLS); a non-empty
// result means Caddy serves HTTPS with an automatic Let's Encrypt
// certificate.
func (c *Config) DomainForEnv(env string) string {
	if dc, ok := c.Deploy[env]; ok && dc.Domain != "" {
		return dc.Domain
	}
	return c.Project.Domain
}
```

In `internal/config/parse.go`:

4. Add `"strings"` to the imports.

5. In `Validate`, replace the required-domain check (lines 63–65):

```go
	if c.Project.Domain == "" {
		return fmt.Errorf("%w: project.domain is required", ErrConfigInvalid)
	}
```

with:

```go
	if c.Project.Domain != "" && !validHostname(c.Project.Domain) {
		return fmt.Errorf("%w: project.domain %q is not a valid hostname (no scheme, port, or path)", ErrConfigInvalid, c.Project.Domain)
	}
```

6. Change `validateDeployEnv`'s call site in `Validate` and its signature so it can check the effective domain, and add the new checks:

```go
	for env, dc := range c.Deploy {
		if err := c.validateDeployEnv(env, dc); err != nil {
			return err
		}
	}
```

```go
// validateDeployEnv checks every required field and enum-style value
// of one [deploy.<env>] section, plus the domain and extra_domains
// hostname syntax. Extracted from Validate so the per-env rule set
// stays reviewable and Validate's complexity stays in check.
func (c *Config) validateDeployEnv(env string, dc DeployConfig) error {
	configured := dc.Host != "" || dc.User != "" || dc.Path != "" || dc.Branch != ""
	if configured && (dc.Host == "" || dc.User == "" || dc.Path == "" || dc.Branch == "") {
		return fmt.Errorf("%w: deploy.%s requires host, user, path, branch (leave all empty to scaffold)", ErrConfigInvalid, env)
	}
	if dc.Domain != "" && !validHostname(dc.Domain) {
		return fmt.Errorf("%w: deploy.%s.domain %q is not a valid hostname (no scheme, port, or path)", ErrConfigInvalid, env, dc.Domain)
	}
	seen := map[string]bool{}
	for _, d := range dc.ExtraDomains {
		if !validHostname(d) {
			return fmt.Errorf("%w: deploy.%s.extra_domains entry %q is not a valid hostname (no scheme, port, or path)", ErrConfigInvalid, env, d)
		}
		if seen[d] {
			return fmt.Errorf("%w: deploy.%s.extra_domains has duplicate %q", ErrConfigInvalid, env, d)
		}
		seen[d] = true
	}
	if seen[c.DomainForEnv(env)] {
		return fmt.Errorf("%w: deploy.%s.extra_domains must not contain the primary domain %q", ErrConfigInvalid, env, c.DomainForEnv(env))
	}
	if err := validateHookList(env, "before_deploy", dc.BeforeDeploy); err != nil {
		return err
	}
	if err := validateHookList(env, "after_deploy", dc.AfterDeploy); err != nil {
		return err
	}
	if dc.Builder != "" && !validBuilder[dc.Builder] {
		return fmt.Errorf("%w: deploy.%s.builder %q must be host_server, local_machine, or build_server", ErrConfigInvalid, env, dc.Builder)
	}
	if dc.QueueWorkers < 0 || dc.QueueWorkers > MaxQueueWorkers {
		return fmt.Errorf("%w: deploy.%s.queue_workers = %d, must be in 0..%d (0 = inherit)", ErrConfigInvalid, env, dc.QueueWorkers, MaxQueueWorkers)
	}
	if dc.BuilderMode() == "build_server" && (dc.BuildHost == "" || dc.BuildUser == "" || dc.BuildPath == "") {
		return fmt.Errorf("%w: deploy.%s.builder = \"build_server\" requires build_host, build_user, and build_path", ErrConfigInvalid, env)
	}
	return nil
}
```

7. Append `validHostname` after `validateDeployEnv`:

```go
// validHostname reports whether s is a bare hostname or IP: no
// scheme, port, path, whitespace, or userinfo. Used to validate
// project and deploy domains so a pasted URL fails fast at config
// load instead of rendering a broken Caddyfile.
func validHostname(s string) bool {
	if s == "" || strings.ContainsAny(s, " /:@") {
		return false
	}
	return true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/...`
Expected: PASS. If `TestLoadFullWithPorts` still complains, confirm the fixture edit from step 1 landed.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/parse.go internal/config/parse_test.go internal/config/testdata/full-ports.toml
git commit -m "feat(config): per-env domain + extra_domains replace tls flag"
```

---

### Task 2: WebScheme / WebPort keyed on domain presence

**Files:**
- Modify: `internal/stack/laravel/ports.go:87-117`
- Modify: `internal/stack/laravel/ports_test.go:173-215`

**Interfaces:**
- Consumes: `Config.DomainForEnv` from Task 1.
- Produces: `WebScheme(cfg, env)` = "https" iff `cfg.DomainForEnv(env) != ""`; `WebPort(cfg, env)` = laravel override, else 443 with domain / 80 without.

- [ ] **Step 1: Rewrite the failing tests**

Replace `TestWebScheme` and `TestWebPort` in `internal/stack/laravel/ports_test.go` (lines 173–215) with:

```go
func TestWebScheme(t *testing.T) {
	base := func(domain string) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x", Domain: domain},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {}},
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
			Project: config.ProjectConfig{Name: "x", Domain: domain},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {Ports: ports}},
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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stack/laravel/ -run 'TestWebScheme|TestWebPort'`
Expected: FAIL — the old implementation keys on `DeployConfig.TLS`, which no longer compiles (TLS was removed in Task 1), so this fails at compile time.

- [ ] **Step 3: Implement**

Replace `WebScheme` and `WebPort` in `internal/stack/laravel/ports.go`:

```go
// WebScheme returns the URL scheme for the env's primary web
// endpoint: "https" when the env has an effective domain (Caddy
// provisions a Let's Encrypt certificate automatically, proving
// ownership via the ACME HTTP-01 challenge on ports 80/443), else
// "http" (no domain, plain HTTP by IP).
func WebScheme(cfg config.Config, env string) string {
	if cfg.DomainForEnv(env) != "" {
		return "https"
	}
	return "http"
}

// WebPort returns the host port for the env's primary web endpoint:
// the [deploy.<env>.ports.laravel] override when set (0 = don't
// expose falls back to the default), else 443 when the env has a
// domain or 80 for the no-domain plain-HTTP default.
func WebPort(cfg config.Config, env string) int {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	if v, set := deployCfg.Ports["laravel"]; set && v != 0 {
		return v
	}
	if cfg.DomainForEnv(env) != "" {
		return 443
	}
	return 80
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/stack/laravel/ -run 'TestWebScheme|TestWebPort'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stack/laravel/ports.go internal/stack/laravel/ports_test.go
git commit -m "feat(laravel): WebScheme/WebPort keyed on domain presence"
```

---

### Task 3: Caddy webserver service + Caddyfile rendering

**Files:**
- Modify: `internal/stack/laravel/prod.go`
- Modify: `internal/stack/laravel/prod_test.go`
- Modify: `internal/stack/laravel/testdata/golden/compose-prod-ports-override.yml`
- Modify: `internal/stack/stack.go:53-56` (doc comment)

**Interfaces:**
- Consumes: `Config.DomainForEnv`, `WebScheme`, `WebPort` from Tasks 1–2.
- Produces: `renderCaddyfile(cfg config.Config, env string) []byte`; `webserverPorts(bind string, override map[string]int, domain bool) []string` (renamed param); `GenerateProdFiles` emits `docker/caddy/Caddyfile`, never `docker/nginx/default.conf`.

- [ ] **Step 1: Update the failing tests**

In `internal/stack/laravel/prod_test.go`:

1. `TestGenerateProdFilesNoServices` (line 31–33): replace the nginx check with:

```go
	if findFile(files, "docker/caddy/Caddyfile") == nil {
		t.Error("docker/caddy/Caddyfile missing")
	}
```

2. Replace `TestGenerateProdFilesNginxProxiesToAppHTTPServer` (lines 173–196) with a Caddyfile test:

```go
func TestGenerateProdFilesCaddyfileProxiesToAppHTTPServer(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
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
		t.Errorf("Caddyfile missing the site block for the effective domain:\n%s", body)
	}
}

func TestRenderCaddyfileHTTPOnly(t *testing.T) {
	body := renderCaddyfile(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, "production")
	if !strings.Contains(string(body), ":80 {") {
		t.Errorf("no-domain Caddyfile must serve :80 (site address with no hostname):\n%s", body)
	}
	if !strings.Contains(string(body), "reverse_proxy app:80") {
		t.Errorf("no-domain Caddyfile missing reverse_proxy app:80:\n%s", body)
	}
}

func TestRenderCaddyfileExtraDomains(t *testing.T) {
	body := renderCaddyfile(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {ExtraDomains: []string{"www.myapp.example.com"}},
		},
	}, "production")
	if !strings.Contains(string(body), "www.myapp.example.com {\n    redir https://myapp.example.com{uri}\n}") {
		t.Errorf("extra_domains entry must redirect to the primary domain:\n%s", body)
	}
}

func TestRenderCaddyfilePerEnvDomainOverride(t *testing.T) {
	body := renderCaddyfile(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"staging": {Domain: "staging.myapp.example.com"},
		},
	}, "staging")
	if !strings.Contains(string(body), "staging.myapp.example.com {") {
		t.Errorf("per-env domain override must win over [project].domain:\n%s", body)
	}
}
```

3. `TestGenerateProdFilesWebserverDefaultPorts` (line 378): this config has `Project.Domain = "myapp.example.com"`, so under the new rules it publishes 443+80. Replace the whole function with the no-domain case:

```go
func TestGenerateProdFilesWebserverNoDomainPorts(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b"},
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
		t.Errorf("webserver ports = %v, want it to include 80:80 (no domain: laravel → container 80)", web.Ports)
	}
	if found443 {
		t.Errorf("webserver ports = %v, must not include 443:443 when no domain is set (plain HTTP only)", web.Ports)
	}
}
```

4. `TestGenerateProdFilesPortPartialOverride` (line 423): with `Project.Domain` set, the laravel override 8383 now maps to container 443, and webserver_http default 80:80 appears. Replace the block from `found8383, found80 := false, false` through the end of the webserver checks (lines 454–468) with:

```go
	found8383, found80 := false, false
	for _, p := range web.Ports {
		if p == "8383:443" {
			found8383 = true
		}
		if p == "80:80" {
			found80 = true
		}
	}
	if !found8383 {
		t.Errorf("webserver ports = %v, want it to include 8383:443 (laravel override → container 443 when a domain is set)", web.Ports)
	}
	if !found80 {
		t.Errorf("webserver ports = %v, want the webserver_http default 80:80 published when a domain is set", web.Ports)
	}
```

5. `TestGenerateProdFilesWebserverTLSPorts` (line 481): drop `TLS: true` from the DeployConfig (the set `Project.Domain` already enables HTTPS) and update the failure message text to `(domain set: laravel=443, webserver_http=80)`. Also add a webserver image/volume assertion at the end of this test:

```go
	var img struct {
		Services map[string]struct {
			Image   string   `yaml:"image"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &img); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if img.Services["webserver"].Image != "caddy:2-alpine" {
		t.Errorf("webserver image = %q, want caddy:2-alpine", img.Services["webserver"].Image)
	}
	wantVols := []string{
		"./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro",
		"caddy_data:/data",
		"caddy_config:/config",
	}
	for _, v := range wantVols {
		found := false
		for _, got := range img.Services["webserver"].Volumes {
			if got == v {
				found = true
			}
		}
		if !found {
			t.Errorf("webserver volumes = %v, missing %q (Caddyfile bind mount + persistent cert volumes)", img.Services["webserver"].Volumes, v)
		}
	}
```

6. Rename `TestGenerateProdFilesWebserverHTTPOverrideWhenNoTLS` (line 525) to `TestGenerateProdFilesWebserverHTTPOverrideWhenNoDomain`, set `Project.Domain = ""`, and update the message text to `(no domain: laravel default 80:80 + explicit webserver_http 8080:80)`.

7. `TestGenerateProdEnvExampleAPPURL` (line 569): replace the whole function with:

```go
func TestGenerateProdEnvExampleAPPURL(t *testing.T) {
	s := New()
	httpFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b"}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (http): %v", err)
	}
	httpEnv := findFile(httpFiles, ".env.production")
	if httpEnv == nil {
		t.Fatal(".env.production missing (http)")
	}
	if contains(string(httpEnv.Contents), "Copy to") {
		t.Errorf(".env.production should not contain copy instructions:\n%s", httpEnv.Contents)
	}
	if !contains(string(httpEnv.Contents), "APP_URL=http://h:80") {
		t.Errorf("env missing no-domain APP_URL (host:port fallback):\n%s", httpEnv.Contents)
	}

	httpsFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b"}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (https): %v", err)
	}
	httpsEnv := findFile(httpsFiles, ".env.production")
	if httpsEnv == nil {
		t.Fatal(".env.production missing (https)")
	}
	if !contains(string(httpsEnv.Contents), "APP_URL=https://myapp.example.com") {
		t.Errorf("env missing HTTPS APP_URL:\n%s", httpsEnv.Contents)
	}
}
```

8. Update `internal/stack/laravel/testdata/golden/compose-prod-ports-override.yml` — replace the `webserver` block (lines 35–46) with:

```yaml
    webserver:
        image: caddy:2-alpine
        ports:
            - 8383:443
            - 80:80
        volumes:
            - ./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
            - caddy_data:/data
            - caddy_config:/config
        networks:
            - pier
        depends_on:
            - app
        restart: unless-stopped
```

and add `caddy_data`/`caddy_config` to the top-level `volumes:` block:

```yaml
volumes:
    caddy_config:
        driver: local
    caddy_data:
        driver: local
    redis_data:
        driver: local
```

9. In `internal/stack/stack.go`, update the `GenerateProdFiles` doc comment: replace "the nginx default.conf" with "the caddy Caddyfile".

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stack/laravel/`
Expected: FAIL — tests expect `docker/caddy/Caddyfile`, `caddy:2-alpine`, and domain-driven ports that `prod.go` doesn't render yet.

- [ ] **Step 3: Implement**

In `internal/stack/laravel/prod.go`:

1. In `GenerateProdFiles`, replace line 33 (`nginx := renderNginx(cfg)`) with:

```go
	caddyfile := renderCaddyfile(cfg, env)
```

and replace the `{Path: "docker/nginx/default.conf", ...}` entry (line 50) with:

```go
		{Path: "docker/caddy/Caddyfile", Contents: caddyfile, Mode: 0644},
```

2. Replace the `webserver` service (lines 98–105) with:

```go
			"webserver": {
				Image:     "caddy:2-alpine",
				Restart:   "unless-stopped",
				Ports:     webserverPorts("", deployCfg.Ports, cfg.DomainForEnv(env) != ""),
				Volumes: []string{
					"./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro",
					"caddy_data:/data",
					"caddy_config:/config",
				},
				Networks:  []string{"pier"},
				DependsOn: []string{"app"},
			},
```

3. Replace `webserverPorts` (lines 219–245) with the domain-flag version:

```go
// webserverPorts assembles the `ports:` slice for the webserver
// service. domain reports whether the env has an effective domain:
// then Caddy serves HTTPS (container 443, port key "laravel") plus
// the HTTP→HTTPS redirect listener (container 80, port key
// "webserver_http"); without a domain it serves plain HTTP on
// container 80 under the "laravel" key. Either key may be 0 in the
// override to opt out. bind is the host-side bind prefix ("" = no
// prefix, host firewall restricts access; the deploy path always
// passes "").
func webserverPorts(bind string, override map[string]int, domain bool) []string {
	laravelDefault, laravelContainer := 80, 80
	if domain {
		laravelDefault, laravelContainer = 443, 443
	}
	defaults := map[string]int{"laravel": laravelDefault, "webserver_http": 80}
	var out []string
	if host, ok := ResolvePort("laravel", override, defaults); ok {
		out = append(out, PortBinding(bind, host, laravelContainer))
	}
	if domain {
		if host, ok := ResolvePort("webserver_http", override, defaults); ok {
			out = append(out, PortBinding(bind, host, 80))
		}
	} else if v, set := override["webserver_http"]; set && v != 0 {
		out = append(out, PortBinding(bind, v, 80))
	}
	return out
}
```

4. In `renderProdEnv`, replace the APP_URL line (line 312) with:

```go
	if domain := cfg.DomainForEnv(env); domain != "" {
		fmt.Fprintf(&b, "APP_URL=%s://%s\n\n", WebScheme(cfg, env), domain)
	} else {
		host := "localhost"
		if dc, ok := cfg.Deploy[env]; ok && dc.Host != "" {
			host = dc.Host
		}
		fmt.Fprintf(&b, "APP_URL=http://%s:%d\n\n", host, WebPort(cfg, env))
	}
```

5. Replace `renderNginx` (lines 353–377) with:

```go
// renderCaddyfile renders the production Caddyfile for env. With an
// effective domain, Caddy serves HTTPS with an automatic Let's
// Encrypt certificate — ownership is proven by the ACME HTTP-01
// challenge on ports 80/443, so the domain's A record must point at
// the deploy host — and every extra_domains entry redirects to the
// primary domain. Without a domain it serves plain HTTP on container
// port 80.
func renderCaddyfile(cfg config.Config, env string) []byte {
	domain := cfg.DomainForEnv(env)
	if domain == "" {
		return []byte(":80 {\n    encode gzip\n    reverse_proxy app:80\n}\n")
	}
	var dc config.DeployConfig
	if v, ok := cfg.Deploy[env]; ok {
		dc = v
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s {\n    encode gzip\n    reverse_proxy app:80\n}\n", domain)
	for _, extra := range dc.ExtraDomains {
		fmt.Fprintf(&b, "\n%s {\n    redir https://%s{uri}\n}\n", extra, domain)
	}
	return b.Bytes()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/stack/laravel/`
Expected: PASS. If a leftover test still references `TLS:` or `renderNginx`, fix it now (search: `rg 'TLS|nginx' internal/stack/laravel/`).

- [ ] **Step 5: Commit**

```bash
git add internal/stack/laravel/prod.go internal/stack/laravel/prod_test.go internal/stack/laravel/testdata/golden/compose-prod-ports-override.yml internal/stack/stack.go
git commit -m "feat(laravel): caddy webserver with rendered Caddyfile and domain-driven ports"
```

---

### Task 4: Deploy — sync filter + caddy reload

**Files:**
- Modify: `internal/deploy/syncfilter.go`
- Modify: `internal/deploy/syncfilter_test.go`
- Modify: `internal/deploy/up.go`
- Modify: `internal/deploy/up_test.go`
- Modify: `internal/deploy/hooks_test.go:334,397-398,627-628`
- Modify: `internal/deploy/deploy_unit_test.go:399-407,461`
- Modify: `internal/deploy/deploy.go:196-198` (comment only)
- Modify: `internal/config/config.go:131-132` (AfterDeploy comment only)
- Modify: `internal/deploy/health.go:22-26` (comment only)

**Interfaces:**
- Consumes: nothing new (the Caddyfile is shipped instead of the nginx conf).
- Produces: `deployFilesOnly` includes `docker/caddy/Caddyfile`; `Up` reloads via `caddy reload --config /etc/caddy/Caddyfile`.

- [ ] **Step 1: Update the failing tests**

1. `internal/deploy/syncfilter_test.go` — in `TestDeployFilesOnly` replace `"docker/nginx/default.conf"` with `"docker/caddy/Caddyfile"`; in `TestDeployFilesOnlyDescendsAncestorDirs` replace the loop slice `[]string{"docker", "docker/nginx"}` with `[]string{"docker", "docker/caddy"}` and update the comment.

2. `internal/deploy/up_test.go` — rename `TestUpReloadsWebserverNginx` to `TestUpReloadsWebserverCaddy` and replace the body with:

```go
func TestUpReloadsWebserverCaddy(t *testing.T) {
	var cmds []string
	r := &recordingUpRunner{cmds: &cmds}
	if err := Up(context.Background(), r, "/srv/myapp"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("Up ran %d commands, want 2 (compose up + caddy reload); got: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "docker compose --env-file .env.production -f docker-compose.prod.yml up -d --wait --wait-timeout 120 --remove-orphans") {
		t.Errorf("up command = %q, want `docker compose --env-file .env.production -f docker-compose.prod.yml up -d --wait --wait-timeout 120 --remove-orphans` (--remove-orphans stops and removes containers of services dropped from the compose file — the per-env teardown contract — while preserving named volumes)", cmds[0])
	}
	if !strings.Contains(cmds[1], "--env-file .env.production") {
		t.Errorf("reload command = %q, want it to pass --env-file .env.production so compose interpolation does not warn", cmds[1])
	}
	if !strings.Contains(cmds[1], "exec -T webserver caddy reload --config /etc/caddy/Caddyfile") {
		t.Errorf("reload command = %q, want it to reload the webserver's caddy so bind-mounted Caddyfile changes (the sync rewrites files in place, preserving the inode) take effect without a container recreate", cmds[1])
	}
}
```

3. `internal/deploy/hooks_test.go`:
   - Line 334 comment: `nginx reload → after_deploy` → `caddy reload → after_deploy`.
   - Lines 397–398: change `"nginx -s reload"` to `"caddy reload --config /etc/caddy/Caddyfile"` and the message "want the nginx reload" → "want the caddy reload".
   - Lines 627–628: same replacement for the rollback reload assertion.

4. `internal/deploy/deploy_unit_test.go` — in `TestPipelineSyncTargetsPerBuilder`:
   - Lines 399–407: comment and seed change to:

```go
			// The render phase does not write docker/caddy/Caddyfile
			// (it exists in a real project from `pier init`); seed it
			// so the image-mode host sync has something to ship.
			if err := os.MkdirAll(filepath.Join("docker", "caddy"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join("docker", "caddy", "Caddyfile"), []byte("example.com {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
```
   - Line 461: replace `filepath.Join("docker", "nginx", "default.conf")` with `filepath.Join("docker", "caddy", "Caddyfile")`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestUp|TestDeployFilesOnly|TestPipelineSyncTargetsPerBuilder'`
Expected: FAIL — sync filter still ships the nginx path and Up still reloads nginx.

- [ ] **Step 3: Implement**

1. `internal/deploy/syncfilter.go` — replace the `deployFilesOnly` list and its comment:

```go
// deployFilesOnly is the filter for image-mode host syncs: exactly
// the files the host needs to run the stack (the compose file, the
// env file with secrets, and the bind-mounted caddy Caddyfile).
// Everything else is excluded — the host never receives the source
// tree when the image is built elsewhere.
var deployFilesOnly = []string{
	"--include=docker-compose.prod.yml",
	"--include=.env.production",
	"--include=docker/caddy/Caddyfile",
	"--exclude=*",
}
```

2. `internal/deploy/up.go` — update the doc comment (replace every "nginx" mention with caddy) and the reload command:

```go
	reload := fmt.Sprintf("cd %s && docker compose --env-file %s -f %s exec -T webserver caddy reload --config /etc/caddy/Caddyfile || true", dir, remoteEnvFile, remoteComposeFile)
```

Comment text: "and then reloads the webserver's caddy", "a changed Caddyfile is visible to the webserver container, but caddy only reads config at start/reload".

3. `internal/deploy/deploy.go` line 196–198: `(after up and the nginx reload, before` → `(after up and the caddy reload, before`.

4. `internal/config/config.go` AfterDeploy comment: `(and the nginx reload)` → `(and the caddy reload)`.

5. `internal/deploy/health.go` `DefaultHealthConfig` doc: `scheme and port resolved from [deploy.<env>].tls and the "laravel" port` → `scheme and port resolved from the env's effective domain (see HealthURL)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/deploy/ ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/syncfilter.go internal/deploy/syncfilter_test.go internal/deploy/up.go internal/deploy/up_test.go internal/deploy/hooks_test.go internal/deploy/deploy_unit_test.go internal/deploy/deploy.go internal/deploy/health.go internal/config/config.go
git commit -m "feat(deploy): ship docker/caddy/Caddyfile and reload caddy after up"
```

---

### Task 5: Deploy — DNS preflight + domain-based health probe

**Files:**
- Create: `internal/deploy/dns.go`
- Create: `internal/deploy/dns_test.go`
- Modify: `internal/deploy/deploy.go`
- Modify: `internal/deploy/deploy_unit_test.go`
- Modify: `internal/deploy/hooks_test.go` (add stub to 6 pipeline tests)
- Modify: `internal/deploy/deploy_integration_test.go:56`

**Interfaces:**
- Consumes: `Config.DomainForEnv`, `WebScheme`, `WebPort`, the `lookupHost` seam (already in `deploy.go:432`).
- Produces: `checkDomainDNS(cfg config.Config, env string) error`; `pipelineCheckDNS` seam; `HealthURL` probes `https://<domain>/up` when a domain is set, else `http://<host>:<port>/up`; `ResolvedURL` uses the effective domain.

- [ ] **Step 1: Write the failing tests**

1. Create `internal/deploy/dns_test.go`:

```go
package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

// pinDNS makes the DNS seam deterministic for a test: resolve maps
// hostnames to IP sets; unknown hosts fail resolution.
func pinDNS(t *testing.T, resolve map[string][]string) {
	t.Helper()
	old := lookupHost
	lookupHost = func(host string) ([]string, error) {
		if ips, ok := resolve[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupHost = old })
}

func TestCheckDomainDNSSkipsWithoutDomain(t *testing.T) {
	pinDNS(t, map[string][]string{})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	if err := checkDomainDNS(cfg, "production"); err != nil {
		t.Errorf("checkDomainDNS(no domain) = %v, want nil (plain HTTP needs no DNS)", err)
	}
}

func TestCheckDomainDNSFailsWhenDomainDoesNotResolve(t *testing.T) {
	pinDNS(t, map[string][]string{"1.2.3.4": {"1.2.3.4"}})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	err := checkDomainDNS(cfg, "production")
	if err == nil {
		t.Fatal("checkDomainDNS = nil, want DNS hint error (ACME HTTP-01 needs the A record)")
	}
	if !strings.Contains(err.Error(), "point an A record") {
		t.Errorf("err = %q, want the actionable A-record hint", err)
	}
	if !strings.Contains(err.Error(), "myapp.example.com") {
		t.Errorf("err = %q, want it to name the domain", err)
	}
}

func TestCheckDomainDNSFailsOnMismatch(t *testing.T) {
	pinDNS(t, map[string][]string{
		"myapp.example.com": {"5.6.7.8"},
		"1.2.3.4":           {"1.2.3.4"},
	})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	err := checkDomainDNS(cfg, "production")
	if err == nil || !strings.Contains(err.Error(), "resolves to") {
		t.Fatalf("checkDomainDNS = %v, want mismatch error naming both IP sets", err)
	}
}

func TestCheckDomainDNSPassesOnMatch(t *testing.T) {
	pinDNS(t, map[string][]string{
		"myapp.example.com": {"1.2.3.4"},
		"1.2.3.4":           {"1.2.3.4"},
	})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	if err := checkDomainDNS(cfg, "production"); err != nil {
		t.Errorf("checkDomainDNS(match) = %v, want nil", err)
	}
}
```

(Add `"strings"` to the imports.)

2. `internal/deploy/deploy_unit_test.go` — rewrite the URL tests:

- `TestDeployFinalStateURL` (line 63): expected becomes `"https://myapp.example.com:8383"` (domain set → https; override 8383).

- `TestDeployFinalStateURLDefault` (line 78): expected becomes `"https://myapp.example.com:443"`.

- `TestDeployFinalStateURLTLS` (line 93): rename to `TestDeployFinalStateURLDomain` and replace the cases with:

```go
		{"domain, no override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b"}, "https://myapp.example.com:443"},
		{"domain, override", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8443}}, "https://myapp.example.com:8443"},
		{"domain, laravel=0 falls back to 443", config.DeployConfig{Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 0}}, "https://myapp.example.com:443"},
```

- `TestHealthURL` (line 116): replace the whole function with:

```go
func TestHealthURL(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		dc     config.DeployConfig
		want   string
	}{
		{"no domain, plain http default", "", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b"}, "http://192.168.1.10:80/up"},
		{"no domain, laravel override", "", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.1.10:8383/up"},
		{"domain, probe the domain over https", "myapp.example.com", config.DeployConfig{Host: "192.168.1.10", User: "u", Path: "p", Branch: "b"}, "https://myapp.example.com/up"},
	}
	for _, c := range cases {
		url := HealthURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: c.domain},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: HealthURL = %q, want %q", c.name, url, c.want)
		}
	}
}
```

- `TestResolvedURLFallsBackToHostIPWhenDomainDoesNotResolve` (line 138): with a set domain, scheme is now https even when falling back to the host IP. Replace the whole function with:

```go
func TestResolvedURLFallsBackToHostIPWhenDomainDoesNotResolve(t *testing.T) {
	pinLookup(t, false)
	cases := []struct {
		name   string
		domain string
		dc     config.DeployConfig
		want   string
	}{
		{"no domain, plain http default", "", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b"}, "http://192.168.122.30:80"},
		{"no domain, laravel override", "", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"laravel": 8383}}, "http://192.168.122.30:8383"},
		{"domain, falls back to host IP over https", "myapp.example.com", config.DeployConfig{Host: "192.168.122.30", User: "u", Path: "p", Branch: "b"}, "https://192.168.122.30:443"},
	}
	for _, c := range cases {
		url := ResolvedURL(config.Config{
			Project: config.ProjectConfig{Name: "myapp", Domain: c.domain},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": c.dc},
		}, "production")
		if url != c.want {
			t.Errorf("%s: ResolvedURL = %q, want %q", c.name, url, c.want)
		}
	}
}
```

- `TestResolvedURLKeepsDomainWithoutDeployHost` (line 161): domain is set → expected `"https://myapp.example.com:443"`.

- `TestResolvedURLBareIPDomain` (line 173): expected becomes `"https://192.168.122.30:443"`.

3. Stub the new seam in every pipeline test that reaches preflight with a domain set. Add to the seam block of each test (every one already has an `origProbe, origEnsure := pipelineProbe, pipelineEnsurePath` style block — in `TestPipelineSyncFailureWrapsPreflight` that block is `origDial, origProbe, origEnsure := pipelineDial, pipelineProbe, pipelineEnsurePath` — extend it the same way):

```go
	origCheckDNS := pipelineCheckDNS
	pipelineCheckDNS = func(cfg config.Config, env string) error { return nil }
	defer func() { pipelineCheckDNS = origCheckDNS }()
```

in these tests:
- `internal/deploy/deploy_unit_test.go`: `TestPipelineSyncsFilesToRemote`, `TestPipelineRenderAbortsWithoutLocalEnvFile`, `TestPipelineSyncFailureWrapsPreflight`, `TestPipelineSyncTargetsPerBuilder`, `TestPipelineBuildServerPreflightDialsBoth`.
- `internal/deploy/hooks_test.go`: `TestPipelineRunsHooksAtCorrectStages`, `TestPipelineSkipsHooksWhenListsEmpty`, `TestPipelineBeforeDeployFailureAborts`, `TestPipelineAfterDeployFailureFirstDeployReportsHook`, `TestPipelineAfterDeployFailureRollsBackToPrevious`, `TestPipelineBeforeDeploySkippedOnFirstDeploy`.

4. `internal/deploy/deploy_integration_test.go:56` — change `Project: config.ProjectConfig{Name: "pierit", Domain: "pierit.example.com"}` to `Project: config.ProjectConfig{Name: "pierit"}` (the test host has no DNS for that domain).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/deploy/`
Expected: FAIL — `checkDomainDNS` undefined, and the URL tests fail against the old `HealthURL`/`ResolvedURL`.

- [ ] **Step 3: Implement**

1. Create `internal/deploy/dns.go`:

```go
package deploy

import (
	"fmt"

	"github.com/Bonnary/pier/internal/config"
)

// checkDomainDNS verifies that the env's effective domain resolves
// to the deploy host — the precondition for Caddy's ACME HTTP-01
// challenge, which proves ownership by answering a token on port 80
// of the domain (a request that only reaches the server when the
// domain's A record points at it). A missing or mismatched record
// fails preflight with an actionable hint instead of a confusing
// certificate error minutes into the deploy. Envs without a domain
// (plain HTTP) are skipped. The host itself resolving is optional
// (it may live only in the SSH config); the health probe surfaces
// any real reachability problem.
func checkDomainDNS(cfg config.Config, env string) error {
	domain := cfg.DomainForEnv(env)
	if domain == "" {
		return nil
	}
	dc := cfg.Deploy[env]
	domainIPs, err := lookupHost(domain)
	if err != nil || len(domainIPs) == 0 {
		return fmt.Errorf(
			"domain %s does not resolve — point an A record for %s at the deploy host IP (%s), wait for DNS propagation, then re-deploy",
			domain, domain, dc.Host)
	}
	hostIPs, err := lookupHost(dc.Host)
	if err != nil || len(hostIPs) == 0 {
		return nil
	}
	for _, dip := range domainIPs {
		for _, hip := range hostIPs {
			if dip == hip {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"domain %s resolves to %v, but the deploy host %s resolves to %v — point an A record for %s at the deploy host IP, wait for DNS propagation, then re-deploy",
		domain, domainIPs, dc.Host, hostIPs, domain)
}
```

2. `internal/deploy/deploy.go`:

- Add the seam next to `pipelineEnsurePath` (after line 37):

```go
// pipelineCheckDNS is a seam for tests to inject a fake domain-DNS
// preflight step into the deploy pipeline's preflight phase.
var pipelineCheckDNS = func(cfg config.Config, env string) error {
	return checkDomainDNS(cfg, env)
}
```

- In `preflight`, after the EnsureDeployPath error block (after line 265) and before the `build_server` block, add:

```go
	if p.Config != nil {
		if err := pipelineCheckDNS(*p.Config, p.Env); err != nil {
			client.Close()
			return nil, err
		}
	}
```

- Replace `ResolvedURL` (lines 413–428) with:

```go
// ResolvedURL returns the public URL for the deployed env: scheme
// and port resolved from the env's effective domain and the
// "laravel" port (default 443 with a domain, 80 for the plain-HTTP
// default, or the per-env override from
// [deploy.<env>.ports.laravel]). The host is the effective domain
// when it resolves (DNS or /etc/hosts); otherwise it falls back to
// the deploy host IP, so the printed URL is usable before DNS
// entries point the domain at the server.
func ResolvedURL(cfg config.Config, env string) string {
	domain := cfg.DomainForEnv(env)
	host := domain
	if host == "" || !hostResolvable(host) {
		if dc, ok := cfg.Deploy[env]; ok && dc.Host != "" {
			host = dc.Host
		}
	}
	return fmt.Sprintf("%s://%s:%d", laravelpkg.WebScheme(cfg, env), host, laravelpkg.WebPort(cfg, env))
}
```

- Replace `HealthURL` (lines 441–451) with:

```go
// HealthURL returns the URL the health probe GETs for env. With an
// effective domain the probe targets https://<domain>/up with normal
// TLS verification — an end-to-end check that exercises the real
// Let's Encrypt certificate (the DNS preflight already verified the
// domain points at the deploy host). Without a domain it probes the
// deploy host IP with the resolved "laravel" port, so health checks
// pass before DNS or /etc/hosts entries exist.
func HealthURL(cfg config.Config, env string) string {
	if domain := cfg.DomainForEnv(env); domain != "" {
		return "https://" + domain + "/up"
	}
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	return fmt.Sprintf("http://%s:%d/up", deployCfg.Host, laravelpkg.WebPort(cfg, env))
}
```

- Update the comment in `Run` phase 7 (line 197): "and the nginx reload" is already changed in Task 4; verify no "nginx" remains (`rg -i nginx internal/deploy/` should only match test fixtures you already updated).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/deploy/`
Expected: PASS. If any pipeline test still fails on the DNS check, it needs the `pipelineCheckDNS` stub from step 1.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/dns.go internal/deploy/dns_test.go internal/deploy/deploy.go internal/deploy/deploy_unit_test.go internal/deploy/hooks_test.go internal/deploy/deploy_integration_test.go
git commit -m "feat(deploy): DNS preflight for ACME readiness and domain-based health probe"
```

---

### Task 6: CLI — init domain prompt + toml encoding

**Files:**
- Modify: `internal/cli/init.go`
- Modify: `internal/cli/init_test.go`
- Modify: `internal/cli/toml.go`
- Modify: `internal/cli/toml_test.go`

**Interfaces:**
- Consumes: `Config.DomainForEnv` semantics (Task 1), `prompt` helper already in init.go.
- Produces: `pier init` prompts "Project domain (e.g. myapp.com; blank = plain HTTP by IP): " and writes the answer (or `""`) to `[project].domain`; `tomlEncode` writes `domain`/`extra_domains` into `[deploy.<env>]` when set.

- [ ] **Step 1: Write the failing tests**

1. `internal/cli/toml_test.go` — append:

```go
func TestTomlEncodeRendersDeployDomain(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "/srv/x", Branch: "main",
				Domain: "prod.example.com", ExtraDomains: []string{"www.prod.example.com"},
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
		`extra_domains = ["www.prod.example.com"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tomlEncode output missing %q; got:\n%s", want, got)
		}
	}
	for _, sec := range strings.Split(got, "\n[deploy.") {
		if strings.HasPrefix(sec, "staging]") && (strings.Contains(sec, "domain") || strings.Contains(sec, "extra_domains")) {
			t.Errorf("staging section must not emit domain keys (empty = inherit); got:\n%s", sec)
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
	if got := loaded.DomainForEnv("production"); got != "prod.example.com" {
		t.Errorf("DomainForEnv(production) = %q, want prod.example.com after round trip", got)
	}
	if got := loaded.DomainForEnv("staging"); got != "x.example.com" {
		t.Errorf("DomainForEnv(staging) = %q, want x.example.com (inherit) after round trip", got)
	}
}
```

2. `internal/cli/init_test.go`:

- New test:

```go
func TestInitPromptsForDomain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetIn(strings.NewReader("8.3\n22\n\n1\nmyapp.com\n\n\n\n"))
	root.SetArgs([]string{"init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `domain = "myapp.com"`) {
		t.Errorf("pier.toml missing the prompted domain:\n%s", got)
	}
}
```

- Update the stdin sequences in the tests whose answers must shift by the new domain prompt (the prompt order becomes: php, node, services, builder, **domain**, host, user, path, then build host/user/path):
  - `TestInitPromptsForDeployFields` (line 372): `"8.3\n22\nredis\n3\nprod.example.com\ndeploy\n/srv/myapp\nbuild.example.com\nbuilder\n/srv/build\n"` → `"8.3\n22\nredis\n3\nmyapp.com\nprod.example.com\ndeploy\n/srv/myapp\nbuild.example.com\nbuilder\n/srv/build\n"` and add `domain = "myapp.com"` to the expected strings list.
  - `TestInitEmptyDeployPromptsSkipFields` (line 410): `"8.3\n22\n\n1\n\n\n\n"` → `"8.3\n22\n\n1\n\n\n\n\n"` (one more blank for the domain).
  - `TestInitBuildServerEmptyAnswerReprompts` (line 445): `"8.3\n22\n\n3\n\n\n\nbh\nbu\n\n/srv/build\n"` → `"8.3\n22\n\n3\n\n\n\n\nbh\nbu\n\n/srv/build\n"`.
  - `TestInitBuildServerRequiredFieldGivesUp` (line 471): `"8.3\n22\n\n3\n\n\n\n"` → `"8.3\n22\n\n3\n\n\n\n\n"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/`
Expected: FAIL — prompt sequences land in the wrong fields (`domain = "prod.example.com"` / deploy-host prompt eats the wrong answer) and tomlEncode emits no deploy domain.

- [ ] **Step 3: Implement**

1. `internal/cli/init.go` — after the builder validation (after line 126) and before the host prompt (line 127), insert:

```go
	domain := prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Project domain (e.g. myapp.com; blank = plain HTTP by IP): ", "")
```

and change the Project config line (line 180):

```go
	cfg := config.Config{
		Project: config.ProjectConfig{Name: filepath.Base(abs), Domain: domain},
		Stack:   config.StackConfig{Type: "laravel", PHP: php, Node: node, Services: services},
		Deploy:  map[string]config.DeployConfig{"production": dc},
	}
```

2. `internal/cli/toml.go` — inside the `[deploy.<env>]` loop, after the `branch` line (line 28), add:

```go
		if dc.Domain != "" {
			fmt.Fprintf(&b, "domain = %q\n", dc.Domain)
		}
		if len(dc.ExtraDomains) > 0 {
			fmt.Fprintf(&b, "extra_domains = %s\n", tomlStringArray(dc.ExtraDomains))
		}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go internal/cli/toml.go internal/cli/toml_test.go
git commit -m "feat(cli): init prompts for the domain; toml encoder writes deploy domain keys"
```

---

### Task 7: Docs — README custom domain guide + config reference + CHANGELOG

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: everything shipped in Tasks 1–6.
- Produces: user-facing docs covering the new config keys, the DNS setup walkthrough, and the `tls` removal migration note.

- [ ] **Step 1: Update README**

1. Features list — in the `pier init` bullet, change "Asks the full deploy setup too: host, user, path, branch, and the build machine" to "Asks the full deploy setup too: domain, host, user, path, branch, and the build machine". Add a new bullet after the `pier deploy` bullet:

```markdown
- **Custom domains + HTTPS** — set `domain` in `pier.toml` (per env
  or project-wide) and production serves HTTPS through Caddy with an
  automatic Let's Encrypt certificate (plus `extra_domains` such as
  `www.example.com`). Leave the domain empty for plain HTTP by IP.
```

2. Config example (`pier.toml` block): replace the `tls = false ...` line and its surroundings in the `[deploy.production]` example (lines 298–303):

```toml
# domain = "myapp.example.com"   # optional: serves HTTPS (Let's Encrypt); absent = inherit [project].domain
# extra_domains = ["www.myapp.example.com"]   # optional: served and redirected to the domain
before_deploy = ["php artisan down"]              # runs in the app container before the new release starts
after_deploy = ["php artisan migrate --force"]    # runs in the app container after the new release is up
```

3. Replace the paragraph after the example (lines 306–315, the one starting "`[deploy.<env>]` fields: `host`, `user`, `path`, `branch`, optional `tls`, and optional `ports` overrides...") with:

```markdown
`[deploy.<env>]` fields: `host`, `user`, `path`, `branch`, optional
`domain` / `extra_domains`, and optional `ports` overrides. HTTPS is
implied by domain presence: the effective domain is
`[deploy.<env>].domain` when set, else `[project].domain`; when it is
non-empty, Caddy serves HTTPS with an automatic Let's Encrypt
certificate (and redirects HTTP to HTTPS), and the deploy health
check probes `https://<domain>/up`. When no domain is set the env
serves plain HTTP end-to-end: the deploy health check probes
`http://<host-ip>:<laravel-port>/up` directly on the deploy host IP,
so it passes before DNS or `/etc/hosts` entries point the domain at
the server. The deploy "done" URL prints the effective domain, but
falls back to the deploy host IP when the domain does not resolve
yet, so the printed URL is always usable. The old `tls = true/false`
key is removed — delete it and set (or blank) the domain instead.
```

4. New section "Custom domain & HTTPS" — insert after the Configuration section (before `### `[dev.services.<name>]` — opt-in dev sidecars``):

```markdown
### Custom domain & HTTPS

Every deploy env serves HTTPS automatically once a domain is
configured, powered by Caddy and Let's Encrypt — no certificate
management. Ownership is proven by the ACME HTTP-01 challenge: you
point the domain's A record at your server, and Caddy answers Let's
Encrypt's token on port 80. Certificates issue on the first deploy
and renew automatically inside Caddy (stored in the `caddy_data`
volume, so they survive re-deploys).

Walkthrough (Namecheap, Vercel, or any registrar):

1. **Buy the domain** (e.g. `myapp.com`) at your registrar.
2. **Find your server's public IP** — `pier status <env>` prints the
   deploy host.
3. **Create DNS A records** in your registrar's DNS settings:
   - `@` (or `myapp.com`) → your server IP
   - `www` → your server IP (optional; pair it with `extra_domains`)
   DNS changes can take a few minutes to a few hours to propagate.
4. **Set the domain in `pier.toml`:**

   ```toml
   [project]
   domain = "myapp.com"

   [deploy.production]
   # domain = "myapp.com"      # or per env — staging can use staging.myapp.com
   extra_domains = ["www.myapp.com"]
   ```

   An empty domain (`domain = ""`) means plain HTTP by IP — the
   default for fresh `pier init` projects that skip the domain
   prompt.
5. **Deploy** — `pier deploy production`. Pier verifies the domain
   resolves to the deploy host before syncing: if the A record is
   missing or points elsewhere, the deploy fails fast with
   "point an A record for myapp.com at the deploy host IP" — fix the
   DNS entry, wait for propagation, and re-deploy. Caddy fetches the
   certificate during the first `up` (the health probe retries with
   backoff, so slow issuance is absorbed).

Requirements: ports **80 and 443** must be open on the server (the
ACME HTTP-01 challenge runs on 80, HTTPS on 443), and the domain must
resolve directly to the server — Caddy cannot issue certificates for
domains proxied through a CDN (Cloudflare, Vercel edge) without
additional configuration, which pier does not set up.

Multiple domains: `extra_domains = ["www.myapp.com"]` serves
`www.myapp.com` and redirects it to the primary domain. Staging:
add a `[deploy.staging]` section with its own `domain =
"staging.myapp.com"` (same A record step for that hostname).
```

5. Search-and-replace remaining nginx references:
   - Line ~324: "sync only the deploy files (`docker-compose.prod.yml`, `.env.production`, `docker/nginx/default.conf`)" → `docker/caddy/Caddyfile`.
   - Line ~363: "after `docker compose up --wait` (...) and the nginx reload, before the health probe" → "and the caddy reload".
   - Line ~412 `.env` table note: "`APP_URL=http://localhost:8000 # dev; .env.production writes http(s)://<domain>`" → "`APP_URL=http://localhost:8000 # dev; .env.production writes https://<domain> (or http://<host>:<port> with no domain)`".
6. Troubleshooting — add before "Still stuck?":

```markdown
- **"domain ... does not resolve — point an A record"** on
  `pier deploy` — the env has a domain configured but DNS does not
  point it at the deploy host. Create the A record at your registrar
  (see [Custom domain & HTTPS](#custom-domain--https)), wait for
  propagation, and re-deploy. Servers blocking ports 80/443 cannot
  get certificates via the HTTP-01 challenge.
```

7. Manual verification checklist — add an item after the `pier deploy production` entry:

```markdown
- [ ] `pier deploy production` with a domain set — DNS preflight
  passes (A record pointing at the host), health probe GETs
  `https://<domain>/up`, and the site serves a valid Let's Encrypt
  certificate; re-deploy with the domain unset — plain HTTP by IP
  still works
```

- [ ] **Step 2: Update CHANGELOG**

Insert a new section above `## v0.0.6-beta`:

```markdown
## Unreleased

### Added

- Custom domains + HTTPS: the production webserver is now Caddy
  (`caddy:2-alpine`) with a pier-rendered `docker/caddy/Caddyfile`.
  A non-empty effective domain (`[deploy.<env>].domain`, falling
  back to `[project].domain`) enables HTTPS with automatic Let's
  Encrypt certificates; `[deploy.<env>].extra_domains` (e.g. `www`)
  are served and redirected to the primary domain. Empty domain =
  plain HTTP by IP.
- Deploy DNS preflight: when a domain is set, `pier deploy` verifies
  it resolves to the deploy host before syncing and fails fast with
  an A-record hint otherwise; the health probe then checks
  `https://<domain>/up` end to end.
- `pier init` prompts for the project domain (blank = plain HTTP by
  IP).

### Removed

- `[deploy.<env>].tls` — HTTPS is now implied by domain presence.
  Existing configs keep loading (the key is ignored); delete it and
  set the domain instead.
```

- [ ] **Step 3: Full verification**

Run:
```bash
go build ./...
go test -race ./...
golangci-lint run
```
Expected: build succeeds, all tests pass, lint clean. Then:

```bash
go build -o /tmp/pier-check ./cmd/pier
/tmp/pier-check --version
```

Expected: `pier <version>` prints normally.

- [ ] **Step 4: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: custom domain + https guide, config reference, changelog"
```

---

## Self-review notes (checked against the spec)

- Spec §1 (config model): Task 1 — `tls` removed, `Domain`/`ExtraDomains`, empty-domain valid, hostname validation. ✓
- Spec §2 (compose + Caddyfile): Task 3 — `caddy:2-alpine`, bind mount, `caddy_data`/`caddy_config`, domain-driven ports, extra_domains redir blocks, HTTP-only `:80` block. ✓
- Spec §3 (pipeline): Task 4 (sync filter + reload) and Task 5 (DNS preflight, health probe https://domain/up vs host-IP fallback, APP_URL, ResolvedURL). ✓
- Spec §4 (init prompts): Task 6 — domain prompt, blank → `domain = ""`, placeholder dropped. ✓
- Spec §5 (docs): Task 7 — config reference, migration note, custom domain guide (Namecheap/Vercel), troubleshooting, checklist. ✓
- Spec §6 (testing): covered per task; all legacy `tls`/`nginx` references updated (Tasks 1–6). ✓
- Spec §7/8 (out of scope, migration): documented in README/CHANGELOG. ✓

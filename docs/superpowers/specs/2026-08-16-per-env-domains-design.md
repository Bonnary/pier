# Per-env domains: drop [project].domain, rename extra_domains — design

Date: 2026-08-16
Status: approved

## Goal

Simplify the domain config model. Today a reader must understand three
touches points — `[project].domain` (global default), `[deploy.<env>].domain`
(per-env override), and `[deploy.<env>].extra_domains` (redirect list) — plus
the `DomainForEnv` fallback chain. Replace that with a single per-env model:
each `[deploy.<env>]` section carries its own `domain` (empty = plain HTTP by
IP) and a `redirect_domains` list of domains that 301 to it.

## Decisions (from brainstorming)

- **Drop `[project].domain`.** `[project]` keeps only `name`. There is no
  global default and no inheritance; the domain is always read from the env
  section being deployed.
- **`[deploy.<env>].domain` keeps its semantics**: the env's public domain;
  empty means Caddy serves plain HTTP by IP (no TLS).
- **Rename `extra_domains` → `redirect_domains`** (plural: it is an array).
  Semantics unchanged: each entry is served by Caddy and 301-redirects to the
  env's primary domain (apex ↔ www pairs).
- **Delete `Config.DomainForEnv`** (approach A). Every call site already has
  an env; read `cfg.Deploy[env].Domain` directly. A helper that only wraps map
  access adds indirection without meaning.
- `pier init` keeps a domain prompt, reworded to
  "Production domain (e.g. myapp.com; blank = plain HTTP by IP): " and written
  to `[deploy.production].domain` (init only scaffolds a production env).
- `pier status` (local, no env) drops its `domain:` line; domain is per-env
  now, and remote status is unchanged.

## 1. Config model (`pier.toml`)

New schema (deploy section):

```toml
[project]
name = "myapp"

[deploy.production]
host = "..."
user = "deploy"
path = "/srv/myapp"
branch = "main"
domain = "myapp.com"            # optional: HTTPS + Let's Encrypt; blank = plain HTTP by IP
redirect_domains = ["www.myapp.com"]  # optional: 301 to domain

[deploy.staging]
domain = "staging.myapp.com"    # each env carries its own domain; no inheritance
```

Changes:

- `ProjectConfig.Domain` removed from the struct. BurntSushi/toml silently
  ignores unknown keys, so an existing config that sets `[project] domain`
  still loads but the value is dropped — envs that relied on inheritance
  switch to plain HTTP. Accepted: the tool is pre-1.0; README and CHANGELOG
  call the change out.
- `DeployConfig.ExtraDomains` renamed to `DeployConfig.RedirectDomains`
  (TOML key `redirect_domains`). Old configs using `extra_domains` silently
  lose those redirects for the same reason; documented alongside.
- `Config.DomainForEnv` deleted.

Validation changes in `internal/config/parse.go`:

- Remove the `project.domain` hostname check.
- `redirect_domains` rules (unchanged semantics, against `dc.Domain` instead
  of the inherited domain): each entry a valid bare hostname, case-insensitive
  uniqueness, must not equal the env's `domain`. Error messages updated to say
  `deploy.<env>.redirect_domains` and "the domain" instead of "the primary
  domain" where the old text implied inheritance.

## 2. Code changes

All "effective domain" reads become direct per-env reads. Env is always known
at each site:

- `internal/config/config.go` — remove `ProjectConfig.Domain` and
  `DomainForEnv`; rename field + comment (`ExtraDomains` →
  `RedirectDomains`).
- `internal/config/parse.go` — validation as above.
- `internal/cli/toml.go` — `[project]` line emits only `name`; emit
  `redirect_domains` instead of `extra_domains` (already only when non-empty).
- `internal/cli/init.go` — reworded prompt; `dc.Domain = domain` on the
  `production` DeployConfig instead of `ProjectConfig.Domain`.
- `internal/cli/status.go` — drop the local `domain:` line.
- `internal/deploy/dns.go` — `checkDomainDNS` reads
  `cfg.Deploy[env].Domain`; empty ⇒ skip (unchanged behavior otherwise).
- `internal/deploy/deploy.go` — `ResolvedURL` and `HealthURL` read
  `cfg.Deploy[env].Domain` directly.
- `internal/stack/laravel/ports.go` — `WebScheme` and `WebPort` read
  `deployCfg.Domain` (WebPort already loads the env config).
- `internal/stack/laravel/prod.go` — `renderCaddyfile` uses `dc.Domain` and
  `dc.RedirectDomains`; `webserverPorts` call sites pass
  `deployCfg.Domain != ""` (prod.go:101); APP_URL block (prod.go:317) reads
  the env domain.
- `skills/pier/SKILL.md` — config table row for `[project]` becomes just
  `name`; deploy row mentions `domain` / `redirect_domains`.
- `README.md` — update the `pier.toml` example (move `domain` from
  `[project]` into `[deploy.production]`), the `[deploy.<env>]` fields
  paragraph (remove inheritance prose, rename the field), the "Custom domain
  & HTTPS" section (no `[project]` example; each env sets its own domain),
  and the features bullet.
- `CHANGELOG.md` — note the removed `[project].domain` and the
  `extra_domains` → `redirect_domains` rename under the unreleased section.

## 3. Tests

- `internal/config/parse_test.go` — `TestDomainForEnv` removed (the helper is
  gone); extra_domains
  validation tests renamed to `redirect_domains` with updated error-text
  expectations; the "must not contain the primary domain" test builds the
  env's own `Domain` instead of relying on `Project.Domain`.
- All fixtures using `ProjectConfig{Name: ..., Domain: ...}` move the domain
  into `DeployConfig.Domain` for the env under test (many files, mechanical).
  Fixtures that relied on inheritance with no env section get the env added or
  lose domain-dependent assertions.
- `internal/config/testdata/*.toml` — move `domain` into the deploy sections;
  `full-ports.toml` renames `extra_domains` → `redirect_domains`.
- `internal/cli/toml_test.go` — round-trip expectations: `[project]` has no
  `domain` line; `redirect_domains` key; no more inheritance assertions.
- `internal/cli/init_test.go` / related — prompt text expectation
  "Production domain", and the written config has
  `Deploy["production"].Domain` set with `Project.Domain` empty.
- `internal/cli/status_test.go` — no `domain:` line in local status output.
- `internal/deploy/*_test.go`, `internal/stack/laravel/*_test.go` — fixture
  updates as above; `prod_test.go` `TestRenderCaddyfileExtraDomains` renamed
  for `RedirectDomains`; error-message assertions updated where they
  referenced inheritance.
- Add a test that a config with `[project] domain = ...` still loads (unknown
  key ignored) and the envs are HTTP — pinning the documented migration
  behavior.

## 4. Verification

- `go build ./...`, `go vet ./...`, `go test ./...` all pass.
- `gofmt`/`goimports` clean (repo lint, if any).
- Grep confirms no remaining `extra_domains`, `Project.Domain`, or
  `DomainForEnv` references in code, tests, or docs.
- `pier init` in a scratch dir writes `domain` under `[deploy.production]`.
- Manual: config with per-env domains renders a Caddyfile serving
  `<env-domain>` and redirecting each `redirect_domains` entry.

# Per-Env `tls` Flag (HTTP-only production mode)

Date: 2026-08-03

## Problem

`pier deploy production` fails at the health stage: the probe GETs
`https://<domain>:443/up`, but pier renders an nginx config that only
listens on port 80 with no TLS at all. HTTPS can never succeed, so the
first deploy also fails rollback ("no previous deploy to roll back
to").

Additionally, the probe targets the public domain, which may not
resolve for placeholder domains (e.g. `test_web.example.com`), and the
pier.toml line `tls = false` under `[deploy.<env>]` is silently
ignored because `DeployConfig` has no such field.

SSL certificate provisioning with custom domains is future work.
Until it lands, production must run over plain HTTP end-to-end.

## Decisions (confirmed with user)

1. Add a per-env `tls` boolean to `[deploy.<env>]`, **default `false`
   (HTTP)**. Absent = false via Go zero value; no validation needed.
2. `tls = false` flips the whole URL story to HTTP: health probe,
   `APP_URL`, displayed URL, and the `laravel` port mapping.
3. When `tls = false`, the `webserver_http` key (the future HTTP→HTTPS
   redirect listener) is not published by default; an explicit user
   override is still honored.
4. The health probe targets the **deploy host IP** from
   `[deploy.<env>].host` instead of the public domain, so it works
   without DNS/`/etc/hosts` setup. The public domain URL is still what
   the deploy output/URL line shows.

## Design

### Config

- `internal/config/config.go`: add `TLS bool` (`toml:"tls"`) to
  `DeployConfig`.

### Scheme helpers (`internal/stack/laravel`)

The laravel stack package owns port semantics; add:

- `WebScheme(cfg config.Config, env string) string` — `"https"` when
  `cfg.Deploy[env].TLS` else `"http"`.
- `WebPort(cfg config.Config, env string) int` — effective host port
  for the `laravel` key: override from `[deploy.<env>.ports.laravel]`
  wins, else default `443` when TLS else `80`.

Both read `cfg.Deploy[env]` with the zero-value fallback used
elsewhere (deploy.go, prod.go).

### URLs

- `deploy.ResolvedURL(cfg, env)` — becomes
  `scheme://<domain>:<port>` (was hardcoded `https://`). Still the
  public URL shown in the deploy TUI and the "URL:"/"url" done event.
  Keep the explicit port in the string (`http://x:80`) for
  consistency with existing tests.
- New `deploy.HealthURL(cfg, env)` — `scheme://<host>:<port>/up`
  where `<host>` is `cfg.Deploy[env].Host`. Probes the host IP.
- `deploy.DefaultHealthConfig(domain string)` changes signature to
  `DefaultHealthConfig(cfg config.Config, env string)`; it builds the
  URL via `HealthURL`. Callers: `cli/deploy.go` (pipeline Health) and
  `cli/status.go` (status probe; the `statusHealthURL` seam now wraps
  `deploy.HealthURL`).
- `renderProdEnvExample` (`internal/stack/laravel/prod.go`):
  `APP_URL=<scheme>://<domain>`.

### Compose webserver ports

`webserverPorts(bind, override)` gains the TLS flag:

- `laravel` → container port `443` when TLS else `80`; host port via
  `ResolvePort` with default `443`/`80` respectively.
- `webserver_http` → emitted only when TLS (the HTTP→HTTPS redirect
  listener); when not TLS, emitted only if the user explicitly set
  `webserver_http` in `[deploy.<env>.ports]`.

Net effect for `tls = false` (defaults): webserver publishes just
`80:80`.

### Nginx render

Unchanged: the template already listens on 80 only. The future cert
feature (443 server block, cert mount, custom domain) is out of
scope; `tls = true` today renders https URLs and a 443 mapping with
nothing serving it, so the health stage fails until that feature
lands. Documented in README/CHANGELOG.

### Status command

`pier status <env>` shows `health: OK/DOWN (<health URL>)` — now the
host-IP URL, e.g. `health: OK (http://192.168.122.126:80/up)`.

## Edge cases

- `laravel = 0` override (don't expose): URL falls back to the TLS
  default port (`443`/`80`), matching today's behavior of falling
  back to `443` when unresolvable.
- Explicit `webserver_http` override with `tls = false`: honored;
  publish `<host>:80` → container 80. If it collides with the
  `laravel` port, `docker compose up` fails with a clear port-already-
  allocated error (user error).

## Tests

- `deploy_unit_test.go`: `ResolvedURL` default now
  `http://myapp.example.com:80`; add TLS=true → `https://...:443`;
  keep override case (`http://...:8383` for the tls=false config, plus
  a TLS=true override case).
- New `HealthURL` test: `http://<host>:80/up` default, `https` and
  override variants.
- `internal/config` parse test: `tls = true` round-trips into
  `Deploy[env].TLS`.
- `internal/stack/laravel` prod render tests: webserver ports for
  tls=false (`80:80` only) and tls=true (`443:443`, `80:80`);
  `APP_URL` scheme variants.
- `cli/status_test.go`: existing seam-based tests keep passing (seam
  body changes to `HealthURL`).

## Docs

- README pier.toml reference: document `[deploy.<env>].tls` (default
  false; `https` requires the upcoming cert feature) and the health
  probe now targeting the host IP.
- CHANGELOG entry.

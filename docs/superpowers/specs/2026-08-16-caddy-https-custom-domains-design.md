# Caddy + HTTPS + custom domains — design

Date: 2026-08-16
Status: approved

## Goal

Replace the production nginx webserver with Caddy so deployed envs can
serve HTTPS with automatic Let's Encrypt certificates, driven by the
domain(s) the user configures for staging or production. Users who buy
a domain at Namecheap/Vercel/etc. point an A record at their server,
set the domain in `pier.toml`, and get working HTTPS on the next
deploy — no manual certificate management.

## Decisions (from brainstorming)

- HTTPS is **implied by domain presence**, not by a `tls` flag.
  `domain = ""` == the old `tls = false` (plain HTTP by IP).
- Per-env domain override (`[deploy.<env>].domain`) so staging and
  production can use different domains.
- Extra domains list (`[deploy.<env>].extra_domains`) for apex + www.
- When a domain is configured but its DNS does not point at the deploy
  host, `pier deploy` **fails fast** with an actionable hint.
- Caddy is configured with a **pier-rendered Caddyfile**, bind-mounted
  into `caddy:2-alpine`, reloaded after each deploy (same pattern as
  the nginx conf today).
- `pier init` prompts for the domain (no separate TLS prompt).
- README gains a "custom domain" guide covering Namecheap/Vercel DNS
  setup.

## 1. Config model (`pier.toml`)

- Remove `tls` from `DeployConfig`. BurntSushi/toml ignores unknown
  keys, so existing configs keep loading; users who set `tls = true`
  now get HTTPS because their domain is set. README documents the
  removal.
- `ProjectConfig.Domain` is no longer required (may be empty). It
  remains the fallback domain for all deploy envs.
- New `DeployConfig.Domain string` — per-env domain. Empty string
  inherits `[project].domain`.
- New `DeployConfig.ExtraDomains []string` — additional domains
  served by Caddy, redirected to the primary domain.
- Effective domain for an env (helper on `config`):
  `[deploy.<env>].domain` if non-empty, else `[project].domain`.
  Empty effective domain ⇒ HTTP-only deployment.

Validation additions in `config.Validate`:
- `extra_domains` entries must be non-empty, unique, not equal to the
  effective domain, and not contain a scheme/path (a bare hostname).
- No other new required fields.

## 2. Compose + Caddyfile rendering

`internal/stack/laravel/prod.go` changes:

- `webserver` service:
  - `image: caddy:2-alpine`
  - volumes:
    - `./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro`
    - `caddy_data:/data`
    - `caddy_config:/config`
  - named volumes `caddy_data` and `caddy_config` (local driver) so
    certs and state persist across deploys and container recreates.
  - ports:
    - domain set: `443:443` (port key `laravel`) + `80:80` (port key
      `webserver_http`; the HTTP→HTTPS redirect listener)
    - no domain: `80:80` (port key `laravel`)
    - existing `[deploy.<env>.ports]` override semantics unchanged
      (0 = don't expose).
- Render `docker/caddy/Caddyfile` instead of `docker/nginx/default.conf`
  (the nginx file is no longer generated):
  - domain set:
    ```
    example.com {
        encode gzip
        reverse_proxy app:80
    }
    www.example.com {
        redir https://example.com{uri}
    }
    ```
    (one `redir` block per `extra_domains` entry)
  - no domain:
    ```
    :80 {
        encode gzip
        reverse_proxy app:80
    }
    ```
- `deploy` sync filter ships `docker/caddy/Caddyfile` instead of
  `docker/nginx/default.conf` (`internal/deploy/syncfilter.go`,
  `deployFilesOnly`, and the render-phase file list).

`internal/deploy/up.go`: the post-up reload becomes
`docker compose exec -T webserver caddy reload --config /etc/caddy/Caddyfile || true`
(kept `|| true` for the same reason as today: a freshly created
container already loaded the current config).

## 3. Deploy pipeline changes

- **Preflight DNS check** (only when the env has an effective domain):
  resolve the domain; if none of its IPs match any of the deploy
  host's IPs, fail preflight with a hint naming the domain and the
  host IP, e.g.:
  `point an A record for <domain> at <host-ip> (and allow ports 80/443), then re-deploy`.
  Resolution helper compares the sets of resolved IPs of the domain
  and `[deploy.<env>].host` (the host may itself be a domain).
- **Health probe** (`internal/deploy/health.go`, `HealthURL`):
  - domain set → `https://<domain>/up` with normal TLS verification
    (true end-to-end check; exercises the real certificate).
  - no domain → `http://<host-ip>:<laravel-port>/up` (today's
    behavior).
- **`APP_URL`** in `.env.production`:
  - domain set → `<scheme>://<effective-domain>` (https).
  - no domain → `http://<host>:<laravel-port>`.
- `WebScheme`/`WebPort` (`internal/stack/laravel/ports.go`): base both
  on effective-domain presence instead of `DeployConfig.TLS`.
- `ResolvedURL` (the deploy "done" URL) keeps its existing
  domain-resolves-else-host-IP fallback.

Caddy certificate timing: on a first deploy with a domain, Caddy
fetches the certificate during `up`; the health probe already retries
with backoff, so a slow issuance is absorbed. Certs auto-renew inside
Caddy; the `caddy_data` volume keeps them across deploys.

## 4. Init prompts

`pier init` already asks for host/user/path/branch. Add one prompt:
- "Project domain (e.g. myapp.com; blank = plain HTTP by IP, enter to skip): "
  - answer written to `[project].domain`
  - blank/skip ⇒ `[project].domain = ""` (HTTP by IP). The old
    `<name>.example.com` placeholder is dropped: a non-empty domain
    now means HTTPS, so shipping a placeholder would make the DNS
    preflight fail every fresh project's deploy.
- No TLS prompt: domain presence is the TLS switch.
- Staging and additional envs are added by hand in `pier.toml`
  (`[deploy.staging]` + `domain = "staging.myapp.com"`) — no new CLI
  machinery in this change.

## 5. Documentation

README updates:
- Config reference: `[project].domain` optional, new
  `[deploy.<env>].domain` + `extra_domains`, `tls` removed with a
  migration note (delete the key; set the domain instead).
- New "Custom domain & HTTPS" guide:
  1. Buy a domain (Namecheap/Vercel examples).
  2. Find your server's public IP (`pier status <env>`).
  3. At the registrar/DNS provider, create an A record for the domain
     (and optionally `www`) pointing at the server IP.
  4. Set `domain = "..."` (and `extra_domains`) in `pier.toml`.
  5. Run `pier deploy <env>`; pier verifies DNS and Caddy obtains the
     Let's Encrypt certificate automatically.
  6. Note that ports 80/443 must be open (HTTP-01 challenge).
- Troubleshooting: DNS-not-pointed deploy failure and how to fix it.

## 6. Testing

- Config: parse tests for the new fields; validation errors for bad
  `extra_domains`; empty-domain configs validate.
- laravel stack: Caddyfile rendering (domain, extra_domains, HTTP-only
  fallback), webserver service uses `caddy:2-alpine` with correct
  volumes/ports, no `docker/nginx/default.conf` emitted.
- deploy: sync filter includes the Caddyfile; `up.go` runs the caddy
  reload; DNS preflight check fails with hint when domain does not
  resolve to host, passes when it does; `HealthURL` picks https+domain
  vs http+host-IP.
- Update every existing test referencing `tls`, `nginx`, or
  `docker/nginx/default.conf`.

## 7. Out of scope

- DNS-01 challenges / wildcard certs (needs DNS provider API keys).
- ACME email / Let's Encrypt account configuration (Caddy defaults are
  fine).
- Multiple primary domains per env beyond `extra_domains` redirects.
- CDN/proxy-aware HTTPS (e.g. serving behind Cloudflare).

## 8. Migration notes

- `tls = true` + domain set: behavior unchanged (HTTPS now actually
  works).
- `tls = false` + domain set: site now serves HTTPS (Caddy redirects
  HTTP). If HTTP-only is truly required, remove the domain.
- `tls` key left in `pier.toml` is silently ignored by the TOML
  decoder; README notes it can be deleted.
- Existing projects initialized with the `<name>.example.com`
  placeholder domain: that is now treated as a real domain (HTTPS +
  DNS preflight). README tells these users to either set their real
  domain or blank it out for HTTP-by-IP.
- Old `docker/nginx/default.conf` files on deploy hosts are left
  behind after the first Caddy deploy (harmless); the compose file no
  longer references them.

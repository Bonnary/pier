# Pier — Dev + Per-Env Host Port Overrides — Design

**Date:** 2026-07-29
**Status:** Approved (brainstorming), pending spec review

## Problem

`pier`'s generated `docker-compose.yml` for the always-on `laravel.test`
service has no `ports:` field. When a user runs `php artisan dev` inside
the container, Laravel's server binds to `0.0.0.0:8000` and Vite binds to
`0.0.0.0:5173` — both inside the container only. The host browser can't
reach either URL because nothing in compose maps them to the host.

See `internal/stack/laravel/dev.go:104-117`:

```go
"laravel.test": {
    Build:       &composeBuild{...},
    Image:       cfg.Project.Name + "/test:latest",
    ExtraHosts:  []string{"host.docker.internal:host-gateway"},
    Volumes:     []string{"./:/var/www/html"},
    Environment: devEnvForServices(svcSet),
    Networks:    []string{"pier"},
    // <-- no Ports!
},
```

The Dockerfile at `internal/stack/laravel/runtimes/8.3/Dockerfile:100`
even has `EXPOSE 80/tcp` (the supervisord default), but `EXPOSE` is
documentation only — Compose ignores it without a `ports:` mapping.

The sidecar services (`mysql`, `postgres`, `redis`, `mailpit`, etc.) all
have hardcoded `Ports` in `internal/stack/laravel/services.go` (e.g.
`3306:3306`, `6379:6379`, `1025:1025, 8025:8025`) and bind to `0.0.0.0`
on the host. If a user already has a service on any of those ports
(another `mysql` server, a local `redis-server`, etc.), `docker compose
up` fails with Docker's own opaque error.

`file` changes already sync host → container via the `./:/var/www/html`
bind mount — that part of the workflow works. The missing piece is
visibility: the user can't see the app from their host browser.

The user wants two things:

1. `laravel.test` ports (8000, 5173) exposed so the browser can reach
   them.
2. A way to override the host port for **any** pier-owned service
   (`laravel.test`, `mysql`, `redis`, ...) in both dev and per-deploy
   environments, so a port collision on the host can be resolved by
   editing `pier.toml` instead of hand-editing `docker-compose.yml`
   (which `pier dev` would then clobber on the next run).

## Goals

1. `pier dev` brings up a stack where `http://localhost:8000` and
   `http://localhost:5173` actually work without the user editing
   `docker-compose.yml`.
2. Every pier-owned port is overridable through `pier.toml`, in both
   dev and per-deploy-env sections.
3. A pre-flight port probe in `pier dev` catches host-port collisions
   *before* Docker starts anything, with an actionable error pointing
   the user at the `pier.toml` key to change.
4. Bind pier-owned dev ports to `127.0.0.1` (loopback only) by default,
   to avoid LAN exposure on shared networks.
5. Keep user-defined `[dev.services.<name>]` working with its existing
   `ports = [...]` field — no migration, no rename.
6. `pier dev` prints the resolved URLs after a successful start, so the
   user doesn't have to remember which port maps to what.

## Non-goals

- Auto-picking free host ports. The URL printed by `php artisan dev`
  (8000) is hardcoded inside the container, so an auto-picked host port
  would break that contract. Override is the only path.
- Changing container ports. The container ports are fixed by the
  service design (8000 for `php artisan dev`, 6379 for redis, etc.).
  Only the **host** side of the `<host>:<container>` binding is
  overridable.
- Remote port probing in `pier deploy`. The remote host owns its port
  allocation; SSH-probing it during deploy is out of scope.
- Renaming or restructuring the existing
  `[dev.services.<name>].ports` mechanism. User-defined dev services
  keep their current shape verbatim.
- Per-port protocol (`tcp`/`udp`). All pier-owned ports are TCP.

## Design

### 1. `pier.toml` shape (additions)

```toml
# pier.toml — full example

[project]
name = "myapp"
domain = "myapp.example.com"

[stack]
type = "laravel"
php = "8.3"
node = "22"
services = ["redis", "mailpit", "s3"]

# Dev: every pier-owned port shown by default. Override any value;
# set to 0 to not expose that port to the host.
[dev.ports]
laravel       = 8000
vite          = 5173
mysql         = 3306
postgres      = 5432
redis         = 6379
meilisearch   = 7700
mailpit_smtp  = 1025
mailpit_ui    = 8025
s3_api        = 8333
s3_filer      = 8888
s3_master     = 9333

# Per-deploy-env: only the keys the user writes are applied; everything
# else falls back to the prod default for that env. This example keeps
# the standard ports but remaps the visible HTTPS port to 8383.
[deploy.production]
host  = "prod.example.com"
user  = "deploy"
path  = "/srv/myapp"
branch = "main"

[deploy.production.ports]
laravel = 8383

# Staging can have its own override independently of production.
[deploy.staging]
host  = "stage.example.com"
user  = "deploy"
path  = "/srv/myapp-stage"
branch = "develop"

[deploy.staging.ports]
laravel = 8443
```

### 2. Per-env key allowlist and defaults

| Key            | dev default | prod/staging default | Meaning |
|----------------|-------------|----------------------|---------|
| `laravel`      | 8000        | 443                  | dev: `laravel.test:8000`. prod: webserver HTTPS (primary visible port) |
| `vite`         | 5173        | (rejected)           | dev only; no Vite in prod |
| `webserver_http` | (rejected) | 80                   | prod only; HTTP→HTTPS redirect |
| `mysql`        | 3306        | 3306                 | same in both |
| `postgres`     | 5432        | 5432                 | same in both |
| `redis`        | 6379        | 6379                 | same in both |
| `meilisearch`  | 7700        | 7700                 | same in both |
| `mailpit_smtp` | 1025        | (rejected)           | dev only (DevOnly service) |
| `mailpit_ui`   | 8025        | (rejected)           | dev only |
| `s3_api`       | 8333        | 8333                 | same in both |
| `s3_filer`     | 8888        | 8888                 | same in both |
| `s3_master`    | 9333        | 9333                 | same in both |

`vite` in `[deploy.<env>.ports]` is a parse error. `mailpit_*` in
`[deploy.<env>.ports]` is a parse error. The reverse — `webserver_http`
in `[dev.ports]` — is also a parse error.

### 3. Bind address

- dev: `127.0.0.1:<host>:<container>` (loopback only, no LAN exposure).
- prod/staging: `<host>:<container>` (binds `0.0.0.0`; relies on the
  host firewall).

### 4. Config types

File: `internal/config/config.go`

```go
type DevConfig struct {
    Services map[string]DevService `toml:"services"`
    Ports    map[string]int       `toml:"ports"`
}

type DeployConfig struct {
    Host   string         `toml:"host"`
    User   string         `toml:"user"`
    Path   string         `toml:"path"`
    Branch string         `toml:"branch"`
    Ports  map[string]int `toml:"ports"`
}
```

`nil` map = no overrides; renderer falls back to defaults.

### 5. Config validation

File: `internal/config/parse.go`

- Port value must be in `0..65535`. Negative or out-of-range = parse
  error pointing at the offending key.
- Key must be in the allowlist for the env where it appears.
  - `[dev.ports]` rejects: `webserver_http`.
  - `[deploy.<env>.ports]` rejects: `vite`, `mailpit_smtp`,
    `mailpit_ui`.
- `webserver_http` is allowed in `[deploy.<env>.ports]` only.
- Error message format:
  `error[config]: [dev.ports] has unknown key "vhost" (valid: laravel, vite, ...)` or
  `error[config]: [deploy.production.ports] has dev-only key "vite" (remove it; this port does not exist in production)`.

### 6. Render

New helper file: `internal/stack/laravel/ports.go`

```go
// ResolvePort returns the host port to bind for a given key, given the
// user's override map and the env's default map. ok=false means the
// port is set to 0 in the override (user wants it not exposed) — the
// renderer omits it from the compose Ports slice entirely.
func ResolvePort(key string, override, defaults map[string]int) (host int, ok bool)

// BindAddr returns the bind-address prefix for a given env ("127.0.0.1"
// for dev, "" for prod/staging).
func BindAddr(env string) string
```

`Service` struct in `internal/stack/laravel/services.go` gains a
`PortKeys []string` field listing the keys (in order) for each port the
service exposes. Multi-port services list multiple keys:

```go
"mailpit": {
    ...
    Ports:    []string{"1025", "8025"},        // container ports
    PortKeys: []string{"mailpit_smtp", "mailpit_ui"},
},
"redis": {
    ...
    Ports:    []string{"6379"},
    PortKeys: []string{"redis"},
},
"s3": {
    ...
    Ports:    []string{"8333", "8888", "9333"},
    PortKeys: []string{"s3_api", "s3_filer", "s3_master"},
},
"laravel.test": (handled directly in renderDevCompose, not via Service registry)
    -> PortKeys: []string{"laravel", "vite"}, Container: []int{8000, 5173}
```

`renderDevCompose` in `internal/stack/laravel/dev.go`:

- For `laravel.test`: emit `Ports: [<bind>8000:8000, <bind>5173:5173]`
  if both keys resolve to a non-zero value. If a key resolves to 0
  (user opted out), that entry is omitted.
- For each registered sidecar in `cfg.Stack.Services`: build the
  `Ports` slice by iterating `Service.PortKeys`, calling `ResolvePort`
  for each, and emitting `<bind><host>:<container>` (or omitting the
  entry when `ok=false`).

`renderProdCompose` in `internal/stack/laravel/prod.go`:

- For the `webserver` service: emit `Ports: [<bind><laravel>:443,
  <bind><webserver_http>:80]` using the env's override map
  (`cfg.Deploy[env].Ports`).
- For each sidecar: same shape as dev, but using the prod defaults
  map and the env's `Deploy.Ports` for overrides.
- `laravel.test` does not exist in prod; the prod app service is named
  `app` and has no host ports (it talks to `webserver` over the pier
  network on PHP-FPM port 9000).

User-defined `[dev.services.<name>]` rendering is **unchanged** — the
existing `DevService.Ports []string` field is still used verbatim.
Pre-flight probing (next section) DOES scan these ports for
conflicts, but their rendering is not touched.

### 7. Pre-flight port probe (dev only)

New package: `internal/portcheck`

```go
// Probe tries a 1-second TCP connect to each port on 127.0.0.1. It
// returns a map of taken ports to a "pid (process name)" string. Ports
// that connect successfully (i.e. something is already listening) are
// reported as taken; ports that fail to connect (nothing listening, or
// the kernel sends a TCP RST) are reported as free.
func Probe(ctx context.Context, ports []int) (map[int]string, error)
```

`Probe` uses `net.DialTimeout("tcp", "127.0.0.1:<port>", 1*time.Second)`.
On Linux, also `/proc/<pid>/comm` lookup is performed to attribute the
listening process: walk `/proc/net/tcp`, find the inode, then walk
`/proc/*/fd/*` to find which pid owns the socket, and read its `comm`.
On macOS/Windows, the pid/process-name attribution is best-effort and
may be omitted; the port number itself is the actionable signal.

In `internal/cli/dev.go`, after the smart-merge write and before
`c.Build` / `c.Up`:

```go
hostPorts := laravelpkg.CollectHostPorts(merged, cfg) // dev + user-defined
taken, err := portcheck.Probe(ctx, hostPorts)
if err != nil { return err }
if len(taken) > 0 {
    for port, who := range taken {
        if who != "" {
            fmt.Fprintf(stderr, "pier: port %d is in use by %s\n", port, who)
        } else {
            fmt.Fprintf(stderr, "pier: port %d is already in use\n", port)
        }
    }
    fmt.Fprintln(stderr, "hint: edit [dev.ports] in pier.toml to remap")
    return cli.PortInUseError(taken) // new ExitError, Kind: KindUser, Code: 6
}
```

`CollectHostPorts` is a new helper in `internal/stack/laravel/ports.go`
that walks the rendered compose AST and returns the host-side of every
`<bind><host>:<container>` binding, plus every `DevService.Ports` entry
(user-defined).

No probe in `pier deploy`. The remote host owns its port allocation;
pier would have to SSH-probe it, which is out of scope. The
`docker-compose.prod.yml` is rendered correctly from `pier.toml`; if
the remote port is taken, Docker's own error surfaces.

### 8. `pier dev` post-Up output

After successful `c.Up` in `internal/cli/dev.go`, print a "ready"
block listing the resolved URLs for every pier-owned service the user
enabled in `[stack].services`. Only list entries where the override is
non-zero (skip opted-out ports).

Format:

```
pier dev: ready
  App:    http://127.0.0.1:8000
  Vite:   http://127.0.0.1:5173
  MySQL:  127.0.0.1:3306
  Redis:  127.0.0.1:6379
  Mailpit UI: http://127.0.0.1:8025
  Mailpit SMTP: 127.0.0.1:1025
  S3:     127.0.0.1:8333
```

User-defined `[dev.services.<name>]` URLs are **not** printed (the keys
are user-chosen and pier doesn't know which are HTTP).

### 9. `pier deploy` end-of-run summary

The deploy TUI's "Done" state already shows the health-check result.
Add one line with the resolved URL:

```
URL: https://<domain>:<laravel-port>
```

Where `<laravel-port>` is `cfg.Deploy[env].Ports["laravel"]` if set,
else `443`. The line is informational; no probe. Same line in
`--json` mode as a `"url": "..."` field on the final event.

### 10. Tests

File: `internal/stack/laravel/dev_test.go`

- Update golden file `testdata/golden/compose-no-services.yml`: the
  `laravel.test` block now has a `ports:` field.
- Update golden file `testdata/golden/compose-with-services.yml`:
  same.
- `TestRenderDevPortsOverride`: pier.toml with
  `[dev.ports] mysql = 3307 redis = 6380` → mysql service has
  `127.0.0.1:3307:3306`, redis has `127.0.0.1:6380:6379`.
- `TestRenderDevPortsZero`: `[dev.ports] mailpit_ui = 0` → mailpit
  service has only `1025:1025` (port 8025 omitted).
- `TestRenderDevPortsFullDefault`: no `[dev.ports]` block → defaults
  applied; laravel.test has both `8000:8000` and `5173:5173`.

File: `internal/stack/laravel/prod_test.go`

- New golden `testdata/golden/compose-prod-ports-override.yml`:
  pier.toml with
  `[deploy.production.ports] laravel = 8383` → webserver has
  `8383:443` and `80:80`, sidecar ports at default.
- `TestRenderProdPortsPartial`: only `laravel` overridden → other
  ports at prod default. Confirms the "only the keys the user writes
  are applied" behavior.
- `TestRenderProdRejectsVite`: `vite = 5173` in
  `[deploy.production.ports]` → parse error from config validation,
  surfaced before render.

File: `internal/config/parse_test.go`

- `TestConfigValidation_PortRange`: `-1`, `70000` → error.
- `TestConfigValidation_KeyInDev`: `webserver_http = 80` in
  `[dev.ports]` → error.
- `TestConfigValidation_KeyInDeploy`: `vite = 5173` in
  `[deploy.production.ports]` → error; `mailpit_smtp = 1025` in
  `[deploy.production.ports]` → error; `webserver_http = 8080` in
  `[deploy.production.ports]` → ok.
- `TestConfigValidation_DuplicateKeys`: TOML natively rejects
  duplicates, but a defensive check returns the same error.

File: `internal/portcheck/portcheck_test.go` (new)

- `TestProbe_FreePort`: listen on a random port, close, probe →
  empty taken map.
- `TestProbe_TakenPort`: `net.Listen("tcp", "127.0.0.1:0")` to grab
  a free port, leave it open, probe that port → taken map has the
  port (process-name attribution is best-effort and may be empty on
  non-Linux).
- `TestProbe_Multiple`: mix of taken and free, verify only taken
  reported.

File: `internal/cli/dev_test.go`

- `TestDev_PortInUse_Exit6`: arrange a listener on port 8000, run
  `pier dev` with that port in the override, assert exit code 6 and
  stderr contains `port 8000 is in use`.

### 11. File-level change list

1. `internal/config/config.go` — add `DevConfig.Ports`,
   `DeployConfig.Ports` (both `map[string]int`).
2. `internal/config/parse.go` — extend `Validate()` with port range
   and key-allowlist checks; add `devPortKeys` and `prodPortKeys`
   sets; add `errInvalidPortRange`, `errUnknownPortKey` sentinels.
3. `internal/config/parse_test.go` — tests for the new validation.
4. `internal/stack/laravel/ports.go` (new) — `ResolvePort`,
   `BindAddr`, `CollectHostPorts`, the dev/prod default maps.
5. `internal/stack/laravel/services.go` — add `PortKeys []string` to
   `Service`; populate it for every service that has `Ports`.
6. `internal/stack/laravel/services_test.go` — add a test that walks
   the registry and asserts `len(PortKeys) == len(Ports)` for every
   service.
7. `internal/stack/laravel/dev.go` — emit `Ports` on `laravel.test`
   using the override; emit `Ports` on each sidecar using
   `ResolvePort` per key.
8. `internal/stack/laravel/prod.go` — emit `Ports` on `webserver`
   using the env override; emit `Ports` on each sidecar using the prod
   defaults + env override.
9. `internal/stack/laravel/dev_test.go` — golden updates; new port
   override tests.
10. `internal/stack/laravel/prod_test.go` — new golden; new port
    override tests.
11. `internal/stack/laravel/testdata/golden/compose-no-services.yml`
    — regenerate with `-update`.
12. `internal/stack/laravel/testdata/golden/compose-with-services.yml`
    — regenerate with `-update`.
13. `internal/stack/laravel/testdata/golden/compose-prod-ports-override.yml`
    (new) — golden for the partial-override case.
14. `internal/portcheck/portcheck.go` (new) — `Probe`.
15. `internal/portcheck/portcheck_test.go` (new) — tests.
16. `internal/cli/dev.go` — call `portcheck.Probe` between merge and
    `c.Build`; print the "ready" block after successful `c.Up`; new
    `PortInUseError` exit-error constructor.
17. `internal/cli/dev_test.go` — `TestDev_PortInUse_Exit6`.
18. `internal/deploy/deploy.go` (or wherever the TUI final-state is
    emitted) — append the resolved URL line to the "Done" block; add
    `"url"` to the JSON event in `--json` mode.
19. `docs/superpowers/specs/2026-07-26-pier-design.md` — append the
    new `[dev.ports]` and `[deploy.<env>.ports]` sections under the
    `pier.toml` shape; add the per-env key/defaults table.
20. `README.md` — add `[dev.ports]` and `[deploy.<env>.ports]`
    examples; add "port N in use — edit [dev.ports] in pier.toml" to
    the troubleshooting section; add exit code 6 to the
    `pier status` / troubleshooting blurb.

### 12. What does NOT change

- User-defined `[dev.services.<name>]` keeps its existing
  `ports = [...]` mechanism. No new field, no migration, no rename.
- The smart-merge logic in `internal/stack/laravel/merge.go` is
  unchanged: pier still owns the `laravel.test` block (and all
  pier-owned sidecar blocks), and the new `ports:` is rendered fresh
  on every `pier dev` / `pier deploy`.
- The Dockerfile's `EXPOSE 80/tcp` stays. The supervisord default
  command (`artisan serve --port=80`) is still set but pier does not
  expose 80 to the host; the assumption is users run `php artisan
  dev` (Laravel 11+) which binds 8000.
- Production nginx config (`renderNginx` in
  `internal/stack/laravel/prod.go`): unchanged. The
  `fastcgi_pass app:9000` line is still the only thing the webserver
  talks to; only the listen ports change.
- Remote deploy health-check (`https://<domain>/up`): unchanged in
  shape. The URL uses the resolved `laravel` port (default 443) so
  the health-check is consistent with the published URL.
- `internal/stack/laravel/merge.go` ownership rules: unchanged. The
  new `ports:` is part of pier's owned render, not a user-overridable
  field in `docker-compose.yml`.
- `internal/stack/laravel/testdata/golden/merge/`: unchanged. None of
  the existing merge fixtures are affected by this work.

## Open questions

None. All resolved during brainstorming:

- Scope: All pier-owned services, dev + per-deploy-env.
- Behavior on conflict: pre-flight probe with clear error, exit code
  6 (Kind: KindUser).
- Bind address: `127.0.0.1` in dev, `0.0.0.0` in prod.
- Shape: flat `map[string]int` table in both `[dev.ports]` and
  `[deploy.<env>.ports]`.
- `laravel` in prod = webserver HTTPS port (default 443).
- `vite` invalid in prod; `mailpit_*` invalid in prod;
  `webserver_http` invalid in dev.
- `laravel.test` exposes 5173 (Vite) + 8000 (`php artisan dev`),
  bound to `127.0.0.1`. Port 80 (supervisord default) is NOT
  exposed; the assumption is users run `php artisan dev`.
- User-defined `[dev.services.<name>]` mechanism untouched.

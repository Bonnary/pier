# Pier — Design Spec

**Date:** 2026-07-26
**Status:** Revised 2026-07-27 (post-brainstorm: service-add flow, custom runtime, pier shell/exec) — awaiting user review

## Goal

`pier` is a personal, cross-platform CLI that turns a Laravel project into a fully provisioned dev + production Docker stack with one-command deploys, health checks, and automatic rollback.

## Users / use cases

- **Primary:** a single developer (the author) who maintains several Laravel projects and wants a consistent, scripted path from "I have a Laravel repo" to "it's running in production on a VPS, and the last bad deploy rolled itself back."
- **Out of scope for v1:** teams, public distribution, non-Laravel stacks. The architecture leaves room for both, but v1 only ships Laravel and is not packaged for end users.

## Out of scope (v1)

- Multiple stacks (Node, Python, Go, Rails) — architecture is ready, code is not.
- Multiple environments per repo at generation time — v1's generated `pier.toml` ships with `[deploy.production]` only. `pier deploy staging` is supported by the engine; the user adds `[deploy.staging]` by hand (or via a future `pier config add-env`).
- System-tray / menu-bar UI. The TUI exists only for the deploy flow.
- Cloud-provider integrations (AWS, DO, Hetzner APIs). Deploy is SSH-only.
- Secret management integrations (1Password, Vault). `.env.production` is read locally; the user is responsible for keeping it out of git.
- Auto-scaling, multi-server, blue/green, canary. One host per environment.
- `pier share` (Expose-based tunnel for sharing a local site publicly) — out of scope for v1. Users wanting this can run Expose directly.
- `pier open` (host `xdg-open` / `open` browser launch) — out of scope for v1. One-line shell command; not worth a subcommand.
- Per-tool command wrappers (`pier artisan`, `pier composer`, `pier tinker`, `pier test`, `pier mysql`, `pier psql`, `pier redis-cli`, etc.) — not in v1. All of these are covered by `pier shell` (interactive bash in the app container) and `pier exec` (one-off command). The `sail` bash script is the proof this category is high-maintenance and low-value; pier replaces it with two commands.
- Agent env forwarding into containers (`AI_AGENT`, `CLAUDECODE`, `OPENCODE`, `CURSOR_AGENT`, etc.) — not in v1. The user sets env in their shell before invoking `pier exec` or `pier shell`. Sail's 21-vendor hard-coded list is not a good default. If a real need surfaces (e.g. a coding agent shells out to `pier exec` and needs to be identified inside the container), revisit as a `--forward-env-from-host` flag.

## Constraints

- **Platforms:** macOS, Linux, Windows. Distributed as a single static binary per platform.
- **Language:** Go. Chosen for single-binary cross-compile, mature SSH/YAML/TUI ecosystem, and natural fit for system tooling.
- **Docker:** assumes Docker + the `docker compose` plugin on the remote. Docker Desktop on dev machines; Docker Engine on the VPS.
- **No background daemon.** `pier` is a one-shot CLI. "Background service" means `docker compose up -d` doing the lifting; `pier` itself doesn't run between commands.
- **No telemetry, no network calls beyond SSH + Docker CLI.**
- **No Composer, no network, no external package install during `pier init`.** pier is fully self-contained. The `laravel.test` Dockerfile is owned by pier, forked from Laravel Sail's runtime Dockerfiles and kept in sync manually — pier does not depend on the `laravel/sail` Composer package, does not run `sail:install`, and does not touch `vendor/`.

## Proposed approach

### 1. Architecture and project layout

Single Go binary, `pier`, with a `Stack` interface so other frameworks can be added as self-contained modules later. v1 ships one implementation: Laravel. The Laravel module owns its own Dockerfile templates (forked from Laravel Sail's runtime images, kept in sync manually inside `pier`'s repo); pier does not depend on the `laravel/sail` Composer package.

```
Stack interface (internal/stack/stack.go)
    Name() string
    Detect(projectPath string) bool
    DefaultConfig() Config
    GenerateDevCompose(cfg Config) Files
    GenerateProdFiles(cfg Config) Files
    RequiredDirs() []string
```

**Project layout**

```
pier/
├── cmd/pier/main.go
├── internal/
│   ├── cli/                  // cobra commands: init, dev, deploy, status, rollback,
│   │                            service add/remove, shell, exec, stop
│   ├── stack/
│   │   ├── stack.go          // interface + registry
│   │   └── laravel/          // v1 implementation
│   │       ├── detect.go
│   │       ├── defaults.go
│   │       ├── dev.go
│   │       ├── prod.go
│   │       ├── services.go
│   │       ├── merge.go      // smart-merge logic for docker-compose.yml
│   │       └── runtimes/     // pier-owned runtime Dockerfiles per PHP version
│   │           ├── 8.2/Dockerfile
│   │           ├── 8.2/php.ini
│   │           ├── 8.2/supervisord.conf
│   │           ├── 8.3/…
│   │           ├── 8.4/…
│   │           └── 8.5/…
│   ├── compose/              // YAML generation, smart-merge with existing
│   ├── deploy/               // SSH, rsync-over-ssh, health checks, rollback
│   ├── tui/                  // Bubble Tea screens for deploy progress
│   ├── config/               // pier.toml parser
│   └── docker/               // thin wrapper around `docker compose` CLI;
│                                pier shell / pier exec / pier dev / pier stop
│                                all funnel through here
├── go.mod
└── README.md
```

**Boundary rules**

- `cli` never calls Docker directly; it goes through `docker` or `deploy`.
- `stack/laravel` never imports SSH or Docker; it returns `Files` and lets the caller write/exec them.
- `deploy` never knows about Laravel; it just rsyncs files, runs commands, and probes.

**Key libraries**

- `github.com/spf13/cobra` — CLI
- `github.com/charmbracelet/bubbletea` (+ `lipgloss`, `bubbles`) — TUI
- `github.com/BurntSushi/toml` — `pier.toml` parser
- `golang.org/x/crypto/ssh` — SSH client
- `gopkg.in/yaml.v3` — compose YAML
- `os/exec` for `docker compose` invocation

### 2. CLI command surface

| Command | What it does |
|---|---|
| `pier init [path]` | Detects Laravel. Prompts for PHP version, Node version, optional services. Writes `pier.toml`, generates `docker-compose.yml` (smart-merge into any existing file), `docker-compose.prod.yml`, the pier-owned `docker/<php-version>/` runtime, nginx config, and `.env.production.example`. `--devcontainer` flag additionally generates `.devcontainer/devcontainer.json`. No Composer, no network, no `sail:install` — pure file writes. |
| `pier dev` | Smart-merges `docker-compose.yml` from current `pier.toml` (preserves user-authored services and unknown keys with a one-time warn-and-confirm), runs `docker compose up -d`, then shows a status table. |
| `pier stop` | `docker compose down`. Pairs with `pier dev`. Containers removed, default network removed, but named volumes preserved (Postgres data, Redis data, S3 data survive). |
| `pier shell` | Interactive bash in the `laravel.test` container. `docker compose exec -it -u <app_user> laravel.test /bin/bash`. Errors if the container isn't up; suggests `pier dev`. This is the day-to-day dev command. Users type `php artisan`, `composer require`, `npm run dev`, `php artisan tinker`, `mysql -uroot -p`, `redis-cli`, etc. directly inside. |
| `pier exec <cmd...>` | One-off command in the `laravel.test` container. `docker compose exec -u <app_user> laravel.test <cmd...>`. Useful for CI or quick one-liners. Errors if the container isn't up. |
| `pier service add <name>` | Adds a service to `pier.toml` `services` list, smart-merges `docker-compose.yml`, brings the new service up (`--no-up` to skip). Idempotent. |
| `pier service remove <name>` | Removes a service from `pier.toml` `services` list, smart-merges `docker-compose.yml`, brings the service down. Idempotent. |
| `pier deploy <env>` | Flagship command. Renders prod files, syncs to remote, builds, runs, health-checks, rolls back on failure. TUI. |
| `pier rollback <env>` | Redeploys the previous image tag stored on the remote. |
| `pier status` | Lists known projects and the state of each (env, last deploy time, health). |

Global flags: `--config <path>` (default `./pier.toml`), `--json` (machine-readable, no TUI), `--verbose` (unfiltered Docker build output).

**Not in v1 (explicitly out of scope; documented above):** `pier share` (tunnel), `pier open` (browser launch), per-tool wrappers (`pier artisan`, `pier composer`, `pier tinker`, `pier test`, `pier mysql`, `pier psql`, `pier redis-cli`, …), agent env forwarding.

### 3. Laravel stack module

**Detect:** `composer.json` requires `laravel/framework`, and `artisan` file exists.

**Defaults:** PHP 8.3, Node 22, no optional services. `pier init` prompts for overrides.

**Version options:**
- PHP: 8.2, 8.3, 8.4, 8.5 (matches Laravel Sail's supported range)
- Node: 20, 22

**Service registry** (each is a struct: `Name`, `Image`, `Ports`, `Env`, `Volumes`, `Healthcheck`, `DevOnly`):

| Service | Dev/Prod | Notes |
|---|---|---|
| `mysql` | both | |
| `postgres` | both | |
| `redis` | both | |
| `meilisearch` | both | |
| `mailpit` | dev only | SMTP catch-all for local |
| `queue` | both | Laravel queue worker container |
| `scheduler` | both | Laravel scheduler (`schedule:run`) container |
| `s3` (SeaweedFS) | both | S3-compatible, single binary runs master + filer + S3, ports 8333/8888/9333 |

**User-defined dev services (`[dev.services.<name>]`):** pier only ships sidecars whose images are known to be available on Docker Hub. Anything else (a log viewer, a dump inspector, a Reverb sidecar from a custom registry, etc.) is configured by the user per-project and rendered into the dev compose. Each entry is a partial compose service; `image` is required, the other keys (`ports`, `environment`, `volumes`, `depends_on`, `restart`) are optional and pass through verbatim. Dev services are dev-only — they are never rendered into `docker-compose.prod.yml`. They are owned by pier for merge purposes, so editing `[dev.services.log-viewer]` in `pier.toml` and re-running `pier dev` updates the rendered block (scalar keys like `image` overlay-wins; sequence keys like `ports` keep the previous value, same as the rest of pier's merge behavior). Example:

```toml
[dev.services.log-viewer]
image = "ghcr.io/example/log-viewer:1.0"
ports = ["8081:8080"]

[dev.services.reverb]
image = "example/reverb:1"
ports = ["8080:8080"]
environment = { REVERB_APP_ID = "1" }
depends_on = ["redis"]
```

**Dev compose (smart merge):** if `docker-compose.yml` already exists, parse it to a YAML AST. For each service pier owns (the always-on `laravel.test` plus everything in `pier.toml` `services`), replace the block with the freshly-generated version from pier's templates. For each service pier doesn't own (user-added sidecars, third-party services, anything not in pier's registry), preserve it byte-for-byte. For pier-owned services, preserve unknown top-level keys the user has added (e.g. `extra_hosts`, a custom `command` override, `labels`) — pier only rewrites the keys it knows about. If the existing file contains a key pier doesn't recognize AND the file is being re-rendered (i.e. it's a "new" key in pier's vocabulary), warn once per session and ask keep-or-drop. If none exists, generate the full pier layout. The merge is idempotent: running `pier dev` twice produces the same file. Implementation lives in `internal/stack/laravel/merge.go` and `internal/compose/merge.go`.

**Runtime Dockerfiles (pier-owned):** pier ships its own `internal/stack/laravel/runtimes/<php-version>/` for each supported PHP version, containing a `Dockerfile` (multi-stage: PHP-FPM + Node + Composer, optimized autoloader), `php.ini`, and `supervisord.conf`. These are forked from Laravel Sail's `vendor/laravel/sail/runtimes/<php-version>/` at v1 cut-off, with a comment block at the top of each Dockerfile crediting Laravel and pointing at upstream. They live inside pier's repo so `pier init` is a pure file-write operation — no Composer install, no network, no `sail:install`, no coupling to Sail's release schedule or internal file layout. When a new PHP version lands, the Dockerfile is added to pier; the same pattern extends cleanly to future stack modules (`internal/stack/python/runtimes/...`, etc.).

**Prod compose:** always generated fresh by the tool. App service built from the pier-owned Dockerfile in `docker/<php-version>/Dockerfile`, plus whichever optional services the user picked (with prod-grade settings — no bind mounts, real volumes, restart policies). pier re-renders `docker-compose.prod.yml` from `pier.toml` on every deploy (deploy flow step 2) to prevent config drift.

**Devcontainer (`--devcontainer` flag):** generates `.devcontainer/devcontainer.json` referencing the pier-owned `docker-compose.yml`, with `service: "laravel.test"`, `workspaceFolder: "/var/www/html"`, and a recommended extensions list.

### 4. `pier.toml` shape (v1)

```toml
[project]
name = "myapp"
domain = "myapp.example.com"

[stack]
type = "laravel"
php = "8.3"
node = "22"
services = ["redis", "mailpit", "s3"]

[dev.services.log-viewer]
image = "ghcr.io/example/log-viewer:1.0"
ports = ["8081:8080"]

[deploy.production]
host = "prod.example.com"
user = "deploy"
path = "/srv/myapp"
branch = "main"
```

`[deploy.staging]` is supported by the engine; the user adds it by hand. README documents this.

#### Host port overrides (added in v0.0.x)

```toml
[dev.ports]
laravel       = 8000
vite          = 5173
mysql         = 3306
# ...

[deploy.production.ports]
laravel = 8383   # only the keys the user writes are applied; rest fall back to defaults
```

Dev binds to 127.0.0.1; prod/staging bind to 0.0.0.0. `pier dev` runs a
pre-flight port probe and exits with code 6 (`ErrPortInUse`) if any
pier-owned host port is already in use; the user edits `[dev.ports]` to
remap. See `docs/superpowers/specs/2026-07-29-dev-ports-design.md` for
the full design.

### 5. Generated files

- `docker-compose.yml` — dev, pier-owned, smart-merge on re-render (preserves user-added services and unknown keys with a one-time warn-and-confirm)
- `docker-compose.prod.yml` — production, pier-owned, regenerated from `pier.toml` on every deploy to prevent config drift
- `docker/<php-version>/Dockerfile` — pier-owned runtime, forked from Laravel Sail, kept in sync inside pier's repo
- `docker/<php-version>/php.ini`, `docker/<php-version>/supervisord.conf` — pier-owned runtime config
- `docker/nginx/default.conf` — HTTPS, gzip, caching
- `.env.production.example` — key list with safe defaults
- `.env` (dev) — auto-merged with user-authored keys
- `.devcontainer/devcontainer.json` (opt-in, `--devcontainer`)

### 6. Deploy flow

`pier deploy <env>` runs seven phases. Each is a discrete unit, individually testable, individually skippable in tests.

1. **Pre-flight (local + remote)** — validate `pier.toml`, verify SSH key, confirm git branch, fail fast. Remote: open SSH, check `docker` + `docker compose` plugin, check disk space, confirm deploy path is writable.
2. **Render (local)** — read `.env.production` (prompt for missing keys vs. `.env.production.example`). Re-emit `docker-compose.prod.yml` from template + current `pier.toml` to prevent config drift.
3. **Sync (SSH)** — `rsync -az -e ssh`. Excludes: `.git`, `node_modules`, `vendor`, `.env`, `.env.*` (except `.env.production`), `storage/logs/*`, IDE files. Falls back to `tar | ssh tar` if rsync is missing on the remote.
4. **Build (remote)** — `docker compose -f docker-compose.prod.yml build --pull`. Tag image as `<project>:<git-sha>` and `<project>:current`. Stream build output to TUI (filtered).
5. **Up (remote)** — save previous image tag to `.pier/state.json`, then `docker compose -f docker-compose.prod.yml up -d`. Wait for each service to report `running` (not just `up -d` returning).
6. **Health check** — HTTP probe against `https://<domain>/up` (Laravel's built-in liveness route) with exponential backoff up to a configurable timeout (default 60s). HTTPS is assumed (Let's Encrypt on the VPS via the standard nginx config); a future v1.1 may add an HTTP-only mode. Per-probe status code + latency in the TUI.
7. **Commit or rollback** — on success, write new state. On any failure in steps 4–6, automatic rollback: switch `:current` to saved previous tag, re-up, re-probe, log failure.

### 7. Remote state file

`.pier/state.json` on the remote:

```json
{
  "current": "a1b2c3d",
  "previous": "789ef01",
  "deployed_at": "2026-07-26T10:30:00Z",
  "deployed_by": "user@host"
}
```

Only written after a successful health check, so a failed deploy never overwrites the previous known-good state.

### 8. TUI

Single Bubble Tea screen, full terminal. The TUI surfaces the **six user-visible phases** of the deploy flow. The Render phase (deploy step 2) runs silently before the TUI opens, and the Commit-or-Rollback step (deploy step 7) is reflected in the final "Done" state — green if committed, yellow with the rollback log if rolled back.

- Top: phase strip (Pre-flight → Sync → Build → Up → Health → Done) with status icon and elapsed time per phase
- Middle: live log tail (last 30 lines, filtered)
- Bottom: per-container status, key bindings (`q` quit, `l` toggle log verbosity, `c` cancel current phase)
- Falls back to `--json` plain log output on TUI crash

### 9. Error handling

| Phase | Failure | Behavior |
|---|---|---|
| Pre-flight | SSH unreachable / auth fail | Print host:port + key path; suggest `pier config get deploy.<env>`. Exit 2. |
| Pre-flight | Docker missing on remote | Print detected versions or "not found"; link README. Exit 2. |
| Pre-flight | Low disk / wrong branch | Warn, prompt to continue or abort. |
| Render (deploy) | Missing `.env.production` keys | Compare to `.env.production.example`; list missing, prompt once each (or `--env-file`). |
| Render (dev / service add) | Smart-merge: unknown key in existing `docker-compose.yml` | Warn once per session, list the key, prompt keep-or-drop. If keep, preserve; if drop, remove. Decision is remembered for the rest of the session. |
| `pier shell` / `pier exec` | Container not running | Print "Container not running" + the last successful phase from `docker compose ps`; suggest `pier dev`. Exit 5. |
| Sync | Rsync interrupted | State unchanged on remote; retry is safe. Print last successful phase. |
| Build | `docker compose build` fails | Stream failing step logs (last 50 lines). No rollback (nothing was up'd). Exit 3. |
| Up / Health | Containers crash or probe times out | Automatic rollback. Print failure + rollback result. Exit 4. |
| Any | `Ctrl-C` or `q` | `SIGINT` to active remote command; no-op rollback if past step 5, leave state as-is otherwise. |
| TUI | Bubble Tea error | Fall back to `--json` plain log output. |

**Logging:** default human-readable with phase headers and color when stdout is a TTY. `--json` = one JSON object per line per event for piping. `--verbose` = unfiltered Docker build output. Every phase emits a start and end event.

### 10. Testing

| Layer | Coverage | Mechanism |
|---|---|---|
| Unit | TOML parsing, service registry, YAML rendering, **smart-merge (preserved keys, unknown-key warn-and-confirm, idempotency)**, state file shape, health-check backoff, error formatting. | Table-driven `testing` package, no I/O mocks. Merge tests feed in compose fixtures with user-added services / unknown keys / extra `extra_hosts` and assert the merge result byte-for-byte (or structurally with go-cmp). |
| Golden | Exact bytes of generated `docker-compose.yml`, `docker-compose.prod.yml`, `docker/<php-version>/Dockerfile`, nginx config, plus **golden merge outputs** for each smart-merge fixture. | `testdata/golden/` snapshots. Update with `go test -update`. |
| Integration | Generated files parse and validate against local Docker Compose schema. | `docker compose config --quiet`. Skipped if Docker unavailable. |
| SSH/deploy | Full deploy pipeline including rollback. | `testcontainers-go` spins up a Linux container with `openssh-server` + Docker. `pier deploy` runs against `localhost:<random port>`. |
| TUI | Deploy screen renders phases, transitions, exits on `q`/success. | Bubble Tea's `teatest`, headless. |
| `pier shell` / `pier exec` | Command construction is correct (right user, right service, TTY detection, agent env NOT forwarded). | Unit tests on the command builder; the actual `docker compose exec` is exercised in dev. |

**Conventions:** every package has `*_test.go` next to the code. Integration/SSH tests behind `//go:build integration` so `go test ./...` stays sub-second. Coverage targets: 80% on `internal/stack`, `internal/compose`, `internal/config`; 60% on `internal/deploy` (rest is exercised by SSH tests). CI matrix on macOS, Linux, Windows for unit + golden; Linux only for integration/SSH.

**Manual verification checklist** (README):
- `pier init` on a fresh Laravel project (no existing compose)
- `pier init` on a project that already has a `docker-compose.yml` (smart-merge path; verify user services preserved)
- `pier init` on a project that already has a `docker-compose.yml` with an unknown key (warn-and-confirm path)
- `pier service add redis` and `pier service remove redis` on a project that already has them (idempotency)
- `pier init --devcontainer` in VS Code
- `pier shell` and `php artisan migrate` from inside
- `pier exec php artisan --version` from the host
- `pier deploy production` to a real VPS
- `pier rollback production` after a deliberate bad deploy

## Alternatives considered and why not

- **Rust + TUI** — equally viable, slightly more polish on the TUI side, but slower compile, no extra benefit for this use case, learning-curve tax.
- **JavaScript/TypeScript (Bun) + CLI only** — fastest to write, but heavier distribution, weaker SSH libs, weaker TUI ecosystem. Wrong tool for a single-binary system tool.
- **Native GUI / menu-bar app** — ruled out for cross-platform reach. Adding Win + Linux tray support would dwarf the rest of the project.
- **Always-on daemon with tray icon** — picked B (CLI + optional background service) over C (tray/daemon) in Q3. The Docker stack itself is the "background service"; `pier` doesn't need its own.
- **Just generate the prod compose, user handles deploy** — picked C over A in Q4 because full deploy with health checks is the whole point. A small deploy script on top of the generator is more useful than the generator alone.
- **Staging + production both in generated `pier.toml`** — picked A over B in Q8 to keep the generated config minimal. README documents the staging shape.
- **Cloud-provider-specific deploy (AWS, DO, Hetzner APIs)** — SSH-only for v1. Cloud SDKs add dependency weight and lock-in for no gain on a single-VPS setup.
- **Use Laravel Sail's runtime Dockerfiles via `vendor/laravel/sail/runtimes/<php-version>/`** — rejected. pier is meant to be fully self-contained (`pier init` does no Composer install, no network, no `sail:install`); depending on the Sail package couples pier's release schedule to Sail's, makes `pier init` non-deterministic (depends on what the user has in `vendor/`), and breaks the multi-stack story (there's no Python equivalent to lean on for the next module). The cost of forking the runtime Dockerfiles is bounded — they're short, well-commented, and easy to keep in sync — and it sets up a clean pattern for future stack modules.
- **Per-tool command wrappers (`pier artisan`, `pier composer`, `pier tinker`, `pier mysql`, `pier psql`, `pier redis-cli`, …)** — rejected. Sail's `bin/sail` is ~400 lines of bash for this category, and the wrappers teach users a `sail`-specific vocabulary when they don't need one. `pier shell` (interactive bash in the app container) plus `pier exec` (one-off command) covers all of it: the user types `php artisan migrate`, `composer require`, `php artisan tinker`, `mysql -uroot -p`, `redis-cli` directly. The pattern generalizes across stacks — `pier shell` into a Python container → `python manage.py`, into a Rails container → `rails db:migrate`. The wrapper-per-language approach doesn't.
- **Forward agent env vars into containers (Sail's 21-vendor list: `AI_AGENT`, `CLAUDECODE`, `OPENCODE`, `CURSOR_AGENT`, `GEMINI_CLI`, …)** — rejected for v1. The list is a moving target; new coding agents appear every quarter and a hard-coded list will be perpetually stale. The user can set env in their shell before `pier exec` / `pier shell` if they need a specific tool identified inside the container. If a real need surfaces (e.g. a coding agent shells out to `pier exec` and needs to be identified inside the container), revisit as `--forward-env-from-host`.

## Open questions

- **SeaweedFS image choice** — default to `chrislusf/seaweedfs` (community image, single binary runs master + filer + S3). If the official `seaweedfs/seaweedfs` image is the maintained path by 2026, switch at implementation time; the implementation plan will confirm the current canonical image before scaffolding.
- **Bucket naming convention** — Laravel's default bucket is `app`. `pier init` will suggest this and let the user override. No magic; user can change in `.env.production`.
- **Backup strategy on the remote** — SeaweedFS data is on a named volume. Backups are out of scope for v1 but the README should mention `docker run --rm -v s3_data_prod:/data -v $(pwd):/backup alpine tar czf /backup/s3.tgz /data` as a manual approach.
- **Multi-host prod** — explicitly out of scope. If a single VPS becomes insufficient, the future answer is probably "two VPS, a load balancer, and Postgres replication" — that's a different design, not a v1.1.
- **Agent env forwarding into containers** — explicitly deferred. v1 does not forward `OPENCODE` / `CLAUDECODE` / `CURSOR_AGENT` / etc. into the container. The user sets env in their shell. If a coding agent shelling out to `pier exec` needs to be identified inside the container, this becomes a `--forward-env-from-host` flag with a curated allowlist (or a `pier.toml` `[exec.forward_env]` list). The Sail 21-vendor list is a cautionary tale: the right set is small and slow-moving, not big and brittle.

## Next step

Hand the spec to the **writing-plans** skill, which will break it into ordered implementation tasks (project scaffold, stack interface, Laravel detect/defaults, dev compose generation, prod compose generation, deploy pipeline, health check + rollback, TUI, tests, CI). No code is written until that plan is reviewed and approved.

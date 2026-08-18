# Changelog

## Unreleased

### Added

- Custom domains + HTTPS: the production webserver is now Caddy
  (`caddy:2-alpine`) with a pier-rendered `docker/caddy/Caddyfile`.
  A non-empty `[deploy.<env>].domain` enables HTTPS with automatic
  Let's Encrypt certificates; `[deploy.<env>].redirect_domains`
  (e.g. `www`) are served and redirected to the env's domain. Empty
  domain = plain HTTP by IP.
- Deploy DNS preflight: when a domain is set, `pier deploy` verifies
  it resolves to the deploy host before syncing and fails fast with
  an A-record hint otherwise; the health probe then checks
  `https://<domain>/up` end to end.
- `pier init` prompts for the production domain, written to
  `[deploy.production].domain` (blank = plain HTTP by IP).

### Changed

- The deploy render phase re-writes `docker/caddy/Caddyfile` from
  `pier.toml` before syncing. Setting (or removing) a domain in
  `[deploy.<env>]` now takes effect on the next `pier deploy`; the
  file is pier-rendered, so manual edits are overwritten.
- The webserver service in the merged prod compose is now fully
  pier-managed: its ports follow `pier.toml`, so adding a domain
  publishes `443:443` (and removing one drops it) instead of keeping
  a stale `[80:80]` list that leaves Caddy unable to answer HTTPS.
- `MergeEnvFile` updates pier-derived `.env.production` keys (e.g.
  `APP_URL` from the env's domain) when the render changes, instead of
  preserving every existing line verbatim. User-owned keys are still
  never touched: secrets (`APP_KEY`, `DB_PASSWORD`, AWS credentials)
  and the `${...}`-interpolated overrides (`TRUSTED_PROXIES`,
  `CACHE_STORE`, `QUEUE_CONNECTION`).
- Domains are per deploy env: `[project].domain` is removed and the
  old per-env extra-domains key is renamed to
  `[deploy.<env>].redirect_domains`. Existing configs keep loading
  (unknown keys are ignored), but envs no longer inherit a
  project-wide domain — set `domain` in each `[deploy.<env>]`
  section.

### Removed

- `[deploy.<env>].tls` — HTTPS is now implied by domain presence.
  Existing configs keep loading (the key is ignored); delete it and
  set the domain instead.

## v0.0.6-beta (2026-08-15)

### Added

- `queue_workers` config (`[stack]` default 1, `[deploy.<env>]`
  override, max 32) runs that many `queue:work` processes in the
  queue container via supervisord `numprocs`, in dev and prod.
  `pier init` writes the explicit default.

### Changed

- Bumped version constant to `0.0.6-beta` (reflected in
  `pier --version`, `cmd/pier/main_test.go`, and the README status
  line).

## v0.0.5-beta (2026-08-11)

### Added

- `pier init` asks the full deploy setup — deploy host/user/path
  (branch defaulting to main) and the build machine (host_server /
  local_machine / build_server, with build host/user/path when
  build_server); `--builder` / `--host` / `--user` / `--path` /
  `--build-host` / `--build-user` / `--build-path` flags skip the
  prompts.
- `[deploy.<env>].builder` / `build_host` / `build_user` / `build_path`
  configuration for build server modes; `pier bootstrap <env>`
  provisions both machines when `build_server` is set.
- Real git SHA image tags (timestamp fallback) replace the hardcoded
  `gitsha` placeholder; `docker tag` wiring fixes `pier rollback` in
  every builder mode.
- Per-env sidecar services: `[deploy.<env>].services` overrides
  `[stack].services` for that env (absent = inherit, `[]` = none).
  `pier init` scaffolds `[deploy.production]` with the chosen
  services; `pier service [env]` replaces `add`/`remove` with a
  single init-style picker that edits dev or per-env lists.
- `pier deploy <env>` now re-renders `docker-compose.prod.yml` and
  `.env.production` from pier.toml before syncing (preserving
  hand-written compose edits and existing env values), so per-env
  services and `[deploy.<env>].ports` overrides take effect.
- Remote teardown: `docker compose up` runs with `--remove-orphans`,
  so sidecars removed from an env are stopped and removed on the
  server (named volumes are kept).

### Removed

- `pier buildmode <env>` — the init flow now asks the build-machine
  question; change it later by editing `pier.toml`.

### Changed

- The `pier init` deploy-host prompt now reads "Deploy host (SSH target
  Domain name or IP address, enter to skip)" so the expected input is
  clear.

### Fixed

- The `queue` / `scheduler` app-image sidecars in image modes
  (`local_machine`, `build_server`) now reference the image tag the
  transfer phase actually ships (`:current`) instead of `:latest`:
  compose would otherwise try to pull `:latest` from the registry, and
  a first deploy failed with `pull access denied`.
- App-image sidecars now receive the same connection environment as the
  app (`DB_*`, `REDIS_*`, `APP_KEY` interpolated from
  `.env.production`): without it the worker authenticated with the dev
  `.env` baked into the image and crash-looped with `password
  authentication failed`.
- With redis in the stack, the rendered env files now default
  `CACHE_STORE=redis` and `QUEUE_CONNECTION=redis` (interpolated via
  `${CACHE_STORE}` / `${QUEUE_CONNECTION}` in the prod compose so the
  values can be overridden in `.env.production`; dev compose gets the
  same defaults). The database-driver defaults made queue/scheduler
  workers boot-check the `cache`/`jobs` tables — which only exist after
  `after_deploy` migrations — so a first deploy's `up --wait` could
  never pass, and a refused boot-time connection made `queue:work` exit
  0, which supervisord's default `autorestart=unexpected` never
  restarted, leaving the queue container unhealthy forever.
- The prod runtime's supervisord now always restarts the php program
  (`autorestart=true`), so a Laravel worker that exits 0 (`queue:
  restart`, a lost-connection stop in newer Laravel) comes back instead
  of staying dead.
- Bumped version constant to `0.0.5-beta` (reflected in
  `pier --version`, `cmd/pier/main_test.go`, and the README status
  line).

## v0.0.4-beta

### Added

- `pier shell <env>` and `pier exec <env> <cmd...>` now target a
  deploy host when the first argument names a `[deploy.<env>]` entry:
  `pier shell production` opens an interactive bash in the production
  `app` container over SSH (PTY with resize forwarding), and
  `pier exec production php artisan migrate` runs a one-off command
  there, propagating its exit code to pier's own exit code.
- `[deploy.<env>].tls` flag (default `false`): production serves plain
  HTTP end-to-end. `tls = true` renders HTTPS URLs and the 443 port
  mapping; SSL certificate provisioning ships in a later release.
- The prod renderer now emits `docker/<php>/Dockerfile.prod`, a
  two-stage Dockerfile that bakes the application into the image
  (COPY from the project-root build context, `composer install
  --no-dev`, `npm ci` + `npm run build`, storage chown to the sail
  user) — previously the prod image only contained the runtime, so
  every container crashed with `Could not open input file:
  /var/www/html/artisan` and the deploy health probe always failed.
- Deploy state (`.pier/state.json`) is now written to the remote host
  over SFTP instead of the local disk, so `pier rollback` and
  `pier status <env>` see the deploy record. `Rollback` reads the
  remote state too.
- `[deploy.<env>].before_deploy` / `after_deploy` command lists: each
  entry runs inside the app container on the deploy host, before the
  new release starts (`before_deploy`, after the image build, while
  the old release still serves) or after it is up (`after_deploy`,
  after `docker compose up` and the nginx reload, before the health
  probe). Commands run in order and stop at the first failure: a
  failing command aborts the deploy with exit code 7 — `before_deploy`
  failures keep the old release serving, `after_deploy` failures roll
  back to the previous image. `before_deploy` is skipped on a first
  deploy (no app container exists yet); `pier init` writes both keys
  commented out.

### Changed

- Deploy and `pier status <env>` health probes now target
  `http://<host-ip>:<port>/up` (the `[deploy.<env>].host` address)
  instead of the public domain, so health checks pass without DNS or
  `/etc/hosts` entries. `APP_URL` and the displayed deploy URL now
  follow the env's scheme.
- The displayed deploy URL falls back to the deploy host IP when the
  project domain does not resolve, so the printed URL is usable
  before DNS entries exist.
- The prod nginx conf proxies every request verbatim to the app
  container's `artisan serve` listener (`proxy_pass http://app:80`)
  instead of fastcgi on 9000 (the runtime has no php-fpm) and no
  longer rewrites to `/index.php` via `try_files` (the built-in
  server 500s when executing an existing public file directly).
- `docker compose up` now runs with `--wait --wait-timeout 120` and
  is followed by a webserver `nginx -s reload`. The `--wait` makes
  compose return only once every healthchecked service (postgres,
  redis, the sidecars) is healthy, so a fresh database volume still
  initializing no longer races the first `after_deploy` command
  (`SQLSTATE[08006] Connection refused` on `php artisan migrate
  --force`, which worked minutes later in an interactive shell); the
  timeout bounds the wait so a never-healthy service fails the
  deploy instead of hanging it. The reload makes bind-mounted conf
  changes (the sync rewrites files in place, preserving the inode)
  take effect without a container recreate.
- Every remote compose invocation now passes
  `--env-file .env.production`, and the prod renderer emits
  `${DB_PASSWORD}` / `${APP_KEY}` interpolations for the DB sidecar
  and the app, so the placeholders resolve to the real values instead
  of warning and falling back to blank.
- Bumped version constant to `0.0.4-beta` (reflected in
  `pier --version`, `cmd/pier/main_test.go`, and the README status
  line).

### Fixed

- The app no longer 500s on a fresh deploy with blank secrets: before,
  the compose interpolation warnings fell back to empty values, so
  Postgres answered `fe_sendauth: no password supplied` (DB_PASSWORD)
  and session encryption threw with no APP_KEY. The deploy host's
  `.env.production` (the only `.env.*` file that survives the sync
  filter) now feeds every remote compose invocation.
- In-flight remote sessions (`pier shell <env>`, `pier exec <env>`,
  the deploy pipeline) are cancelled via the SSH context when the
  command is interrupted, and `before_deploy` / `after_deploy` hooks
  stop running once the context is cancelled.
- On a first deploy (no `.pier/state.json` on the host), an
  `after_deploy` or health failure no longer dies with a confusing
  dead-end "deploy: rollback: no previous deploy to roll back to"
  error: rollback is skipped, the phase is logged as such, and the
  real failure is reported (exit code 7 for a failed hook). With a
  previous image on record, rollback still re-tags and re-ups it
  before the error is reported.

## v0.0.3-beta

### Fixed

- Deploy file sync now recreates local symlinks on the remote host over
  SFTP, so links such as `storage/app/public` survive a `pier deploy`
  (they were previously skipped). Recreation is idempotent: an existing
  remote link with the same target is left untouched, a stale regular
  file at the link path is replaced, and a directory conflict surfaces a
  clear error instead of being silently deleted.
- Remote commands (`pier deploy`, `pier bootstrap`, `pier status <env>`)
  now fail fast with a clear preflight error when the SSH key path
  exists but cannot be read (e.g. broken permissions), instead of
  silently falling back to the password prompt.

### Changed

- Bumped version constant to `0.0.3-beta` (reflected in `pier --version`, `cmd/pier/main_test.go`, and the README status line).

## v0.0.2-beta

### Added

- SSH password auth fallback: when a deploy host rejects the SSH key
  (or no key exists), `pier deploy`, `pier rollback`, `pier status <env>`,
  and `pier bootstrap` prompt once for the password and connect with
  `password` / `keyboard-interactive` auth. The password is never
  stored. Cancelling the prompt exits 130.
- Deploy file sync now runs over SFTP on pier's own SSH connection
  instead of the `rsync` subprocess, so no local `ssh`/`rsync` binary
  is required. `.env.production` is now actually synced (the old
  rsync exclude ordering dropped it).
- Application logo (`assets/logo.png`) is now embedded into the binary via `go:embed` in the new `assets` package and surfaced on the README so pier has a recognizable brand.
- Comprehensive Go doc comments on every package and on every exported type, function, and method. `go doc ./...` now produces a complete reference (cmd/pier, internal/cli, internal/config, internal/deploy, internal/docker, internal/portcheck, internal/compose, internal/stack, internal/stack/laravel, internal/tui).
- `pier status <env>` probes a remote deploy host over SSH: container state, deploy-path and docker disk usage, a one-shot health check, and the last deploy record from `.pier/state.json`. `pier status` with no env still shows local status only.

### Changed

- Bumped version constant to `0.0.2-beta` (reflected in `pier --version`, `cmd/pier/main_test.go`, and the README status line).

### Fixed

- `pier shell` (and `pier exec` in TTY mode) no longer fails with `cannot attach stdin to a TTY-enabled container because stdin is not a terminal`. `ExecRunner` now forwards `os.Stdin` to the child `docker compose exec` process instead of leaving it nil (which silently became `/dev/null`). The `Runner` interface gained a `stdin io.Reader` parameter so the fix is unit-tested.
- `pier dev` no longer fails with `pull access denied for opcodesio/log-viewer` (and the cascade of interrupted pulls for `nicolasbissig/laravel-dumps` and `serversideup/reverb`). These services were hardcoded in pier's registry but their Docker images do not exist on Docker Hub — `opcodesio/log-viewer` and `nicolasbissig/laravel-dumps` are Laravel Composer packages, not container images; `serversideup/reverb` was an incorrect image name.

### Added

- `[dev.services.<name>]` section in `pier.toml` for opt-in dev-only sidecars (log viewer, Reverb, dump inspector, or anything else). Each entry takes `image` (required), `ports`, `environment`, `volumes`, `depends_on`, and `restart`. Dev services are merged into `docker-compose.yml` and never appear in `docker-compose.prod.yml`.

### Removed

- Hardcoded `reverb`, `log-viewer`, and `dumps` entries from the service registry. They are no longer in `pier service add` / `pier init` pickers. Use `[dev.services.<name>]` with a real image instead.

### Fixed

- `pier bootstrap` now force-syncs the remote clock when it drifts more than 60 seconds from the local clock, so a freshly-reset VM with a stale RTC no longer fails provisioning with `Release file ... is not valid yet`.
- `pier deploy` build failures now include the tail of the remote build output, so docker compose validation errors (e.g. `refers to undefined volume`) reach the terminal instead of only an exit status.

## v0.0.1-beta (2026-07-27)

Initial beta release.

- `pier init`, `pier dev`, `pier stop`, `pier shell`, `pier exec`
- `pier service add` / `pier service remove`
- `pier status`
- `pier deploy <env>` with health check + automatic rollback
- `pier rollback <env>`
- Laravel stack module with smart-merge into existing `docker-compose.yml`
- Pier-owned runtime Dockerfiles (forked from Laravel Sail)
- Bubble Tea TUI for deploy pipeline
- CI on macOS, Linux, Windows (unit + golden); Linux only (integration)

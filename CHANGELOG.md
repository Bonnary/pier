# Changelog

## Unreleased

### Added

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
- `docker compose up` is followed by a webserver `nginx -s reload`
  so bind-mounted conf changes (the sync rewrites files in place,
  preserving the inode) take effect without a container recreate.

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

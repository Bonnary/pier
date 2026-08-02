# Changelog

## v0.0.2-beta

### Added

- Application logo (`assets/logo.png`) is now embedded into the binary via `go:embed` in the new `assets` package and surfaced on the README so pier has a recognizable brand.
- Comprehensive Go doc comments on every package and on every exported type, function, and method. `go doc ./...` now produces a complete reference (cmd/pier, internal/cli, internal/config, internal/deploy, internal/docker, internal/portcheck, internal/compose, internal/stack, internal/stack/laravel, internal/tui).

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

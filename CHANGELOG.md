# Changelog

## v0.1.0 (2026-07-27)

Initial release.

- `pier init`, `pier dev`, `pier stop`, `pier shell`, `pier exec`
- `pier service add` / `pier service remove`
- `pier status`
- `pier deploy <env>` with health check + automatic rollback
- `pier rollback <env>`
- Laravel stack module with smart-merge into existing `docker-compose.yml`
- Pier-owned runtime Dockerfiles (forked from Laravel Sail)
- Bubble Tea TUI for deploy pipeline
- CI on macOS, Linux, Windows (unit + golden); Linux only (integration)

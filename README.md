<p align="center">
  <img src="assets/logo.png" alt="pier logo" width="160" />
</p>

<h1 align="center">pier</h1>

<p align="center">
  Personal cross-platform CLI for Laravel Docker dev + production deploys.<br />
  One command from a fresh Laravel repo to a production deploy with health checks and automatic rollback.
</p>

<p align="center">
  <a href="https://github.com/Bonnary/pier/releases"><img alt="Release" src="https://img.shields.io/github/v/release/Bonnary/pier?style=flat-square"></a>
  <a href="https://github.com/Bonnary/pier/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/github/license/Bonnary/pier?style=flat-square"></a>
  <a href="https://golang.org"><img alt="Go" src="https://img.shields.io/badge/go-1.25+-00ADD8?style=flat-square&logo=go"></a>
  <a href="https://github.com/Bonnary/pier/actions"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Bonnary/pier/unit.yml?style=flat-square&branch=main&label=ci"></a>
  <a href="https://github.com/Bonnary/pier/issues"><img alt="Issues" src="https://img.shields.io/github/issues/Bonnary/pier?style=flat-square"></a>
  <img alt="Status" src="https://img.shields.io/badge/status-beta-orange?style=flat-square">
</p>

---

`pier` turns a Laravel project into a fully provisioned dev + production
Docker stack with one-command deploys, health checks, and automatic
rollback. It is a single, self-contained Go binary — no Composer
dependency, no daemon, no telemetry, no network calls beyond SSH and the
Docker CLI.

> **Status:** `v0.0.2-beta` — under active development. The Laravel
> stack is feature-complete for the documented workflows; other stacks
> (Node, Python, Rails, etc.) are explicitly out of scope for v1.

---

## Table of contents

- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Quickstart](#quickstart)
- [Commands](#commands)
- [Configuration (`pier.toml`)](#configuration-piertoml)
- [Project structure](#project-structure)
- [Development](#development)
- [Manual verification checklist](#manual-verification-checklist)
- [Out of scope (v1)](#out-of-scope-v1)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [Roadmap](#roadmap)
- [License](#license)
- [Acknowledgements](#acknowledgements)

---

## Features

- **`pier init`** — Detect Laravel, write `pier.toml`, generate
  `docker-compose.yml`, runtime Dockerfiles, and a matching
  `vite.config.ts` patch in one pass. Smart-merges into an existing
  `docker-compose.yml` with warn-and-confirm on unknown keys.
- **`pier dev` / `pier stop`** — Bring up (or stop) the dev stack
  with a pre-flight port probe and a clear ready block.
- **`pier shell` / `pier exec`** — Interactive bash in the
  `laravel.test` container, or a one-off command against it.
- **`pier service add|remove`** — Manage auxiliary services
  (`redis`, `mailpit`, `s3`, `meilisearch`, etc.) interactively or
  from the CLI. Idempotent.
- **`pier deploy <env>`** — Build, sync, up, health-check, and
  commit a production image tag over SSH. A Bubble Tea TUI shows
  live phase progress.
- **Automatic rollback** — Any failure in the `up` or `health`
  phase re-tags the previous image and re-deploys it before the
  command exits non-zero.
- **`pier rollback <env>`** — Re-deploy the previous image tag on
  demand.
- **`pier status`** — One-glance project + container status.
- **Dev-only sidecars** — `[dev.services.<name>]` in `pier.toml` for
  opt-in dev-only services (log viewers, Reverb, dump inspectors,
  etc.). Never appear in the production compose.
- **Cross-platform** — Single static binary for macOS, Linux, and
  Windows.
- **No background daemon** — `pier` is a one-shot CLI. `docker
  compose up -d` does the lifting between calls.
- **No vendor lock-in** — pier does not depend on `laravel/sail`, does
  not run `sail:install`, and does not touch `vendor/`.

---

## Prerequisites

- **Go 1.25+** — only required if you are building from source.
  Pre-built binaries are available on the
  [releases page](https://github.com/Bonnary/pier/releases).
- **Docker Engine 24+** with the `docker compose` plugin (Docker
  Desktop on macOS/Windows; Docker Engine on Linux).
- **OpenSSH** client (`ssh`, `rsync`-over-ssh) for `pier deploy` and
  `pier rollback`. `pier` uses the host's `~/.ssh/id_ed25519` by
  default; override with `--ssh-key` or `$DEPLOY_SSH_KEY`.
- **A Laravel project** — `pier init` requires a `composer.json`
  that requires `laravel/framework` and an `artisan` file at the
  project root.

---

## Installation

### From source (recommended)

```bash
go install github.com/Bonnary/pier/cmd/pier@latest
```

This installs the `pier` binary into `$GOBIN` (or
`$GOPATH/bin`, defaulting to `~/go/bin`).

### Pre-built binaries

Download the archive for your platform from the
[releases page](https://github.com/Bonnary/pier/releases), extract
it, and move the binary onto your `$PATH`:

```bash
# macOS (Apple Silicon)
curl -L -o pier.tar.gz \
  https://github.com/Bonnary/pier/releases/latest/download/pier_darwin_arm64.tar.gz
tar -xzf pier.tar.gz
sudo mv pier /usr/local/bin/

# Linux (x86_64)
curl -L -o pier.tar.gz \
  https://github.com/Bonnary/pier/releases/latest/download/pier_linux_amd64.tar.gz
tar -xzf pier.tar.gz
sudo mv pier /usr/local/bin/

# Windows (PowerShell)
Invoke-WebRequest -Uri "https://github.com/Bonnary/pier/releases/latest/download/pier_windows_amd64.zip" -OutFile pier.zip
Expand-Archive pier.zip
Move-Item pier.exe $env:USERPROFILE\bin\
```

### Build from a local clone

```bash
git clone https://github.com/Bonnary/pier
cd pier
go build -o pier ./cmd/pier
sudo mv pier /usr/local/bin/
```

### Verify

```bash
pier --version
# pier 0.0.2-beta
```

---

## Quickstart

```bash
cd my-laravel-app

# 1. Initialize pier (writes pier.toml, docker-compose.yml, runtime Dockerfiles,
#    and patches vite.config.ts so the Vite dev server is reachable from the host).
pier init

# 2. Bring up the dev stack.
pier dev

# 3. Open a shell in laravel.test, or run a one-off command.
pier shell             # interactive bash in laravel.test
pier exec php artisan migrate

# 4. Add an aux service (e.g. redis) — interactive TUI picker.
pier service add redis

# 5. Deploy to production after editing pier.toml to add [deploy.production].
pier deploy production

# 6. Roll back if a deploy misbehaves.
pier rollback production

# 7. Check the state of any env at a glance.
pier status
```

`pier init` writes:

- `pier.toml` — pier's project config.
- `docker-compose.yml` — dev stack (smart-merged into an existing
  one with warn-and-confirm on unknown keys).
- `docker/<php>/Dockerfile` and matching runtime files — pier-owned,
  forked from Laravel Sail.
- `.devcontainer/devcontainer.json` — when `pier init --devcontainer`
  is passed.

It also patches `vite.config.ts` to set `server: { host: true }` so
the Vite dev server is reachable from the host through the Docker
port forward.

---

## Commands

| Command | Description |
| --- | --- |
| `pier init [path]` | Detect Laravel, write `pier.toml`, generate `docker-compose.yml` + runtime, patch `vite.config.ts`. |
| `pier init --devcontainer` | Also generate `.devcontainer/devcontainer.json` for VS Code. |
| `pier dev` | Bring up the dev stack. Runs a pre-flight port probe; exits with code 6 if a pier-owned host port is taken. |
| `pier stop` | Stop the dev stack (volumes preserved). |
| `pier shell` | Interactive `bash` in the `laravel.test` container. |
| `pier exec <cmd...>` | Run a one-off command in `laravel.test`. |
| `pier service add <name...>` | Add one or more services to `pier.toml` + `docker-compose.yml`. Interactive TUI picker when no names are given. |
| `pier service remove <name...>` | Remove one or more services from `pier.toml` + `docker-compose.yml`. |
| `pier deploy <env>` | Build, sync, up, health-check; rollback on failure. Renders a Bubble Tea TUI with live phase progress. |
| `pier rollback <env>` | Re-deploy the previous image tag. |
| `pier status` | Show project and container status for the current env. |

### Global flags

| Flag | Description |
| --- | --- |
| `--config <path>` | Path to `pier.toml` (default: `pier.toml`). |
| `--json` | Emit one JSON object per line per event (machine-readable deploy logs). |
| `--verbose` | Unfiltered Docker build output. |

---

## Configuration (`pier.toml`)

A minimal `pier.toml`:

```toml
[project]
name = "myapp"
domain = "myapp.example.com"

[stack]
type  = "laravel"
php   = "8.3"
node  = "22"
services = ["redis", "mailpit"]

[dev]
# bind = "0.0.0.0"   # uncomment to expose dev ports to your LAN (default: 127.0.0.1)

[dev.ports]
laravel = 8000
vite    = 5173
redis   = 6379

[deploy.production]
host   = "prod.example.com"
user   = "deploy"
path   = "/srv/myapp"
branch = "main"

[deploy.production.ports]
laravel = 443   # only the keys the user writes are applied
```

### `[dev.services.<name>]` — opt-in dev sidecars

Anything you want to run locally but not in production — a log
viewer, Reverb, a dump inspector, a sidecar Postgres for tests, etc.
These are merged into `docker-compose.yml` and never appear in
`docker-compose.prod.yml`.

```toml
[dev.services.reverb]
image       = "laravel/reverb:latest"
ports       = ["8080:8080"]
environment = { BROADCAST_CONNECTION = "reverb" }
restart     = "unless-stopped"

[dev.services.log-viewer]
image = "grahamcampbell/php-fpm-log-viewer:latest"
ports = ["8081:80"]
```

The full shape of `pier.toml` is documented in
[`docs/superpowers/specs/2026-07-26-pier-design.md`](docs/superpowers/specs/2026-07-26-pier-design.md).

---

## Project structure

```
pier/
├── cmd/pier/                 # entry point (main.go)
├── internal/
│   ├── assets/               # go:embed for the logo
│   ├── cli/                  # cobra command tree
│   ├── compose/              # YAML generation + smart-merge
│   ├── config/               # pier.toml parser
│   ├── deploy/               # SSH, rsync, health, rollback
│   ├── docker/               # thin wrapper around `docker compose`
│   ├── portcheck/            # pre-flight host port probe
│   ├── stack/                # Stack interface + registry
│   │   └── laravel/          # v1 implementation
│   └── tui/                  # Bubble Tea screens
├── assets/
│   └── logo.png              # embedded into the binary
├── docs/superpowers/         # design specs and implementation plans
├── go.mod
├── LICENSE
└── README.md
```

### Boundary rules

- `cli` never calls Docker directly; it goes through `docker` or
  `deploy`.
- `stack/laravel` never imports SSH or Docker; it returns `Files`
  and lets the caller write/exec them.
- `deploy` never knows about Laravel; it just rsyncs files, runs
  commands, and probes.

---

## Development

### Build

```bash
go build -o pier ./cmd/pier
```

### Test

```bash
# Unit tests (macOS, Linux, Windows)
go test -race -coverprofile=coverage.txt -covermode=atomic ./...

# Integration tests (Linux only — needs Docker)
go test -tags=integration -timeout 15m ./internal/deploy/...
```

CI runs unit tests on macOS, Linux, and Windows, and integration
tests on Linux only. See [`.github/workflows/`](.github/workflows/).

### Lint

```bash
golangci-lint run
```

The repo ships a [`.golangci.yml`](.golangci.yml) configuration.

### Cross-compile

```bash
GOOS=darwin  GOARCH=arm64 go build -o pier_darwin_arm64   ./cmd/pier
GOOS=linux   GOARCH=amd64 go build -o pier_linux_amd64    ./cmd/pier
GOOS=windows GOARCH=amd64 go build -o pier_windows_amd64.exe ./cmd/pier
```

### Go doc

Every package, exported type, function, and method has a Go doc
comment. Browse the full reference with:

```bash
go doc ./...
```

---

## Troubleshooting

- **"pier.toml is invalid"** — run `cat pier.toml` and check the
  section named in the error. The validator reports which field is
  at fault.
- **"ssh: handshake failed"** — run `pier status`, check
  `~/.ssh/id_ed25519` perms (`chmod 600`), and confirm the host is
  reachable.
- **"container not running"** — run `pier dev` first, then
  `pier shell`.
- **"port N in use"** — `pier dev` runs a pre-flight port probe and
  exits with code 6 when a pier-owned host port is already taken on
  `127.0.0.1`. Edit `[dev.ports]` in `pier.toml` to remap to a free
  port, then re-run `pier dev`.
- **"Connection refused" on `http://localhost:N` even though the
  container is up** — your host resolves `localhost` to `::1` (IPv6)
  before `127.0.0.1`, and pier binds dev ports to `127.0.0.1` only.
  Either point your browser at `http://127.0.0.1:N` directly, or opt
  in to LAN exposure with `[dev] bind = "0.0.0.0"` in `pier.toml` (and
  accept the LAN exposure).
- **Vite dev server unreachable / CSS not loading in browser** —
  `pier init` patches `vite.config.ts` to set
  `server: { host: true }` on first run. If your config was somehow
  missed (e.g. you initialized before this behavior shipped),
  hand-edit `vite.config.ts` to add `server: { host: true }` to the
  `defineConfig({ ... })` call.
- **"pull access denied for opcodesio/log-viewer"** — that is a
  Laravel Composer package, not a container image. The same is true
  for `nicolasbissig/laravel-dumps`. Use the `[dev.services.<name>]`
  block in `pier.toml` with a real Docker image instead.

Still stuck? [Open an issue](https://github.com/Bonnary/pier/issues).

---

## Contributing

Issues and pull requests are welcome. For anything beyond a typo
or a docs fix, please open an issue first so we can agree on the
shape of the change before code lands.

1. Fork the repo and create a feature branch.
2. Run the [manual verification checklist](#manual-verification-checklist)
   locally before pushing.
3. Keep the boundary rules (see
   [Project structure](#project-structure)) — `cli` does not import
   Docker directly, `stack/laravel` does not import SSH/Docker,
   `deploy` does not know about Laravel.
4. Make sure `go test -race ./...` and `golangci-lint run` pass.

The design spec at
[`docs/superpowers/specs/2026-07-26-pier-design.md`](docs/superpowers/specs/2026-07-26-pier-design.md)
is the source of truth for the architecture.

---

## Roadmap

- **v0.0.x** — Bug fixes, CI hardening, additional dev-sidecar
  examples, more PHP / Node / runtime versions.
- **v0.1** — Stable Laravel workflow. Documented in
  `CHANGELOG.md` when shipped.
- **v0.2+** — Open the `Stack` interface to additional frameworks
  (Node, Python, Rails) once the Laravel workflow is stable.

---

## License

MIT — see [`LICENSE`](LICENSE).

```
Copyright (c) 2026 The pier authors
```

---

## Acknowledgements

- The runtime Dockerfiles are forked from
  [Laravel Sail](https://github.com/laravel/sail). pier is not a
  fork of Sail and does not depend on the `laravel/sail` Composer
  package; the Dockerfiles are kept in sync manually inside this
  repo.
- The deploy TUI is built on
  [Bubble Tea](https://github.com/charmbracelet/bubbletea) and
  [Lip Gloss](https://github.com/charmbracelet/lipgloss).

---

<p align="center">
  <a href="https://github.com/Bonnary/pier/issues/new">Report a bug</a>
  ·
  <a href="https://github.com/Bonnary/pier/issues/new">Request a feature</a>
</p>

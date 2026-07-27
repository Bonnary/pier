# pier

Personal cross-platform CLI for Laravel Docker dev + production deploys.

`pier` turns a Laravel project into a fully provisioned dev + production
Docker stack with one-command deploys, health checks, and automatic rollback.

## Status

v0.1.0 — under active development.

## Install

```bash
go install github.com/pcnerd/pier/cmd/pier@latest
```

Or build from source:

```bash
git clone https://github.com/pcnerd/pier
cd pier
go build -o pier ./cmd/pier
sudo mv pier /usr/local/bin/
```

## Quickstart

```bash
cd my-laravel-app
pier init
pier dev
pier shell             # interactive bash in laravel.test
pier exec php artisan migrate
pier deploy production # after editing pier.toml to add [deploy.production]
```

## Commands

| Command | What it does |
|---|---|
| `pier init [path]` | Detect Laravel, write pier.toml, generate docker-compose + runtime |
| `pier dev` | Bring up the dev stack |
| `pier stop` | Stop the dev stack (volumes preserved) |
| `pier shell` | Interactive bash in the laravel.test container |
| `pier exec <cmd...>` | Run a one-off command in laravel.test |
| `pier service add <name>` | Add a service to pier.toml + docker-compose |
| `pier service remove <name>` | Remove a service from pier.toml + docker-compose |
| `pier deploy <env>` | Build, sync, up, health-check; rollback on failure |
| `pier rollback <env>` | Re-deploy the previous image tag |
| `pier status` | Show project and container status |

## Configuration

See `docs/superpowers/specs/2026-07-26-pier-design.md` for the full pier.toml
shape. Minimal example:

```toml
[project]
name = "myapp"
domain = "myapp.example.com"

[stack]
type = "laravel"
php = "8.3"
node = "22"
services = ["redis", "mailpit"]

[deploy.production]
host = "prod.example.com"
user = "deploy"
path = "/srv/myapp"
branch = "main"
```

## Manual verification checklist

Run before tagging a release.

- [ ] `pier init` on a fresh Laravel project (no existing compose)
- [ ] `pier init` on a project that already has a `docker-compose.yml` (smart-merge path; verify user services preserved)
- [ ] `pier init` on a project with an unknown top-level key in `docker-compose.yml` (warn-and-confirm path)
- [ ] `pier service add redis` and `pier service remove redis` on a project that already has them (idempotency)
- [ ] `pier init --devcontainer` in VS Code; reopen in container
- [ ] `pier shell` and `php artisan migrate` from inside
- [ ] `pier exec php artisan --version` from the host
- [ ] `pier deploy production` to a real VPS
- [ ] `pier rollback production` after a deliberate bad deploy

## Out of scope (v1)

- Multi-stack (Node, Python, Rails)
- Cloud-provider deploys (AWS, DO, Hetzner)
- Secret-management integrations (1Password, Vault)
- Auto-scaling, multi-server, blue/green, canary
- Per-tool command wrappers (`pier artisan`, `pier mysql`, etc.) — use `pier shell` / `pier exec`
- `pier share`, `pier open`
- Agent env forwarding into containers

## Troubleshooting

- **"pier.toml is invalid"** — run `cat pier.toml` and check the section that's named in the error. The validator reports which field.
- **"ssh: handshake failed"** — check `pier status`, your `~/.ssh/id_ed25519` perms (`chmod 600`), and that the host is reachable.
- **"container not running"** — run `pier dev` first, then `pier shell`.

## License

MIT (see `LICENSE`).

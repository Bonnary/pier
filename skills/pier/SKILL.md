---
name: pier
description: Use when working in a Laravel project that has a pier.toml file, or when setting up a Docker dev environment or deploying a Laravel app to a VPS. Covers the pier CLI commands, pier.toml config, and the canonical init-to-deploy workflow.
---

# pier

## Overview

`pier` is a single self-contained Go binary that turns a Laravel project
into a Docker dev + production stack: from a fresh repo to a production
deploy with health checks and automatic rollback. Config lives in
`pier.toml`. pier owns the Docker Compose lifecycle for the project — do
not hand-drive `docker compose` for pier-managed environments.

There are unrelated tools named "pier" on the internet. The one covered
here is **Bonnary/pier** (Laravel dev + deploy, Go binary, no daemon, no
telemetry).

## When to Use

- The user's Laravel repo contains a `pier.toml`
- Setting up a Laravel Docker dev environment from scratch
- Deploying a Laravel app to a VPS / production server
- Running artisan commands inside the app container (local or remote)

Do NOT use when the project has no `composer.json` requiring
`laravel/framework` and no `artisan` file — `pier init` will refuse.

## Canonical Workflow

The workflow, in order:

1. **`pier init`** — detect Laravel, write `pier.toml`, generate
   `docker-compose.yml` + runtime Dockerfiles, patch `vite.config.ts`
   (`server: { host: true }`). Prompts for the deploy target
   (host/user/path/branch) and build machine. Idempotent: re-running
   smart-merges into an existing compose file, preserving hand edits.
2. **`pier dev`** — bring up the dev stack. Pre-flight port probe;
   exits with code 6 if a pier-owned host port is taken.
3. **`pier exec php artisan migrate`** — one-off commands in the
   `laravel.test` container. `pier shell` for interactive bash.
4. **`pier bootstrap production`** — one-time server provisioning:
   installs Docker Engine + compose plugin over SSH, grants the deploy
   user passwordless docker access, creates the deploy path. Prompts
   once for the deploy user's sudo password.
5. **`pier deploy production`** — build, sync, up, health-check with
   automatic rollback on failure (live TUI progress).
6. **`pier rollback production`** — re-deploy the previous image tag
   on demand.
7. **`pier status [env]`** — project/container status; with an env
   name, probes the remote host over SSH.

## Command Reference

| Command | Purpose |
| --- | --- |
| `pier init [path] [--devcontainer]` | Scaffold `pier.toml`, compose, runtime. `--builder` / `--host` / `--user` / `--path` / `--build-host` / `--build-user` / `--build-path` skip the prompts. |
| `pier dev` / `pier stop` | Start / stop the dev stack (volumes preserved on stop). |
| `pier shell [env]` | Interactive bash in `laravel.test`; with an env name, in the remote `app` container (PTY, resize forwarding). |
| `pier exec [env] <cmd...>` | One-off command in `laravel.test`, or in the remote `app` container when the first arg names a deploy env. Exit codes propagate. |
| `pier service [env]` | Interactive picker editing `[stack].services` (dev) or `[deploy.<env>].services` (prod override). |
| `pier bootstrap [env...] [--all] [--force]` | Provision servers: Docker + compose plugin, docker access for deploy user, create deploy path. |
| `pier deploy <env>` | Full deploy pipeline with TUI; rolls back automatically if up / `after_deploy` / health fails. |
| `pier rollback <env>` | Re-deploy the previous image tag. |
| `pier status [env]` | Containers, disk, health, last deploy (remote over SSH). |

Global flags: `--config <path>` (pier.toml path), `--json` (one JSON
object per line per deploy event), `--verbose` (unfiltered build output).

## Configuration (`pier.toml`)

| Section | Meaning |
| --- | --- |
| `[project]` | `name`, `domain`. |
| `[stack]` | `type`, `php`, `node`, `services` (the sidecar list). |
| `[dev]` | `bind` (`0.0.0.0` exposes dev ports to LAN), `[dev.ports]` (`laravel`, `vite`, `redis`, ...). |
| `[deploy.<env>]` | `host`, `user`, `path`, `branch`, optional `services` override, `tls` (keep `false` — cert provisioning not shipped), `ports` overrides, `before_deploy` / `after_deploy` hook lists. |
| `[deploy.<env>].builder` | Where the image is built: `host_server` (default), `local_machine`, or `build_server` (needs `build_host` / `build_user` / `build_path`). |
| `[dev.services.<name>]` | Dev-only sidecars (log viewers, Reverb, ...) — never appear in the production compose. |

Env files: `.env` (dev) and `.env.production` (prod — written as
placeholders; fill in real secrets before deploying). pier syncs
`.env.production` from the local machine to the server on every deploy
(the only `.env.*` file that ships — everything else is excluded), and
aborts the deploy if it is missing (generating a fresh template to
fill in). Keep it out of git. `.env.production.example` is the
hand-managed reference template.

Deploy hooks: each entry runs in the app container via
`docker compose exec -T app`. `before_deploy` runs while the old
release is still serving; `after_deploy` runs after
`docker compose up --wait` and before the health probe. Commands run in
order and stop at the first failure (deploy aborts, exit code 7). On a
first deploy there is no app container yet, so `before_deploy` is
skipped — put first-run setup in `after_deploy`. Migrations belong in
`after_deploy`.

## Common Mistakes

| Mistake | Fix |
| --- | --- |
| Hand-editing `docker-compose.yml` as the source of truth | `pier.toml` is the source; pier smart-merges and preserves hand edits, but use `pier service` to change services. |
| Manually provisioning the VPS (php-fpm, nginx, ssh keys, docker) | Use `pier bootstrap <env>` — that is what it is for. Deploy user needs password-protected sudo once. |
| Running raw `docker compose` against pier-managed environments | pier owns the lifecycle: use `pier dev` / `pier stop` / `pier exec`. |
| Deploying with placeholder `.env.production` values | Fill real secrets first; an empty `APP_KEY` breaks the app (generate with `pier exec php artisan key:generate`). |
| Committing `.env.production` (or deleting it locally) | It holds production secrets and is synced from your local machine to the server — keep it local, out of git; the deploy aborts if it is missing. |
| Running migrations by SSHing into the server | Put `after_deploy = ["php artisan migrate --force"]` in `[deploy.<env>]`; hook failures roll back the deploy loudly. |
| Thinking the build machine is the deploy env | `build_server` is not a deploy env — no `[deploy.build]` section; `pier shell build` errors with `no [deploy.build] section`. |
| Searching the web for "pier" docs | The GitHub `pier-cli/pier` is an unrelated script manager. This skill and the repo README are the reference. |
| "Connection refused" at `http://localhost:N` while the container is up | Host resolves `localhost` to IPv6; use `http://127.0.0.1:N` or set `[dev] bind = "0.0.0.0"` (LAN exposure). |

## Troubleshooting

- **"server not bootstrapped"** on deploy → run `pier bootstrap <env>` once.
- **"deploy path ... is not writable"** → deploy path missing; re-run `pier bootstrap <env>` to create it.
- **"ssh: handshake failed"** → check `~/.ssh/id_ed25519` permissions
  (`chmod 600`); override key with `$DEPLOY_SSH_KEY`. Password-only
  servers get an automatic interactive prompt.
- **"port N in use"** → a pier-owned port is taken (exit code 6);
  remap it in `[dev.ports]`.
- **"container not running"** → run `pier dev` first.

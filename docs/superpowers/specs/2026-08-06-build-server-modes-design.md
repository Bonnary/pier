# Build Server Modes Design

Date: 2026-08-06
Status: Approved (design review)

## Problem

`pier deploy <env>` always builds the production image on the deploy
host itself: the source tree is synced over SFTP, `docker compose
build` runs there, and the host both builds and runs the stack. Some
users want the build to happen elsewhere — on their dev machine
(`local_machine`) or on a dedicated remote build machine
(`build_server`) — with only the finished image shipped to the host.
Shipping the image must go over SSH (pier's existing transport; no
registry dependency), and rollback/health/hooks must keep working
regardless of where the image was built.

## Goals

1. A per-env `builder` setting in pier.toml with three values:
   `host_server` (default, today's behavior), `local_machine`,
   `build_server`.
2. In `local_machine` / `build_server` modes the host receives only
   the deploy files it needs to run the stack (compose, env, nginx
   conf) — never the full source tree.
3. The built image travels from build side to host as a streamed
   `docker save` → `docker load` pipe through pier's own SSH
   connection(s) — no temp tar files, no cross-server SSH keys.
4. A `pier buildmode <env>` command edits the builder setting with an
   interactive picker.
5. `pier bootstrap <env>` provisions both machines when the env uses
   `build_server`.
6. Real git SHA image tags replace the hardcoded `"gitsha"`
   placeholder; the never-called `Tag()` step is wired in so rollback
   works in every mode.
7. Deploy TUI shows a `transfer` phase in the image modes.

## 1. Config model

New optional fields on `DeployConfig` (internal/config/config.go):

```toml
[deploy.production]
builder     = "build_server"    # "host_server" (default) | "local_machine" | "build_server"
build_host  = "build.example.com"  # required when builder = "build_server"
build_user  = "deploy"
build_path  = "/srv/myapp"
```

- `Builder string toml:"builder"` — empty means `host_server`
  (existing configs unchanged). Valid values: `host_server`,
  `local_machine`, `build_server`.
- `BuildHost`, `BuildUser`, `BuildPath` — the build server SSH target
  and sync path; required iff `Builder == "build_server"`.
- Validation (internal/config/parse.go): unknown `builder` value is
  an invalid-config error; `build_host`/`build_user`/`build_path`
  must all be non-empty exactly when `builder = "build_server"`.
- The existing "configured ⇒ host/user/path/branch required" rule is
  untouched — `builder` is independent of it.

## 2. Image tag

The hardcoded literal `"gitsha"` (internal/deploy/deploy.go, passed to
`Build` and written to state.json) is replaced by a real tag computed
once per deploy:

- `git rev-parse --short HEAD` when the project is a git repo with a
  HEAD; fallback to a timestamp
  (`time.Now().Format("20060102150405")`) when not (no git repo, no
  HEAD, or git unavailable).
- The tag is used as `<project>:<sha>`: the build output image, the
  transferred image, and `state.json` `Current`/`Previous`.
- Fix the never-called `Tag()` (internal/deploy/build.go): in
  `host_server` mode, after the remote build, tag
  `project:latest` → `project:<sha>` and → `project:current`.
  Today's rollback retags `project:<previous>` — a tag that is never
  created — so the wiring fixes an existing rollback bug.

## 3. Builder modes — pipeline flow

### 3.1 `host_server` (unchanged behavior)

preflight (dial host, probe, ensure path) → render (build-variant
compose, today's output) → full source sync to host → remote
`docker compose build --pull` (today's `Build`) → **new**: `Tag()`
(`latest` → `<sha>` and → `current`) → before_deploy → up →
after_deploy → health → commit.

### 3.2 `local_machine`

1. preflight: dial host, probe bootstrap, ensure path (no second
   machine).
2. render: **image-variant** compose — app service is
   `image: project:current` with no `build:` key.
3. sync: deploy files only → host (`docker-compose.prod.yml`,
   `.env.production`, `docker/nginx/default.conf`).
4. build: local `docker build --pull
   -f docker/<php>/Dockerfile.prod --build-arg WWWUSER=1337
   --build-arg WWWGROUP=1337 -t project:<sha> .` (working tree as
   context).
5. transfer: local `docker save project:<sha>` stdout piped through
   pier's SSH connection into the host session's `docker load` stdin;
   then host `docker tag project:<sha> project:current`.
6. before_deploy → up → after_deploy → health → commit: unchanged,
   all host-side.

### 3.3 `build_server`

1. preflight: dial **both** the host and the build server; probe both
   bootstrapped; ensure the host path and `build_path`.
2. render: image-variant compose.
3. sync: full source (today's excludes) → build server at
   `build_path`; deploy files only → host.
4. build: the same `docker build --pull ... -t project:<sha> .`
   command run on the build server inside the synced source.
5. transfer: build-server session `docker save project:<sha>` stdout
   piped into host session `docker load` stdin — pier opens both
   connections (already dialed) and pipes between the two sessions;
   then host `docker tag project:<sha> project:current`.
6. before_deploy → up → after_deploy → health → commit: unchanged.

### Shared

- Hooks, up (`--wait --wait-timeout 120 --remove-orphans`), health
  probe, state.json, and `pier rollback` always target the host.
  Rollback (`docker tag project:<previous> project:current` + up)
  works in every mode: `host_server` creates sha-tagged images after
  the Tag fix; the image modes load them onto the host.
- `up` in image modes requires `project:current` to exist before it
  runs (transfer precedes up). If the image is missing, compose fails
  loudly instead of pulling from a registry.
- The existing render guard ("no local `.env.production` → generate a
  template and abort before sync") is unchanged.

## 4. Transfer mechanics

New binary-safe primitive in internal/deploy/ssh.go:

- `Client.StreamIn(ctx, cmd string, in io.Reader, onLine func(string))
  error` — runs a remote command with stdin fed from an arbitrary
  reader; streams stderr lines to the caller for progress and error
  tails. The stdin path must not go through `bufio.Scanner` (existing
  `RunStdin` buffers a whole string; `RunStream` line-scans stdout —
  both unsuitable for a binary tar stream).

`local_machine`: `exec.Command("docker", "save", "project:<sha>")`
locally, its stdout pipe wired into the host session's stdin via
`StreamIn`. No temp files anywhere.

`build_server`: both connections are already dialed in preflight.
Two sessions start before copying; a goroutine `io.Copy`s the build
server's save stdout into the host's load stdin; the first failure on
either side cancels the other and aborts with that side's stderr tail.

Failure semantics (no new rollback machinery):

- save/load fails → nothing on the host changed (`current` still
  points at the old image) → deploy aborts, old release keeps
  serving, no rollback needed.
- load succeeds but retag or up fails → existing rollback path
  (previous → `current`, up) restores the old release.

Logging/TUI: a `transfer` phase with a byte-counting `io.Copy` —
coarse MB-streamed progress in the TUI; `docker load` stderr lines
stream to the logger; final line `image project:<sha> loaded on
<host>`.

## 5. Render and sync sets

`renderProdCompose` (internal/stack/laravel/prod.go) already receives
`cfg` and `env`, so it reads `[deploy.<env>].builder` directly:

- `host_server`: unchanged — app service `build:` context +
  `image: project:latest`.
- `local_machine` / `build_server`: app service
  `image: project:current` only, no `build:` key. Sidecars, volumes,
  ports, TLS, nginx identical.

The hand-edit-preserving merge (merge_prod.go) merges whatever
variant the generator produced; no merge changes needed.

Two sync filter sets (internal/deploy/syncfilter.go):

- Full source = today's `rsyncExcludes` — host in `host_server`;
  build server in `build_server`.
- New `deployFilesOnly`: exactly `docker-compose.prod.yml`,
  `.env.production`, `docker/nginx/default.conf`, everything else
  excluded — host in both image modes (the nginx conf ships because
  the webserver service bind-mounts it).

## 6. Bootstrap

`pier bootstrap <env>` when the env sets `builder = "build_server"`:

- Provisions the host first (as today), then the build server
  (Docker + compose plugin install, sudo prompt, then create/chown
  `build_path`), reporting each independently:
  `production (host): done` / `production (build server): done`.
- Re-running skips each already-provisioned machine independently
  (`--force` re-provisions both).
- `host_server` / `local_machine` envs: bootstrap unchanged
  (host only). In `local_machine` mode nothing is provisioned
  remotely beyond the host.

## 7. Commands

New `pier buildmode <env>`:

- Interactive picker with the three builder values (current value
  pre-ticked), then prompts for `build_host`, `build_user`,
  `build_path` when `build_server` is chosen.
- Writes the `[deploy.<env>]` fields with the same pier.toml edit
  machinery as `pier service`.
- `pier init` scaffolds `builder = "host_server"` (explicit) plus
  commented `build_host` / `build_user` / `build_path` keys.

`status`, `shell`, `exec` always target the host — unchanged in every
mode.

## 8. Errors and TUI

- Build failures in `build_server` mode surface the build server host
  in the error (the existing `RemoteBuildError(host, err)` pattern
  with the build server's host).
- The deploy TUI gains a `transfer` phase, rendered only in the image
  modes. Phase list per mode: preflight, render, sync, build,
  [transfer], before_deploy, up, after_deploy, health, commit.

## 9. Edge cases

- Same SHA deployed twice: load + retag + up are no-ops —
  idempotent.
- Switching `builder` between deploys: rollback still works because
  previous images live on the host in every mode.
- Non-git repo / no HEAD: timestamp fallback tag.
- Working tree with uncommitted changes: same as today (the tree is
  what gets built; `branch` stays validation-only). Documented, not
  changed.
- Dev machine on Windows: local `docker build` / `docker save` pipe
  over SSH works the same.
- `bootstrap` in `build_server` mode prompts for sudo on each machine
  in sequence.

## 10. Testing

Unit (no Docker/SSH):

- config: invalid `builder` rejected; `build_*` required iff
  `build_server`; absent key defaults to `host_server`.
- render: image-only vs build variant per mode; hand-edit merge
  preserves user edits in both variants.
- sha: `git rev-parse --short HEAD` with timestamp fallback.
- syncfilter: `deployFilesOnly` ships exactly the three files; full
  set unchanged.
- pipeline: phase selection per mode with fake runners; `StreamIn`
  piping between two fake clients.
- existing tests updated where the missing `Tag()` call changes
  expectations.

Integration (`-tags=integration`, Linux, Docker):

- `host_server` end-to-end unchanged.
- New `local_machine` end-to-end against a localhost SSH target.

## 11. Docs

- README: `builder` config table, `pier buildmode` command, bootstrap
  both-machines behavior, transfer phase, per-mode flow description.
- CHANGELOG entry.

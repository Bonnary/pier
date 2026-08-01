# Pier — `pier bootstrap` Server Provisioning — Design

**Date:** 2026-08-01
**Status:** Draft (brainstorming), pending spec review

## Problem

`pier deploy` assumes the remote host already has Docker Engine +
the `docker compose` plugin installed and reachable by the deploy
user without a password (`internal/deploy/up.go:12` runs
`docker compose ... up -d` with no `sudo`). Fresh VPSs rarely meet
that bar:

- SSH key auth proves the login identity, but `sudo` is a separate
  authentication boundary — so `ssh host@server` works with just a
  key, while `apt install docker` still prompts for the user's
  password.
- When the deploy user has password-protected sudo, any privileged
  command pier runs over a non-PTY SSH session either hangs on a
  prompt it can't answer or fails outright.

Coolify's installer "solves" this by requiring the user to run it
as root/sudo in their own terminal (`install.sh:27-30` — `if [ $EUID
!= 0 ]; then exit`), i.e. a human types the password once, locally,
at install time. That model only works for a local script; pier runs
commands remotely over SSH, so it needs its own one-time
provisioning flow: prompt for the sudo password exactly once,
provision the server, and then make the privilege persistent so
every future `pier` command is non-interactive.

## Goals

1. New command `pier bootstrap [env...]` that installs Docker
   Engine + `docker compose` plugin on a remote host and grants the
   deploy user passwordless Docker access.
2. Target selection: explicit env args, `--all`, or a Bubble Tea
   single-picker when nothing is given (mirroring the existing
   `pier service add` convention, `internal/cli/service.go`).
3. The sudo password is entered once per env via a hidden local
   prompt and is never stored in `pier.toml`, on disk, or in shell
   history.
4. Idempotent: probe each server first; skip already-bootstrapped
   servers unless `--force`.
5. `pier deploy` / `rollback` stay non-interactive and fail fast
   with a clear message when the server was never bootstrapped,
   instead of hanging on a hidden sudo prompt.
6. The existing deploy pipeline (up/build/health/rollback) does not
   need sudo-wrapping — after bootstrap, Docker access is
   passwordless, so no existing command changes.

## Non-goals

- Managing Docker itself after install (upgrades, restarts, daemon
  config). `get.docker.com` and the host package manager own that.
- Storing any password in `pier.toml` or an env file. YAGNI — the
  one-time interactive prompt covers the bootstrap use case.
- Bootstrapping via the Docker *rootless* install (`get.docker.com
  --rootless`). The standard root install matches pier's compose
  usage and every documented server setup. Rootless is a follow-up
  if anyone asks.
- Supporting hosts where the deploy user is not in `sudoers` at
  all — there is no root path for pier to use. pier detects this and
  errors with instructions.
- Windows targets for bootstrap. Bootstrap runs over SSH from any
  platform, but the *remote* provisioning assumes a Linux
  systemd/apt-style host (like the rest of the deploy pipeline).
- PTY passthrough of the sudo prompt for every command. Rejected —
  `deploy` must stay scriptable/CI-able; the one-time `sudo -S`
  bootstrap keeps interactivity confined to `pier bootstrap`.

## Design

### Command UX

```
pier bootstrap [env...] [--all] [--force]
```

- `pier bootstrap` — no args → `tui.NewSinglePicker` listing every
  env in `pier.toml` (sorted alphabetically, so the list is
  deterministic), labeled `production (prod.example.com)`.
  Cancel → clean abort, exit 0.
- `pier bootstrap stage production` — those envs, in order.
- `pier bootstrap --all` — every env in `pier.toml`, sorted
  alphabetically (Go maps don't preserve TOML order; the picker and
  `--all` must be deterministic).
- Unknown env name → same error style as deploy
  (`internal/cli/deploy.go:31`): `no [deploy.<env>] section in
  pier.toml`.
- Env with `--all` and positional args combined → error.
- Per env, in order: **probe → (skip | prompt → provision →
  verify)**. If probe says already bootstrapped, print
  `production: already bootstrapped — skipping` and continue to the
  next env. `--force` skips the probe and re-provisions.
- The password prompt appears once per env, immediately before that
  env's provisioning, and only when provisioning is actually needed
  (already-bootstrapped envs never prompt).

### Password mechanics

Hidden local prompt (no echo, like `sudo`'s own prompt). pier never
reads `pier.toml` for a password and never writes one back. Flow
per env:

1. Validate with `sudo -S -v` (password on stdin; this also
   refreshes the remote sudo timestamp).
2. If validation fails and stderr indicates the user is not in
   `sudoers` (not a wrong-password error): abort with
   `deploy user <user> has no sudo rights on <host>; add them to
   sudoers first or bootstrap as a different user`.
3. Wrong password → one re-prompt, then error.
4. Provisioning commands run as `sudo -S <cmd>`, password piped on
   stdin. Never `-p` on the command line (would leak into remote
   process listings), never echoed.

### Provisioning steps (per server, in order)

1. **Install Docker Engine + compose plugin:**
   `curl -fsSL https://get.docker.com | sh` run under sudo. Installs
   `docker-ce`, the `docker compose` plugin, and creates the `docker`
   group. (Same official source Coolify's installer and most VPS
   setup guides use.)
2. **Grant the deploy user Docker access:**
   `usermod -aG docker <user>` under sudo. This is the privilege
   persistence — after this, `docker` and `docker compose` need no
   sudo, so the existing pipeline commands
   (`internal/deploy/up.go`, `build.go`, `health.go`) run unchanged.
   Group membership applies to new SSH sessions automatically, so
   the follow-up verify (a fresh session) sees it.
3. **Verify** (fresh SSH command, no sudo): `docker info` and
   `docker compose version` both succeed. Failure → non-zero exit
   naming the failed step.

The "already bootstrapped" probe (before prompting/provisioning):
`command -v docker` and `docker info` both succeed for the deploy
user without sudo. That is the skip condition. (`--force` bypasses
it; the install script and `usermod` are both idempotent, so
re-running is safe.)

### Deploy-side integration

`pier deploy` and `pier rollback` get one fail-fast probe, inside
the existing preflight phase (`internal/deploy/deploy.go:41`) right
after `Dial`: run `command -v docker && docker info` for the deploy
user. On failure, abort with:

```
server not bootstrapped for <env> — run `pier bootstrap <env>` first
```

This replaces the current silent hang-on-sudo-prompt failure mode.
The probe costs one SSH round-trip per deploy.

### Code layout & boundary rules

- `internal/deploy/bootstrap.go` — probe, sudo-validate, provision,
  verify. All remote steps run through the existing `runner`
  interface (`internal/deploy/ssh.go:135`), so unit tests use the
  same fake as `ssh_test.go`.
- `internal/cli/bootstrap.go` — cobra command, env resolution
  (args → `--all` → picker), hidden password prompt.
- Password prompt is an injectable function in `cli`
  (like `osGetenv`/`osUserHomeDir` in `internal/cli/helpers.go:12`),
  so tests inject a canned password and CI never prompts.
- Boundary rules hold: `cli` never runs SSH directly; `deploy` never
  reads the TUI.

### Files changed

- `internal/deploy/bootstrap.go` — new (probe/validate/provision/
  verify; `ErrNotBootstrapped` sentinel for the deploy-side check).
- `internal/deploy/deploy.go` — preflight calls the probe; clear
  bootstrap-missing error.
- `internal/cli/bootstrap.go` — new (command, env resolution,
  picker, prompt).
- `internal/cli/root.go` — register `bootstrap` command.
- `internal/cli/helpers.go` — add injectable `readPassword` helper
  (hidden stdin read).
- `README.md` — command table entry, prerequisites update (Docker no
  longer required on the remote *before* bootstrap), manual
  verification checklist entry.
- `docs/superpowers/specs/2026-08-01-bootstrap-design.md` — this
  spec.

### Test plan

| Test | Asserts |
|---|---|
| `TestBootstrapProbeBootstrapped` (fake runner) | `command -v docker` + `docker info` OK → skip, no password prompt, no provision commands |
| `TestBootstrapProbeNotBootstrapped` (fake runner) | Probe fails → prompts, validates with `sudo -S -v`, runs install + `usermod`, verifies |
| `TestBootstrapForceReprovisions` (fake runner) | `--force` → probe skipped, provision runs even when bootstrapped |
| `TestBootstrapWrongPassword` (fake runner) | `sudo -S -v` fails with wrong password → one re-prompt → error |
| `TestBootstrapUserNotInSudoers` (fake runner) | `sudo` error is not a wrong-password error → abort with "add to sudoers" message |
| `TestBootstrapEnvResolution` | args only → those envs; `--all` → all envs; both → error; unknown env → `no [deploy.<env>] section` error |
| `TestBootstrapDeployProbeFailFast` (fake runner) | `deploy`'s preflight probe fails → `ErrNotBootstrapped` with "run pier bootstrap" message |
| `TestDeployProbeBootstrappedOK` (fake runner) | `deploy` preflight probe passes → pipeline proceeds (existing tests keep passing) |
| Integration (`-tags=integration`, Linux): `TestBootstrapRealServer` | Real box: install → verify → idempotent re-run skips → `--force` re-runs |

Manual verification checklist (added to README):
- `pier bootstrap` on a fresh VPS with key-auth + password sudo →
  hidden prompt, docker installed, `docker info` works for the
  deploy user afterwards; re-run prints `already bootstrapped —
  skipping`; `pier bootstrap --force` re-provisions; `pier deploy
  <env>` works without any password prompt; on an un-bootstrapped
  server `pier deploy` fails fast with the bootstrap hint.

## Rollback

There is no pier-managed state to roll back: the one-time `usermod`
membership and the Docker packages are host state. Reverting means
removing the user from the `docker` group
(`gpasswd -d <user> docker`) and uninstalling Docker via the host
package manager. pier itself is not persisted and keeps working with
pre-existing servers (the deploy-side probe passes for them).

## Out of scope (deferred)

- **Rootless Docker install.** `--rootless` variant of the install
  script; follow-up if requested.
- **Windows/macOS remote targets.** Remote provisioning assumes a
  Linux host, consistent with the rest of the deploy pipeline.
- **Docker upgrades / daemon management post-install.** Host package
  manager's job.
- **Non-sudoers deploy user.** No root path means pier can't
  provision; the error message hands the admin the fix.
- **Password in config or env file.** Explicitly rejected in Goals;
  prompts only.

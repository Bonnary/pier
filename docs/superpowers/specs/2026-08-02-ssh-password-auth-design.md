# Design: SSH password auth support

Date: 2026-08-02

## Problem

pier only supports key-file authentication: `Dial`
(internal/deploy/ssh.go:52) registers `ssh.PublicKeys(signer)` as the
only auth method, so a remote production server that requires a
password (no key installed) fails every remote command at preflight
with exit code 2 and a hint to fix key perms. There is no way to use
a password-only server at all.

Two independent gaps:

1. **Auth:** `Dial` has no password path — no `ssh.Password`, no
   keyboard-interactive, so password-only hosts are unusable.
2. **Sync:** the deploy pipeline syncs files with
   `rsync -az -e ssh` (internal/deploy/rsync.go:64), which shells out
   to the system `ssh` binary. That binary cannot share a password
   pier already used, so password-auth sync would need a second
   interactive prompt (and a local `ssh`/`rsync` install).

## Decisions

- Password auth is a **fallback**: try the configured key first; only
  if the server rejects all keys (auth-class failure) do we prompt
  for a password. Key-auth users never see a prompt; password-only
  servers work with zero configuration.
- The password is **never stored**: supplied interactively via a
  prompt (echo disabled, on stderr) or pre-supplied for tests.
  No pier.toml schema changes.
- File sync switches from the `rsync` subprocess to **SFTP over
  pier's own SSH connection** (`github.com/pkg/sftp`). One prompt
  covers the whole command; the local `rsync`/`ssh` binary dependency
  for sync disappears (benefits key-auth users too).
- The fallback is shared by every remote command (`deploy`,
  `rollback`, `status`, `bootstrap`) because they all dial through
  `Dial`.

## Section 1: Auth fallback in Dial

`SSHConfig` (internal/deploy/ssh.go:21) gains two fields:

- `PasswordPrompt func() (string, error)` — nil means "never
  prompt" (tests, non-interactive callers).
- `Password string` — optional pre-supplied password (tests bypass
  the prompt).

`Dial` flow:

1. Validate + read the key exactly as today (empty host / key path
   still fail fast with `ErrPreflight`).
2. Try `ssh.NewClientConn` with `ssh.PublicKeys(signer)`.
3. On success → return the client as today.
4. On failure, classify: only an **auth-class** failure
   (`*ssh.ServerAuthError`, i.e. the server rejected the offered
   methods) with an available password source triggers the fallback.
   Network, DNS, and TCP errors never prompt.
5. Obtain the password (`Password`, else `PasswordPrompt()`). If the
   prompt returns an error (Ctrl+C), return `ErrAborted` so the CLI
   exits 130 ("aborted"), not 2.
6. Retry once with `ssh.Password(pw)`. If the server advertises
   `keyboard-interactive` only (PAM), the retry uses a
   `ssh.KeyboardInteractive` responder that answers once with the
   same password.
7. If the retry fails, return the original handshake error wrapped in
   `ErrPreflight` exactly as today (`error[config]`, exit 2, existing
   hint).

## Section 2: SFTP sync

Add `github.com/pkg/sftp` to go.mod. `*Client` gains:

```
func (c *Client) SyncDir(ctx context.Context, local, remote string,
                          excludes []string) error
```

Implementation:

1. Walk `local/` recursively, sorted, applying the existing
   `rsyncExcludes` semantics (internal/deploy/rsync.go:46), ported to
   a small pure match function. The include-override rule
   (`.env.*` excluded but `.env.production` kept) is preserved.
2. `mkdirAll` remote directories; upload each file over SFTP,
   preserving mode bits and mtime where practical.
3. Pipeline phase 3 (internal/deploy/deploy.go:78) becomes
   `client.SyncDir(ctx, ".", p.DeployEnv.Path, rsyncExcludes)` — the
   client is already dialed in preflight.

`rsync.go`'s subprocess path is removed; any tests referencing
`Sync`/`defaultRunner` move to the SFTP path.

## Section 3: CLI wiring, prompts, errors

- One shared CLI helper constructs `SSHConfig` for the four call
  sites (internal/cli/deploy.go:39, rollback.go:33, status.go:69,
  bootstrap.go:62), filling `Host/User/KeyPath` and setting
  `PasswordPrompt` to a new `readSSHPassword`, modeled on
  `readSudoPassword` (internal/cli/helpers.go:20): prompt on stderr,
  echo disabled, so `--json` stdout stays clean.
- Prompt text: `SSH password for deploy@host: `.
- `bootstrap` on a password-only host prompts twice (SSH password,
  then the existing sudo password) — two distinct prompts.
- Error mapping:
  - Both key and password rejected → today's
    `error[config]: ... preflight failed: handshake ...` + hint
    (unchanged).
  - Prompt cancelled → `ExitAborted` (130), consistent with TUI
    aborts.
  - Later-phase failures on a password-authed host → remote hints
    exactly as today (`RemoteHost` stamped).
- No pier.toml schema changes.

## Section 4: Testing and docs

- Unit tests with an in-process SSH server (pure Go, `x/crypto/ssh`,
  no Docker):
  - Key works → no prompt.
  - Key rejected + prompt returns password → success.
  - Key rejected + no prompt wired → preflight error.
  - Prompt aborts → `ErrAborted`.
  - Both rejected → preflight error.
  - Keyboard-interactive-only server → success.
- `SyncDir`: exclude matching as a pure function with table tests;
  walk/upload tested against an in-process `sftp` server; mode
  preservation asserted.
- Existing tests referencing `Sync`/`defaultRunner` updated to the
  SFTP path.
- Docs: README Prerequisites ("OpenSSH client — ssh, rsync-over-ssh"
  → SSH key or password; SFTP-based sync), Troubleshooting handshake
  hint, CHANGELOG entry, and this spec.
- Go doc comments on `Dial`, `SyncDir`, and prompt helpers per repo
  convention.

## Out of scope

- Storing passwords in pier.toml or in any state file.
- Non-interactive password injection via env var (only the prompt and
  the test seam `Password` field).
- Strict host-key verification (already deferred in v1).
- Replacing the local `rsync` binary for any non-pier workflow.

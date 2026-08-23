# VirtioFS check on Windows init — design

Date: 2026-08-23
Status: approved

## Goal

On Windows, Docker bind mounts of the project (on a Windows drive like
`C:\` / `D:\`) cross the Windows↔WSL2 VM boundary over 9P and are slow.
`pier init` should detect the Windows case and offer to enable WSL-level
VirtioFS (`.wslconfig`), which makes those mounts near-native speed. The
user must consent at every mutating step; nothing is changed without a
`[Y/n]` answer.

## Decisions (from brainstorming)

- **Windows only.** Non-Windows builds compile the check but it no-ops
  (`runtime.GOOS` guard, no build tags).
- **`pier init` only**, asked before the TUI/prompt sequence (right after
  Laravel detection). Not in `pier dev` — init is the one-shot setup where
  environment guidance belongs.
- **Skip without prompting when** the project already lives under
  `\\wsl$\` (mounts are already native — the user's own insight) or
  `wsl.exe` is unavailable.
- **WSL ≥ 2.7.1 required.** Verified against microsoft/WSL sources: 2.7.1 is
  the first release with VirtioFS for Windows drive mounts (PR #13880, merged
  Dec 12 2025, released Mar 24 2026). Older WSL gets an offer to run
  `wsl --update` (also `[Y/n]`).
- **Both keys required.** The WSL config validator forces `virtiofs` off when
  `virtio` is not enabled, so `.wslconfig` gets `virtio=true` **and**
  `virtiofs=true`. The community doc omits `virtio`; pier does not.
- **Merge, never clobber.** Existing `.wslconfig` content (`memory=`,
  `processors=`, etc.) is preserved. Existing `virtio` / `virtiofs` keys are
  never overwritten — the user's value wins. Only missing keys are added.
- **No Windows 11 check.** If the OS doesn't support VirtioFS, the setting is
  simply ignored by WSL; the check adds noise without preventing anything.
- **Print instructions only** for the restart (close Docker Desktop →
  `wsl --shutdown` → relaunch Docker Desktop). pier does not kill the user's
  running WSL/distro containers.
- **Caveat surfaced in the prompt**: VirtioFS is experimental — known
  file-permission/ownership quirks, and strict databases (PostgreSQL/MySQL)
  may fail to initialize on host-directory bind mounts (use named volumes).
- **No `--no-check` flag** — answering `n` at the prompt is the escape.

## 1. Flow (`internal/cli/virtiofs.go`, new)

`maybeEnableVirtioFS(stdout, stdin io.Writer/Reader, projectAbs string, run func(...))`:

1. If `runtime.GOOS != "windows"` → return.
2. If `projectAbs` starts with `\\wsl$\` or `\\wsl.localhost\` → return
   (already native).
3. `wsl --version` via `exec.Command`; if it fails (no WSL) → return.
   Parse the `WSL version: x.y.z` line as semver.
4. If version < 2.7.1 → print explanation, ask `Run wsl --update now? [Y/n]`
   (default `n` — running the update is the one non-trivial side effect); on
   yes run `wsl --update` and re-read the version; if still < 2.7.1, print
   "update WSL to 2.7.1+ and rerun pier init" and return.
5. Read `%USERPROFILE%\.wslconfig`. If `[wsl2] virtiofs=true` already present
   → print "already enabled", return (no prompt).
6. Explain the fix + caveat, ask `Enable VirtioFS ... [Y/n]` (default `n`).
7. On yes, merge `[wsl2] virtio=true` / `virtiofs=true` into the file:
   - no file → create with the two keys under `[wsl2]`;
   - existing `[wsl2]` section → insert missing keys after the section
     header line;
   - `[wsl2]` section missing → append a new section;
   - keys already present under `[wsl2]` → leave them untouched.
8. Print the restart instructions.

Exec, the GOOS check, and file access go through small package-level seams
(`isWindows`, `wslVersionCmd`, `wslconfigPath`, `readFile`, `writeFile`) so
tests can inject fake `wsl --version` output and `.wslconfig` contents
without a real Windows box — including the non-Windows skip path on Linux CI.

## 2. Code changes

- `internal/cli/virtiofs.go` — new file as above, `maybeEnableVirtioFS`.
- `internal/cli/init.go` — call `maybeEnableVirtioFS` in `runInit` after the
  Laravel `Detect` check, before the TUI. Uses the existing `prompt` helper
  (already `[Y/n]`-compatible: empty answer returns the default).
- `README.md` — Troubleshooting section entry: Windows dev mounts slow →
  run `pier init` (or enable VirtioFS manually: `[wsl2] virtio=true` +
  `virtiofs=true` in `%USERPROFILE%\.wslconfig`, WSL 2.7.1+, then
  `wsl --shutdown` and restart Docker Desktop).
- `CHANGELOG.md` — unreleased entry.

## 3. Tests (`internal/cli/virtiofs_test.go`, new)

Table-driven with injected seams; no real WSL calls. Cases:

- non-Windows / WSL-path project → no prompt, no file writes;
- `wsl --version` failure → silent skip;
- version 2.6.2 → update prompt offered; accepting runs update then re-checks;
- version ≥ 2.7.1 + `virtiofs=true` already present → "already enabled",
  no writes;
- no `.wslconfig` → consent prompt → file created with both keys;
- existing `.wslconfig` with `memory=4GB` → keys inserted under `[wsl2]`,
  `memory` preserved;
- existing `virtio=false` → untouched, file otherwise unchanged;
- declining the prompt → no writes, no update run;
- restart instructions printed after a successful enable.

## 4. Verification

- `go build ./...`, `go vet ./...`, `go test ./...` pass (CI on Linux
  exercises the skip paths via the seams).
- `gofmt` clean.
- Manual (Windows): run `pier init` in a scratch Laravel dir — check the
  prompt text, merge behavior against a pre-populated `.wslconfig`, and the
  no-prompt cases (`\\wsl$` path, virtiofs already on).
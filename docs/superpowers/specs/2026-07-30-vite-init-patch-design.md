# Pier — `pier init` Auto-Patches `vite.config.ts` for Vite Dev Server — Design

**Date:** 2026-07-30
**Status:** Draft (brainstorming), pending spec review

## Problem

When `pier init` scaffolds a Laravel project's dev stack, it writes
`pier.toml`, `docker-compose.yml`, `.env`, and the
`docker/<php>/Dockerfile` family. It deliberately does not touch the
user's source code (`vite.config.ts`, `package.json`, `routes/*`,
`resources/*`, etc.) — pier's job is the Docker layer.

The Vite dev server that ships with the Inertia starter kit (and most
modern Laravel starters) is configured by `vite.config.ts`. Out of the
box, that file has no `server` block:

```ts
// resources/views/../vite.config.ts (Inertia starter kit, unchanged)
export default defineConfig({
    plugins: [ ... ],
});
```

When the user runs `npm run dev` inside the `laravel.test` container,
Vite defaults to binding `127.0.0.1:5173` (loopback only inside the
container). Docker's port forward for `5173:5173` is configured
correctly — but the traffic arrives at the container's `eth0`, not
loopback, and Vite refuses it. The result:

- `http://localhost:5173` from the host browser: `ERR_CONNECTION_REFUSED`.
- `http://localhost:8000` returns the page, but unstyled — the
  `@vite` directive in `resources/views/app.blade.php` emits
  `<link rel="stylesheet" href="http://localhost:5173/resources/css/app.css">`,
  the browser fetches that URL, the fetch fails, the CSS never lands,
  the Inertia page renders as raw HTML with browser-default styling.

The fix the user has to make by hand, every time, on every project:

```ts
// vite.config.ts
export default defineConfig({
    server: { host: true },   // bind 0.0.0.0 inside the container
    plugins: [ ... ],
});
```

This is a bug in the *generated* dev environment, not in the user's
code. pier should fix it as part of the same `pier init` that creates
the rest of the dev environment.

## Goals

1. `pier init` patches the project's `vite.config.ts` to add
   `server: { host: true }` so the Vite dev server is reachable from
   the host through Docker's port forward.
2. The patch is idempotent: re-running `pier init` is a no-op for an
   already-patched file, and for a project with no Vite config.
3. The patch is merge-aware: if the user has a `server: { ... }` block
   already, we add `host: true` to it rather than clobbering it. If
   the user has explicitly set `server.host` to any value, we leave
   the file alone.
4. The patch is loud about what it did: a one-line stdout message
   names the file and the property. Silent modifications erode trust.

## Non-goals

- Editing `package.json` (e.g. `"dev": "vite --host"`). Decided
  during brainstorming: a config-file change survives more invocation
  paths (`npx vite`, custom npm scripts, IDE runners).
- Editing `vite.config.js`. Most Laravel starters ship `.ts`; if
  `.ts` is absent and `.js` is present, we patch `.js`. If neither
  exists, we don't create one.
- Vite config files written as exported functions, or with multiple
  `defineConfig` calls, or with `server` set via `mergeConfig`. The
  Inertia starter kit — the only shape we need to support for v1 — is
  the canonical `export default defineConfig({...})` form.
- A separate `pier vite patch` command. The patch is part of
  `pier init`. If a user on an existing project needs it, they can
  hand-edit the file (or re-init; pier won't re-init over an existing
  `pier.toml`).
- Touching the in-container Vite bind via `Dockerfile` or
  `supervisord.conf` env vars. The right knob lives in the user's
  Vite config; that file is what Vite reads.
- A `pier.toml` knob to disable the patch. The patch is the correct
  behavior; no opt-out for v1.

## Design

### Where the patch lives

New file: `internal/stack/laravel/vite.go`

Public function:

```go
// EnsureViteHost walks the project root looking for vite.config.ts
// (falling back to vite.config.js) and, if the file exists and does
// not already set server.host, adds server: { host: true } to the
// defineConfig() call. Returns (true, nil) when the file was
// modified, (false, nil) when no change was needed (file missing or
// already configured), and a non-nil error only for I/O or parse
// failures.
func EnsureViteHost(projectPath string) (changed bool, err error)
```

### Algorithm

1. Probe for candidates in this order:
   `<projectPath>/vite.config.ts`, `<projectPath>/vite.config.js`.
   If neither exists → return `(false, nil)`. No file is created.
2. Read the file as bytes.
3. Check whether the file already has `host:` inside a `server:` block.
   Match the regex
   `(?s)server\s*:\s*\{[^}]*?\bhost\b\s*:`.
   If matched → return `(false, nil)`. We treat any explicit
   `host:` setting (including `host: '0.0.0.0'`, `host: false`, etc.)
   as user intent and do not modify the file.
4. Otherwise, check whether the file has a `server:` block (with or
   without a `host` key) using the regex
   `(?s)server\s*:\s*\{`.
5. **Insertion case A — no `server` block, has `defineConfig({`:**
   Find the first `defineConfig(\s*{` and insert
   `\n    server: { host: true },` immediately after the opening
   brace. Write the file. Return `(true, nil)`.
6. **Insertion case B — has `server:` block but no `host:`:** find
   the `server\s*:\s*\{` and insert ` host: true,` immediately after
   the opening brace of the `server` block. Write the file. Return
   `(true, nil)`.
7. **Edge case — no `defineConfig({` at all:** this is a
   non-standard config shape (function form, etc.). We do not
   attempt to patch. Return `(false, nil)`. The user can hand-edit
   or open an issue; the comment in the file is one line.

The `(?s)` flag (DOTALL) lets `[^}]` span newlines, so the regex
covers multi-line `server: { ... }` blocks. The `\b` word boundary
on `host` avoids false positives on identifiers like `ghost:`
(unlikely in practice; defense in depth).

Idempotency follows from step 3: once `host:` is in the file, every
subsequent call to `EnsureViteHost` matches step 3 and returns
`(false, nil)` without touching the file.

### Caller change in `internal/cli/init.go`

In `runInit`, after the existing pier-owned files are written (the
`for _, file := range append(devFiles, prodFiles...)` loop at
`internal/cli/init.go:139-147`), before the `initialized pier
project at <abs>` line:

```go
changed, err := laravelpkg.EnsureViteHost(abs)
if err != nil {
    return fmt.Errorf("patch vite.config.ts: %w", err)
}
if changed {
    fmt.Fprintf(cmd.OutOrStdout(),
        "patched vite.config.ts: set server.host=true (required so Vite is reachable from the host through the Docker port forward)\n")
}
```

`changed=false` is silent. The "initialized pier project at <abs>"
line follows, so the output order is: pier files, optional patch
message, success line.

The call sits *after* the existing pier-owned write loop so that a
Vite patch failure doesn't leave the project half-initialized. If
`EnsureViteHost` errors, we return without printing the success
line, and the user sees a clean error pointing at the patch step.
(The pier-owned files have already been written at that point;
that's fine — the user can re-run `pier init` once the underlying
issue is fixed, but pier.toml exists so `pier init` will refuse. The
user's path is: hand-fix the Vite issue, then either accept the
existing pier.toml or delete it and re-init. The README will
mention this in the troubleshooting section.)

### File preservation

The function writes the file in place using `os.WriteFile` with mode
`0644`. The user's existing `vite.config.ts` is overwritten only
when step 5 or 6 of the algorithm fires. The diff is exactly two
lines (insertion in the common case); for the merge case, exactly
one line.

### Error semantics

- File not found: `(false, nil)`. Not an error.
- File read failure: `(false, err)`. Surface to the user.
- Regex match failure (no `defineConfig({`): `(false, nil)`. Not
  an error; out of scope for v1.
- Write failure: `(false, err)`. Surface to the user.

The function never returns `(true, err)`. Either we changed the
file and the write succeeded, or we didn't and any error is
informational.

### Tests

New file: `internal/stack/laravel/vite_test.go`.

Each test runs `EnsureViteHost` on a `t.TempDir()` containing a
hand-written `vite.config.ts`, then asserts on `(changed, file
contents)`. The tests do not depend on any other pier package.

| Test | Input | Expected `changed` | Expected file contains |
|---|---|---|---|
| `TestEnsureViteHost_NoConfigFile` | empty dir | `false` | (no file created) |
| `TestEnsureViteHost_FallsBackToJS` | only `vite.config.js` present, shape A | `true` | `server: { host: true },` in the `.js` file |
| `TestEnsureViteHost_PatchesInertiaShape` | Inertia starter-kit shape (no `server:`) | `true` | `server: { host: true },` as a sibling of `plugins:` |
| `TestEnsureViteHost_MergesIntoExistingServer` | `server: { port: 3000 }` only | `true` | `server: { host: true, port: 3000 }` (or `port: 3000, host: true,` — order doesn't matter; assert both keys present) |
| `TestEnsureViteHost_AlreadyHasHostTrue` | `server: { host: true }` | `false` | file byte-equal to input |
| `TestEnsureViteHost_AlreadyHasHostString` | `server: { host: '0.0.0.0' }` | `false` | file byte-equal to input |
| `TestEnsureViteHost_AlreadyHasHostFalse` | `server: { host: false }` | `false` | file byte-equal to input |
| `TestEnsureViteHost_Idempotent` | Inertia shape; run twice | `true` then `false` | file byte-equal between the second call and the first call's output |
| `TestEnsureViteHost_NoDefineConfig` | file with no `defineConfig({` (just a `plugins:` array exported as default) | `false` | file byte-equal to input |
| `TestEnsureViteHost_PreservesSurroundingContent` | Inertia shape with trailing comment | `true` | `server: { host: true },` inserted; trailing comment intact |

The `TestEnsureViteHost_PreservesSurroundingContent` test guards
against a regex match that eats too much. The assertion is exact:
read the input, run the function, diff the output against
`input-with-insertion` byte-for-byte.

### Caller test in `internal/cli/init_test.go`

Extend the existing init-test coverage to assert the stdout line:

- `TestInit_PatchesViteConfig`: build a `t.TempDir()` with a
  Laravel-ish layout (just `composer.json` referencing
  `laravel/framework` and a `vite.config.ts` in the Inertia
  starter-kit shape), call `runInit` with the test seams
  (`tuiForTest = false`), capture stdout, assert it contains
  `patched vite.config.ts: set server.host=true`.
- `TestInit_NoPatchWhenAlreadyConfigured`: same setup but
  `vite.config.ts` has `server: { host: true }` already; assert
  stdout does NOT contain the `patched` line.
- `TestInit_NoPatchWhenNoViteConfig`: same setup but no
  `vite.config.ts`; assert stdout does NOT contain the `patched`
  line and the function returns nil.

These three tests pin the behavior at the CLI layer. Combined with
the unit tests in `vite_test.go`, the function is covered at both
the boundary and the interior.

### Files changed

1. `internal/stack/laravel/vite.go` (new) — `EnsureViteHost`.
2. `internal/stack/laravel/vite_test.go` (new) — table above.
3. `internal/cli/init.go` — call `EnsureViteHost` after the file
   write loop, print the patch message on `changed=true`. ~10 lines
   added.
4. `internal/cli/init_test.go` — three new tests. ~80 lines added.
5. `README.md` — add a one-line troubleshooting bullet:
   *"Vite dev server unreachable / CSS not loading in browser — run
   `pier init` again or hand-edit `vite.config.ts` to add
   `server: { host: true }`."* and a brief note in the "Quickstart"
   about what `pier init` touches (now includes `vite.config.ts`).
6. `docs/superpowers/specs/2026-07-30-vite-init-patch-design.md` —
   this spec.

### What does NOT change

- `pier.toml` shape: no new fields, no new keys, no migration. The
  user does not need to edit `pier.toml` to opt in or out of the
  patch.
- `docker-compose.yml` generation: no change. The Vite port forward
  (`5173:5173`) is already in place via the dev-ports design
  (`docs/superpowers/specs/2026-07-29-dev-ports-design.md`).
- The `pier init` flow order: detect → write pier.toml → write
  docker files → write .env → (NEW) patch vite.config.ts → success
  line. The new step is the last thing before the success line.
- `package.json`: untouched. The `dev` script stays `"vite"`. Vite
  reads `server.host` from the config file regardless of the CLI
  flag.
- The smart-merge logic for an existing `docker-compose.yml` (in
  `internal/stack/laravel/merge.go`): unaffected. The Vite patch is
  independent of the compose merge.
- The dev `bind` opt-in (`docs/superpowers/specs/2026-07-30-dev-bind-opt-in-design.md`):
  orthogonal. `bind` controls the *host*-side bind address;
  `server.host` controls the *container*-side Vite bind. Both are
  needed for the URL to be reachable end-to-end.
- The container-side supervisord default
  (`artisan serve --host=0.0.0.0`): unaffected. Laravel has been
  binding `0.0.0.0` correctly; only Vite needed the fix.

## Rollback

Reverting the commit restores the previous behavior: `pier init` no
longer touches `vite.config.ts`. No data migration. No state in
`pier.toml`. Users who ran the new `pier init` keep their patched
`vite.config.ts`; users who revert their pier binary still have
the patch, which is correct — there is no harm in
`server: { host: true }` being set, and the next user who runs
`pier init` again is a no-op (idempotent).

## Out of scope (deferred)

- **A `pier vite patch` subcommand for existing projects.** pier
  init refuses to run when `pier.toml` exists, so the patch is
  unavailable to users who initialized before this change. A
  follow-up could add `pier vite patch` for that case. YAGNI for
  v1; the user can hand-edit.
- **`vite.config.js` support beyond "fall back if `.ts` is
  absent."** Real-world JS configs (non-TS) are rare in modern
  Laravel. The fallback is one line; anything more (a separate
  `PierConfigViteJs` codepath, etc.) is over-engineering.
- **Patching `package.json` as a defense-in-depth measure.** The
  brainstorming decision was to patch the config file only. If a
  user reports that the patch doesn't work in their setup (e.g.
  they run `vite` through a custom npm script that strips args),
  we revisit.
- **A `pier.toml` knob to disable the patch.** The patch is
  correct; the failure mode it fixes is real; the cost (one
  line of `vite.config.ts` modified on first init) is small.
  Opt-out adds API surface for no user benefit.
- **Notification of the patch via the README's "Quickstart"
  section.** The current Quickstart says "pier init" without
  enumerating touched files; we'll add a one-line note but no
  callout block.
- **Validation that the project actually uses Vite.** The
  detection step (`internal/stack/laravel/detect.go`) looks for
  `composer.json` with `laravel/framework`; we don't check for
  `vite` in `package.json` deps. That's fine: a Laravel project
  without Vite is rare, and if `vite.config.ts` doesn't exist
  the patch is a no-op. The unit tests cover the no-file case.

## Open questions

None. Resolved during brainstorming:

- **Scope of touch:** `vite.config.ts` only. `package.json` is
  left alone.
- **Where to call:** at the end of `runInit`, after the pier-owned
  file write loop.
- **Idempotency rule:** any existing `server.host` (any value)
  → no-op. This includes `host: false` and `host: 'something'` —
  we trust the user's explicit choice.
- **JS fallback:** yes, `vite.config.js` is patched when `.ts` is
  absent. No creation of either file.
- **Failure mode:** surface the error, do not half-init. The
  pier-owned files are already written; that's the same as any
  partial-disk-failure during init today, and the README's
  troubleshooting section will document the recovery.

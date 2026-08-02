# Pier — Live Output for `pier bootstrap` — Design

**Date:** 2026-08-02
**Status:** Draft (brainstorming), pending spec review

## Problem

`pier bootstrap` swallows every byte of remote output. The
provisioning step pipes the sudo password through `RunStdin`
(`internal/deploy/ssh.go:107`), which captures stdout and stderr into
in-memory buffers; nothing is written to the terminal until the whole
command finishes — and on success, captured output is discarded
entirely. The Docker install (`curl -fsSL https://get.docker.com | sh`)
quietly takes 2–5+ minutes, so the user stares at a blank screen after
entering the sudo password and cannot tell whether pier is working,
blocked on the network, or hung on a sudo prompt.

Coolify's installer streams its progress (`+ sh -c 'apt-get ...'`
lines) because it runs the script in the user's own terminal. pier
runs the same script over SSH but currently hides it. The user asked:
"can we do the same?" — yes.

## Goals

1. Stream the remote command's stdout **and** stderr live during
   `pier bootstrap` provisioning (install + verify steps), Coolify
   style.
2. Keep piping the sudo password over the session's stdin — never on
   the command line.
3. Always stream; no new flag. Bootstrap runs once per server, and
   live progress is exactly what the user wants to see.
4. Preserve the existing failure contract: wrong password →
   re-prompt once; not in sudoers → abort with instructions. Error
   classification (`classifySudoErr`) must keep working on streamed
   runs.
5. Follow the existing line-streaming pattern (`RunStream` +
   `onLine` callbacks, used by the deploy build stage,
   `internal/deploy/build.go:11`).

## Non-goals

- Streaming for `pier deploy` / `rollback` — the build stage already
  streams; the rest stays captured.
- PTY allocation for the SSH session. Line streaming matches the
  existing pattern and keeps stderr/stdout separable for error
  classification; a PTY would merge channels and mangle line
  structure.
- A `--quiet` flag. YAGNI — nobody has asked for silent bootstrap.
- Changing the probe step (`command -v docker && docker info`): its
  output is intentionally discarded, it is a boolean check.
- Re-prompting behavior changes — the one re-prompt on a wrong
  password stays as-is.

## Design

### 1. SSH layer — new `RunStreamStdin` (`internal/deploy/ssh.go`)

New method on `*Client`, combining `RunStdin` and `RunStream`:

```go
func (c *Client) RunStreamStdin(
    ctx context.Context, cmd, stdin string,
    onStdout, onStderr func(string),
) error
```

- `sess.Stdin = strings.NewReader(stdin)` — same password piping as
  `RunStdin`.
- `sess.StdoutPipe()` + `sess.StderrPipe()`; two goroutines scan
  lines and invoke the callbacks as each line arrives.
- While streaming, stderr lines are **also** appended to a shared
  `bytes.Buffer` used for error classification. Only the stderr
  goroutine writes to it, so no locking is needed.
- `sess.Start(cmd)` then `sess.Wait()`; the exit error is returned as
  today.

`stdinRunner` (`internal/deploy/ssh.go:163`) gains the new method —
bootstrap is its only implementer, so this is a one-interface change.
The fake runners in `bootstrap_test.go` gain a matching stub.

### 2. Bootstrap layer — thread callbacks (`internal/deploy/bootstrap.go`)

- `runSudo(ctx, r, password, cmd, onStdout, onStderr)` — signature
  gains the two callbacks; executes `sudo -S -p '' sh -c '<cmd>'` via
  `RunStreamStdin`. On failure it classifies using the captured stderr
  exactly as today (`classifySudoErr`).
- `-p ''` suppresses sudo's own `[sudo] password for X:` prompt: the
  password was already piped in, so showing it would look like a
  second prompt. The prompt text would otherwise appear in the
  streamed stderr.
- `ValidateSudo`, `Provision`, `VerifyBootstrap` gain the same two
  callbacks and forward them. `sudo -S -v` and `docker info` are
  quiet in practice; the get.docker.com script is the chatty one —
  the whole point.
- `BootstrapEnv` gains `OnStdout, OnStderr func(string)` fields on
  `BootstrapOpts` (the existing param struct) and forwards them.
  `ProbeEnv` is untouched — probe output stays discarded.
- Nil callbacks are safe (no-op) so existing call sites/tests that
  don't care about output keep compiling.

### 3. CLI layer — wire the terminal (`internal/cli/bootstrap.go`)

`runBootstrap` passes the cobra writers:

- `onStdout = func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) }`
- `onStderr = func(line string) { fmt.Fprintln(cmd.OutOrStderr(), line) }`

On failure nothing new is needed: the streamed lines are already on
screen, then the classified error is printed via `PrintError` as
today. The wrong-password re-prompt path is unchanged.

### What the user sees

```
$ pier bootstrap production
sudo password for root@203.0.113.5:
# Executing docker install script
+ sh -c 'apt-get update -qq ...'
+ sh -c 'apt-get install -y docker-ce ...'
...
docker info && docker compose version  # verify output streams too
production: done
```

A wrong password now visibly shows sudo's `sorry, try again` live
before the CLI's re-prompt appears.

### Code layout & boundary rules

- `internal/deploy/ssh.go` — `RunStreamStdin`; `stdinRunner`
  extension. No behavior change to `Run`/`RunStdin`/`RunStream`.
- `internal/deploy/bootstrap.go` — callback threading, `-p ''`.
- `internal/cli/bootstrap.go` — writer wiring only.
- Boundary rules hold: `cli` never runs SSH directly; `deploy` never
  reads the TUI.

### Files changed

- `internal/deploy/ssh.go` — new `RunStreamStdin`, `stdinRunner` +
  method.
- `internal/deploy/bootstrap.go` — callbacks through
  `runSudo`/`ValidateSudo`/`Provision`/`VerifyBootstrap`/`BootstrapEnv`,
  `-p ''` in the sudo command.
- `internal/cli/bootstrap.go` — wire stdout/stderr writers.
- `internal/deploy/bootstrap_test.go` — fake runner gains
  `RunStreamStdin`; new tests (below).
- `internal/deploy/ssh_test.go` — unit tests for `RunStreamStdin`.
- `README.md` — one line in the bootstrap section: installation
  output is streamed live.
- `docs/superpowers/specs/2026-08-02-bootstrap-live-output-design.md`
  — this spec.

### Test plan

| Test | Asserts |
|---|---|
| `TestRunStreamStdinStreamsBothChannels` | Lines from both pipes arrive at their callbacks in order |
| `TestRunStreamStdinPipesStdin` | The stdin string reaches the remote command |
| `TestRunStreamStdinCapturesStderrForClassification` | On exit failure, captured stderr still classifies as wrong password / not sudoers |
| `TestRunStreamStdinExitError` | Non-zero remote exit returns an error (as today) |
| `TestRunSudoStreamsAndPipesPassword` | Fake runner receives the piped password and callback invocations |
| `TestRunSudoSuppressesPrompt` | Command contains `-p ''`; stderr capture unchanged |
| `TestProvisionForwardsCallbacks` | get.docker.com run's lines reach the callback |
| `TestBootstrapEnvStreamsOutput` | CLI wiring: streamed lines appear on stdout/stderr writers |
| Existing bootstrap/sudo tests | Keep passing with fake runners updated for the new interface |

Manual verification checklist (README):
- `pier bootstrap <env>` on a fresh VPS shows the get.docker.com
  progress live; a wrong sudo password shows sudo's error before the
  re-prompt; verify output streams; `production: done` prints at the
  end.

## Rollback

Reverting this change restores the silent buffered behavior — no
state is affected. The remote provisioning steps are unchanged; only
the transport of their output changes.

## Out of scope (deferred)

- **Streaming deploy/rollback stages.** Different question; the build
  stage already streams.
- **PTY / progress-bar rendering.** Line streaming is the ask;
  fancy rendering is follow-up if requested.
- **`--quiet` flag.** YAGNI.

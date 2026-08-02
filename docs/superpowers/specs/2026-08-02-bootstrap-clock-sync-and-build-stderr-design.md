# Pier — Bootstrap Clock Sync & Build Stderr Surfacing — Design

**Date:** 2026-08-02
**Status:** Draft (brainstorming), pending spec review

## Problem

Two real-world failures surfaced when bootstrapping and deploying to a
freshly-reset QEMU/KVM guest (`192.168.122.63`, Ubuntu 24.04):

**1. Bootstrap fails on a stale guest clock.** After a VM reset, the
guest boots with a stale RTC well before systemd-timesyncd catches up.
`pier bootstrap` then pipes `get.docker.com | sh` straight into the
box (`internal/deploy/bootstrap.go:99`), and the script's first
`apt-get update` fails because signed Release files are dated in the
guest's future:

```
E: Release file for http://archive.ubuntu.com/ubuntu/dists/noble-updates/InRelease
   is not valid yet (invalid for another 5h 54min 54s)
```

The observed skew was ~24h; apt rejects anything even minutes off.
Pier has no clock handling, so a brand-new server can fail its first
provisioning run minutes after boot for a reason the user cannot see
or fix without SSHing in and running `sudo date -s` themselves.

**2. Deploy build failures are opaque.** `pier deploy` runs
`docker compose build` through `Client.RunStream`
(`internal/deploy/ssh.go:182`), which streams **stdout only**. Docker
compose writes its validation and build errors to **stderr**, which is
discarded; on failure the pipeline surfaces only the exit status:

```
10:35:14 build failed: Process exited with status 1
error[docker]: Process exited with status 1
```

The real cause (`service "s3" refers to undefined volume s3_data:
invalid compose project`) never reaches the terminal. Every build
failure is a black box.

## Goals

1. `pier bootstrap` detects and corrects a stale remote clock before
   provisioning — without depending on NTP reachability.
2. `pier deploy` build failures include the remote command's stderr so
   the actual docker error is visible without extra flags.
3. Zero new dependencies; no new SSH surface; existing interfaces stay
   stable (`runner`, `stdinRunner`).

## Non-goals

- Waiting for NTP. The chosen approach force-sets the clock from
  pier's own (assumed-correct) host clock; NTP is allowed to re-sync
  afterwards.
- Fixing compose-file defects like undeclared volumes. The stderr
  surfacing makes them *visible*; pier does not validate user compose
  files.
- Changing CLI flags, exit codes, `Kind`s, or hints. Both fixes ride
  the existing error contract.
- Clock sync in the deploy pipeline — only bootstrap provisions, and
  deploy requires docker, which implies a completed bootstrap.

## Approach

**A (chosen): set the remote clock from pier when skew exceeds a
threshold.** Read the remote epoch with a plain `Run` (no sudo),
compare to `time.Now().Unix()`, and when |skew| > 60s run the existing
`runSudo` helper with `date -s @<epoch>`. Deterministic, works
offline, one extra command in the happy path (the read), zero extra
when in sync.

Rejected alternatives:

- **B — wait for NTP** (`timedatectl show -p NTPSynchronized` loop).
  Non-invasive but fails on NTP-unreachable hosts (isolated
  networks); adds a timeout with no clear fallback.
- **C — retry provisioning on apt's "not valid yet" error.** Post-hoc
  and fragile; the failing command already ran, and apt may fail on
  later steps too.

**For the build fix:** stream stderr alongside stdout in `RunStream`
(mirroring the two-loop pattern `RunStreamStdin` already uses,
`internal/deploy/ssh.go:146-175`), and keep a bounded ring buffer of
the last ~20 streamed lines so the returned error carries the output
tail. Signature unchanged — `Build` (`internal/deploy/build.go:11`)
benefits automatically.

## Design

### 1. Clock sync (`internal/deploy/bootstrap.go`)

New exported const and function:

```go
// ClockSyncThreshold is the max |remote - local| offset in seconds
// tolerated before pier force-sets the remote clock. Freshly-reset
// VMs boot with a stale RTC; apt rejects signed Release files whose
// dates fall outside the (wrong) guest clock, so even minutes of
// skew break provisioning.
const ClockSyncThreshold = 60

// EnsureClockSynced compares the remote clock to the local clock and,
// when they differ by more than ClockSyncThreshold seconds, force-sets
// the remote clock from the local one under sudo (`date -s @<epoch>`).
// Needs sudo only when a correction is required.
func EnsureClockSynced(ctx context.Context, r stdinRunner, password string, onStdout, onStderr func(string)) error
```

Behavior:

1. `r.Run(ctx, "date +%s")` — no sudo; parse the epoch.
2. `local := time.Now().Unix()`; if `abs(local-remote) <=
   ClockSyncThreshold`, no-op.
3. Otherwise `runSudo(ctx, r, password, "date -s @<local>", ...)`.
   `date -s @<epoch>` works even when systemd-timesyncd is active;
   NTP re-adjusts afterwards, and the set value is pier's own clock
   anyway.
4. On correction, re-read the remote epoch and emit one line via
   `onStdout`: `remote clock was Ns off; corrected to <RFC3339>`.
5. Fail fast with wrapped, actionable errors: the read wraps
   `read remote clock: %w`; a failed `date -s` wraps
   `sync remote clock: %w` (classifying through the existing
   `classifySudoErr`).

Called from `BootstrapEnv` between `ValidateSudo` and `Provision`
(password and sudo are proven by then; docker install happens on a
sanely-timed host). Flow per env after this change:

1. Probe (skip if bootstrapped, unless `--force`).
2. Validate sudo password (`sudo -S -v`).
3. **Sync remote clock if skewed (`date +%s` read; `date -s` if needed).**
4. Install Docker Engine + compose plugin.
5. Grant deploy user docker group membership.
6. Create deploy path (when set).
7. Verify.

### 2. Build stderr surfacing (`internal/deploy/ssh.go`)

`Client.RunStream` changes internally; signature stays:

```go
func (c *Client) RunStream(ctx context.Context, cmd string, onLine func(string)) error
```

- Both `StdoutPipe` and `StderrPipe` are drained; stderr lines feed
  the same `onLine` callback (goroutine, same pattern as
  `RunStreamStdin`), so docker compose validation/build errors appear
  in the stream.
- A ring buffer keeps the last 20 streamed lines.
- On a non-zero remote exit, the returned error wraps
  `sess.Wait()`'s error with the tail:

```
remote command failed: Process exited with status 1 (last output:
service "s3" refers to undefined volume s3_data: invalid compose project)
```

The deploy pipeline (`deploy.go:86-91`) wraps this in `BuildError`
unchanged; the CLI's `build failed:` phase line now carries the real
cause. The `runner` interface and all fakes
(`fakeSSHClient`, `fakeRollbackRunner`) keep compiling unchanged.

### 3. Error classification

No changes. Clock read/set failures wrap as general errors from
`BootstrapEnv` (bootstrap exit path); build failures stay
`KindDocker` / `ExitBuild`.

## Testing

### Unit (fake runner, no SSH)

| Test | Asserts |
|---|---|
| `TestEnsureClockSyncedInSync` | `date +%s` returns local epoch → no `date -s` command issued |
| `TestEnsureClockSyncedCorrectsSkew` | `date +%s` returns local−24h → `sudo -S -p '' sh -c 'date -s @<epoch>'` issued, stdin is the password, read precedes set |
| `TestEnsureClockSyncedReadFailure` | `date +%s` fails → error wraps `read remote clock` |
| `TestEnsureClockSyncedSetFailure` | `date -s` fails with `Sorry, try again.` → `ErrSudoWrongPassword` |
| `TestBootstrapEnvSyncsClockBeforeProvision` | Full flow: command order is date-read → date-set → `get.docker.com`; corrected line reaches `OnStdout` |

Existing `BootstrapEnv` tests (`TestBootstrapEnvProvisionsWhenNeeded`,
`TestBootstrapEnvForceReprovisions`, `TestBootstrapEnvStreamsOutput`,
`TestBootstrapEnvCreatesDeployPath`,
`TestBootstrapEnvSkipsPathWhenEmpty`) gain a scripted
`{match: "date +%s", ok: true, stdout: <local epoch>}` step, since
`scriptedRunner` answers unknown commands with exit failure
(`bootstrap_test.go:76`).

### Integration (`-tags=integration`, Linux)

`TestRunStreamRealServer` in `bootstrap_integration_test.go` (gated
like `TestRunStreamStdinRealServer`): a command that writes to both
stdout and stderr and exits non-zero (e.g.
`sh -c 'echo out; echo err >&2; exit 7'`) must (a) deliver both lines
to `onLine` and (b) return an error containing the tail.

## Files changed

- `internal/deploy/bootstrap.go` — `ClockSyncThreshold`,
  `EnsureClockSynced`, wired into `BootstrapEnv`.
- `internal/deploy/ssh.go` — `RunStream` streams stderr + error tail.
- `internal/deploy/bootstrap_test.go` — new clock-sync tests; script
  the `date +%s` step in existing `BootstrapEnv` tests.
- `internal/deploy/bootstrap_integration_test.go` —
  `TestRunStreamRealServer` (integration-tagged).
- `CHANGELOG.md` — entry for both fixes.

Boundary rules hold: `cli` never runs SSH directly; `deploy` never
reads the TUI; no new dependencies; the sudo password is never
stored; `Run`/`RunStdin`/`RunStreamStdin` behavior unchanged.

## Verification checklist (manual)

1. Unit + integration suite: `go vet ./... && go test ./... &&
   go test -tags=integration ./internal/deploy/`.
2. Against a VM with a deliberately wrong clock: `pier bootstrap
   production` prints the skew-correction line, then completes.
3. `pier deploy production` against a compose file with an
   undeclared volume: the `build failed` line shows the compose
   validation error.
4. Rebuild the distributed binary (`go build -o pier ./cmd/pier`) and
   re-run the real deploy on `192.168.122.63` end to end.

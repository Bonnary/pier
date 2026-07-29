# Pier — `[dev] bind` Opt-In for Host-Side Port Binding — Design

**Date:** 2026-07-30
**Status:** Draft (brainstorming), pending spec review

## Problem

The 2026-07-29 dev-ports design chose `127.0.0.1` as the host-side bind
address for all pier-owned dev ports, in service of LAN safety (spec
`2026-07-29-dev-ports-design.md:63-64`). The implementation is merged:
`internal/stack/laravel/ports.go:50-55` returns `"127.0.0.1"` for dev,
and the generated `docker-compose.yml` emits entries like
`127.0.0.1:8000:8000`.

This works for every host where `localhost` resolves to `127.0.0.1`
first. It breaks on hosts where `localhost` resolves to `::1` (IPv6
loopback) first — a default on many modern Linux distros (Arch, recent
Ubuntu, Fedora) and on macOS when IPv6 is enabled. In that case the
browser tries `[::1]:8000`, Docker's port proxy isn't listening on
`::1` (it only bound `127.0.0.1`), and the user gets `ERR_CONNECTION_REFUSED`
even though the container is up and the port is mapped.

In-container, pier already does the right thing: the supervisord default
is `php artisan serve --host=0.0.0.0` and the Laravel 11+ `php artisan
dev` command also binds `0.0.0.0:8000`. The failure is symmetric to a
bug already documented in `internal/stack/laravel/services_test.go:96-108`
("`localhost` resolves to `::1` in alpine images and meilisearch only
binds 0.0.0.0:7700 (IPv4), so the check fails with 'Connection refused'")
— the project already knows about the `localhost` → `::1` failure mode
and has fixed it for the in-container case. The host-side case is the
same problem with the bind direction reversed.

Flipping the default to `0.0.0.0` would fix the failure everywhere, but
it would also re-introduce LAN exposure for every user on a shared
network — the explicit, reviewed design goal of the previous spec. The
right shape is an opt-in: keep the safe default, give the user a
one-line knob when they know they're on a trusted network (or when
they just want the binding to be unambiguous regardless of `localhost`
resolution).

## Goals

1. Add an opt-in `[dev] bind` field in `pier.toml` that lets the user
   override the host-side bind address for all pier-owned dev ports.
2. Default to `"127.0.0.1"` (the current behavior) when the field is
   absent — no breaking change for existing `pier.toml` files.
3. Accept exactly two values: `"127.0.0.1"` and `"0.0.0.0"`. Anything
   else is a config error.
4. When the field is set to `"0.0.0.0"`, print a warning to stderr at
   the start of every `pier dev` run so the user knows the dev ports
   are reachable from their LAN.
5. Update the URLs printed in `pier dev`'s ready block to reflect the
   configured bind (no more silently printing `127.0.0.1` when the
   actual port is on `0.0.0.0`).
6. Add a README troubleshooting bullet that names the exact failure
   mode and points the user at the new knob.

## Non-goals

- Changing the default. `127.0.0.1` stays the default. Users who want
  `0.0.0.0` opt in explicitly.
- A deploy-side equivalent. `[deploy.<env>]` keeps the existing
  "no prefix" / host-firewall-responsible behavior. Production exposure
  is a deployment concern, not a pier config concern.
- A `--bind` CLI flag. YAGNI — the `pier.toml` field is enough; if a
  user wants a one-shot override they can edit the file, run, revert.
- Per-port bind overrides. A single top-level `[dev] bind` covers the
  failure mode. Mixing `127.0.0.1` and `0.0.0.0` across ports is a
  footgun (which ports are exposed? which aren't?) with no real use
  case behind it.
- Accepting raw IP literals beyond the allowlist (`::`, `192.168.x.x`,
  etc.). The IPv6 `::` case is real but adds a second failure surface
  (some browsers will resolve `localhost` to `::1`, some to `::`, some
  to both); punt until a user reports it.
- Changing the in-container bind (already `0.0.0.0`).
- Changing `portcheck.Probe`. It still dials `127.0.0.1`; a port bound
  to `0.0.0.0` is reachable on `127.0.0.1` too, so collision detection
  keeps working.

## Design

### TOML schema

New optional field under `[dev]`:

```toml
[dev]
# bind = "0.0.0.0"   # uncomment to expose dev ports to your LAN (default: 127.0.0.1)
ports = { laravel = 8000, vite = 5173 }
services = { ... }
```

The field is **dev-only**. `[deploy.<env>]` is unchanged.

### Config struct

`internal/config/config.go`:

```go
type DevConfig struct {
    Bind     string               `toml:"bind"`     // NEW
    Services map[string]DevService `toml:"services"`
    Ports    map[string]int        `toml:"ports"`
}

const DefaultDevBind = "127.0.0.1"
```

### Parser

`internal/config/parse.go` — apply the default and validate **before**
any other `[dev.*]` validation runs, so subsequent error messages can
reference the resolved value if needed:

```go
if c.Dev.Bind == "" {
    c.Dev.Bind = DefaultDevBind
}
switch c.Dev.Bind {
case "127.0.0.1", "0.0.0.0":
    // ok
default:
    return fmt.Errorf("%w: [dev] bind = %q must be %q or %q",
        ErrConfigInvalid, c.Dev.Bind, "127.0.0.1", "0.0.0.0")
}
```

`ErrConfigInvalid` is the existing sentinel; the caller-side error path
doesn't change.

### `BindAddr` becomes config-driven

`internal/stack/laravel/ports.go:50-55` currently takes an `env` string.
The env parameter goes away — the caller has the config in hand:

```go
// BindAddr returns the bind-address prefix for compose `ports:` strings,
// taken from the user's pier.toml. Empty string is treated as the default
// for safety, but the parser ensures c.Dev.Bind is never empty by the
// time anyone calls this.
func BindAddr(bind string) string {
    if bind == "" {
        return config.DefaultDevBind
    }
    return bind
}
```

`DefaultDevBind` lives in the `config` package as the single source of
truth; `stack/laravel` imports it.

Call sites update from `BindAddr("dev")` → `BindAddr(cfg.Dev.Bind)`.
There is no more `if env == "dev"` branching anywhere in the code that
emits port bindings. The deploy-side call sites (prod, staging) pass
`""` inline — no helper, no new public symbol.

### `printReadyBlock`

`internal/cli/dev.go:111-129` currently hardcodes `127.0.0.1` in every
URL. It becomes config-driven:

```go
func printReadyBlock(cfg *config.Config) {
    bind := cfg.Dev.Bind
    for _, svc := range readyServices(cfg) {
        fmt.Printf("  %-7s http://%s:%d/\n", svc.Name, bind, svc.HostPort)
    }
}
```

### LAN-exposure warning

New function in `internal/cli/dev.go`, called once at the top of
`runDev` right after `config.Load` and before `MergeDev`:

```go
func maybeWarnLanExposure(cfg *config.Config) {
    if cfg.Dev.Bind == "127.0.0.1" {
        return
    }
    fmt.Fprintf(os.Stderr,
        "warning: [dev] bind = %q — dev ports are reachable from your LAN\n"+
        "         set [dev] bind = \"127.0.0.1\" in pier.toml to restore loopback-only\n",
        cfg.Dev.Bind)
}
```

Prints on every `pier dev` run when the bind is non-loopback. Not
persisted, not silenced, not blocking. The warning goes to stderr so it
doesn't interfere with the ready-block URLs on stdout.

### `pier init` default `pier.toml`

The default `pier.toml` written by `pier init` gets the new field as
a commented-out hint so users discover it without reading the docs:

```toml
[dev]
# bind = "0.0.0.0"   # uncomment to expose dev ports to your LAN (default: 127.0.0.1)
ports = { laravel = 8000, vite = 5173 }
```

### Files changed

- `internal/config/config.go` — add `Bind` field + `DefaultDevBind` const.
- `internal/config/parse.go` — default + allowlist validation.
- `internal/config/parse_test.go` — parser tests (default, both valid
  values, rejection of unknowns).
- `internal/stack/laravel/ports.go` — `BindAddr` signature change; drop
  the `env` parameter; import `config.DefaultDevBind`.
- `internal/stack/laravel/ports_test.go` — update `TestBindAddrDev` to
  assert the config-driven behavior; add a `0.0.0.0` case to
  `TestPortBinding`.
- `internal/stack/laravel/dev.go` — pass `cfg.Dev.Bind` to `BindAddr` in
  `laravelTestPorts` and `sidecarPorts`.
- `internal/stack/laravel/dev_test.go` — add
  `TestDevComposeBindsAllInterfacesWhenOptedIn` (constructs YAML
  in-memory; no new golden file).
- `internal/cli/dev.go` — `printReadyBlock` reads `cfg.Dev.Bind`;
  add `maybeWarnLanExposure` and call it from `runDev`.
- `internal/cli/init.go` (or wherever the default `pier.toml` is
  emitted) — write the commented-out `bind` hint.
- `README.md` — configuration example, troubleshooting bullet, manual
  verification checklist entry.
- `docs/superpowers/specs/2026-07-30-dev-bind-opt-in-design.md` — this
  spec.

### Test plan

| Test | Asserts |
|---|---|
| `TestDevBindDefaults` | pier.toml with no `[dev] bind` → `cfg.Dev.Bind == "127.0.0.1"` |
| `TestDevBindLoopbackAccepted` | `bind = "127.0.0.1"` → no error |
| `TestDevBindAllInterfacesAccepted` | `bind = "0.0.0.0"` → no error |
| `TestDevBindRejectsUnknown` | `bind = "::"`, `"localhost"`, `"192.168.1.1"`, `"0"` → `ErrConfigInvalid` with field name in message. Note: `bind = ""` is **not** rejected — the parser treats it the same as an absent field and applies the default. |
| `TestBindAddrReturnsConfig` (renamed from `TestBindAddrDev`) | `BindAddr("127.0.0.1")` → `"127.0.0.1"`; `BindAddr("0.0.0.0")` → `"0.0.0.0"`; `BindAddr("")` → `"127.0.0.1"` (safety fallback) |
| `TestPortBinding` (existing) | Add case: `("0.0.0.0", 8000, 8000) → "0.0.0.0:8000:8000"` |
| `TestDevComposeBindsLoopbackByDefault` (existing) | Existing golden files (`compose-no-services.yml`, `compose-with-services.yml`) still pass unchanged |
| `TestDevComposeBindsAllInterfacesWhenOptedIn` (new) | With `[dev] bind = "0.0.0.0"`, rendered YAML has `0.0.0.0:8000:8000`, `0.0.0.0:5173:5173`, and `0.0.0.0:<port>:<port>` for every sidecar |
| `TestPrintReadyBlockUsesConfiguredBind` (new, table-driven) | With bind `"127.0.0.1"` → output contains `http://127.0.0.1:N/`, not `0.0.0.0`. With bind `"0.0.0.0"` → reverse. |
| `TestMaybeWarnLanExposure` (new) | Bind `"127.0.0.1"` → no stderr output. Bind `"0.0.0.0"` → stderr contains `"LAN"` and the bind value. |

Manual verification checklist (added to README):
- `pier dev` with `[dev] bind = "0.0.0.0"` in `pier.toml` → warning
  printed, ready block shows `0.0.0.0`, port reachable from another
  device on the LAN. Remove the line, re-run → warning gone, ready
  block shows `127.0.0.1`, port not reachable from another device.

## Rollback

Deleting (or commenting out) the `[dev] bind` line in `pier.toml`
restores the previous behavior. No data migration, no persisted state
in pier, no rollback of the parser needed. The pier binary itself
remains backward-compatible with pre-this-change `pier.toml` files
(field is optional; absent field defaults to `127.0.0.1`).

## Out of scope (deferred)

- **Per-port bind override.** Not needed; the failure mode is uniform
  across pier-owned ports.
- **`--bind` CLI flag.** YAGNI. `pier.toml` is the source of truth.
- **Deploy-side bind.** Production exposure is a deployment concern;
  host firewall is the right layer.
- **IPv6 bind (`::1`, `::`).** The IPv6 failure mode is real but
  smaller in scope; punt to a follow-up if/when reported.
- **Cleaning up the stale "pending spec review" status on
  `2026-07-29-dev-ports-design.md`.** That spec was approved and its
  implementation is already merged. The status comment is stale. Note
  here for visibility; not blocked on this design.

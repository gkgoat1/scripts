# Plan: cleanartifacts — Pi-aware preservation, targeted mode, daemon, and resilient FS handling

> **Status: implemented.** This document is the design record for the changes
> landed in `cleanartifacts/main.go`, `internal/fsutil/`, and
> `install-cleanartifacts.sh`. See the test files for executable verification.

## Goal

Make `cleanartifacts` safe to leave running in the background by (a) never
deleting `node_modules` trees that Pi is actively using, (b) adding a focused
mode that only touches `target` and `node_modules` with the *expected*
on-disk structure, (c) shipping a daemon that runs that focused mode on an
interval, and (d) making filesystem permission errors non-fatal across the
tool — and, as a reusable pattern, in at least one shared helper other tools
can adopt.

The driving problem: `cleanAll` today walks a tree and `os.RemoveAll`s every
`target` and `node_modules` it finds, then returns the *first* error and exits
non-zero. On a shared/networked volume (`/Volumes/My Shared Files`), a single
`operation not permitted` (EPERM) on one directory aborts the whole run and
leaves the rest uncleaned — and worse, it can wipe a `node_modules` that Pi
needs to load extensions/skills.

## Background: how Pi uses node_modules

Pi loads packages from `~/.pi/agent/settings.json` `packages`. Each source
type materialises `node_modules` in a known place (see `docs/packages.md`):

| Source  | Global location                                                | Project location            |
|---------|----------------------------------------------------------------|-----------------------------|
| npm     | `~/.pi/agent/npm/node_modules` (sibling of `package.json`)     | `.pi/npm/node_modules`      |
| git     | `~/.pi/agent/git/<host>/<path>/node_modules`                   | `.pi/git/<host>/<path>/...` |
| local   | `<resolved-path>/node_modules`                                | (same)                      |
| pi CLI  | `<pi install>/node_modules` (e.g. `/opt/homebrew/lib/.../...`) | n/a                         |

The common, reliable signal that a `node_modules` belongs to Pi is **not**
its path (local-path packages can live anywhere) but its structure: a
`node_modules` directory whose **parent** contains a `package.json` is a
dependency tree that some toolchain owns. Pi always installs packages into a
directory that has a `package.json` (it runs `npm install` when one exists),
so "parent has `package.json`" is the structural fingerprint we preserve by
default — with an additional explicit allowlist of well-known Pi roots.

## Non-goals

- Do **not** change the behaviour of `-repo-roots` other than adding the same
  Pi-preservation and EPERM-resilience. It remains a workspace-scoped clean.
- Do **not** delete `~/.pi/agent` or the pi CLI install under any mode.
- Do **not** introduce a generic "ignore paths" glob language; preservation is
  structural + a fixed Pi-roots set, not user-pattern-driven. (A future plan
  can add `-ignore` if needed.)
- Do **not** make the daemon require `launchd`/root. It is a userspace
  `time.Ticker` loop with a `SIGINT/SIGTERM` shutdown, mirroring `pulse` and
  the `agentweave` daemon — optionally wired to a LaunchAgent by an installer
  script that reuses `installer/launchagent.sh`.

## Design

### 1. Pi-aware preservation

Add a `preserve` predicate applied to every candidate path before deletion,
in **all** modes (`cleanAll`, `cleanRepoRoots`, and the new targeted mode).

```go
// isPiOwnedNodeModules reports whether nm is a node_modules tree Pi is
// likely loading. Pi installs packages into a directory containing
// package.json; the well-known roots catch the npm/git/CLI install dirs
// even when discovered without a sibling package.json.
func isPiOwnedNodeModules(nm string) bool {
    parent := filepath.Dir(nm)
    if fileExists(filepath.Join(parent, "package.json")) {
        return true
    }
    abs, _ := filepath.Abs(nm)
    for _, root := range piNodeModulesRoots() {
        if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
            return true
        }
    }
    return false
}
```

`piNodeModulesRoots()` resolves the fixed, well-known Pi `node_modules`
paths against `~/.pi/agent` (env-overridable via `PI_AGENT_HOME`, default
`$HOME/.pi/agent`) and the pi CLI install dir (env `PI_INSTALL_DIR`, derived
from the running executable when unset):

- `$PI_AGENT_HOME/npm/node_modules`
- `$PI_AGENT_HOME/git` (any `node_modules` underneath)
- `$PI_INSTALL_DIR/node_modules`

`target` directories are never Pi-owned, so preservation only affects
`node_modules`. A `node_modules` *without* a sibling `package.json` and
*outside* the Pi roots is treated as a stray build artifact and cleaned
(this is the common "leftover from a deleted project" case).

### 2. Targeted mode: `-targets-only`

A new flag that restricts cleaning to `target` and `node_modules` **and**
validates each has the expected structure before deleting:

- `target/` — must be a directory (Rust/Cargo convention). Always cleaned;
  no Pi conflict.
- `node_modules/` — must be a directory whose **parent** has `package.json`
  **or** which itself contains a `.package-lock.json` (npm installs leave
  this marker). This is the "expected structure" gate: a bare directory
  named `node_modules` with no package context is left alone in this mode
  (unlike `cleanAll`, which still cleans stray ones).

`-targets-only` composes with `-repo-roots` (scan-restricted traversal +
structural gate) and with a positional root argument. It is the mode the
daemon runs.

Preservation (§1) still applies on top: even a structurally-valid
`node_modules` is skipped if it is Pi-owned.

### 3. Daemon: `-daemon`

```go
-daemon          run -targets-only on an interval until interrupted
-daemon-interval duration (default 30m)
```

- Loops with `time.Ticker`, calling the targeted clean each tick. Fires once
  immediately on start (so a freshly started daemon cleans right away), like
  `agentweave`'s `ServeWithPoll`.
- `signal.NotifyContext(SIGINT, SIGTERM)` for clean shutdown; prints
  `[start]`/`[stop]` lines to match `pulse`'s convention.
- Honours `-root` (positional) and `-repo-roots` so it can be scoped.
- All clean errors are logged to stderr and swallowed (daemon must not exit
  on a single EPERM); the loop continues. A run that produced any error
  logs a summary count but is not fatal.
- `PI_AGENT_HOME` / `PI_INSTALL_DIR` are read each tick so newly-installed
  Pi packages are preserved without a daemon restart.

`install-cleanartifacts.sh` (new) reuses `installer/launchagent.sh` to
register `com.gkgoat.scripts.cleanartifacts` running
`<bin>/cleanartifacts -daemon -daemon-interval 30m`, with
`--uninstall` to remove it — the same shape as `install-pulse.sh`.

### 4. Resilient FS handling (EPERM not fatal)

The current `removePaths` returns the first error from its buffered channel,
which is racy (the "first" error is nondeterministic) and aborts the whole
batch's *result reporting*. Restructure to:

- Collect **all** errors, not the first.
- Classify each error: `errors.Is(err, os.ErrPermission)` (covers EPERM and
  EACCES) and the literal `"operation not permitted"` string (SMB/network
  volumes sometimes surface a wrapped error without a clean errno) are
  treated as **non-fatal**: log to stderr, count, continue.
- Any other error (e.g. `os.ErrNotExist` after a concurrent deletion, or a
  genuine I/O error) is also logged and counted, but only non-permission
  errors contribute to the final non-zero exit. `ErrNotExist` is dropped
  entirely (the path is already gone — that's success).

Extract this into a reusable helper in `internal/fsutil` so the pattern is
available to other tools ("across, at least, `cleanartifacts`"):

```go
// internal/fsutil/rm.go
package fsutil

// RemoveAll removes path like os.RemoveAll but never returns a permission
// error: EPERM/EACCES and wrapped "operation not permitted" errors are
// logged to errOut (if non-nil) and treated as non-fatal. ErrNotExist is
// success. Other errors are returned.
func RemoveAll(path string, errOut io.Writer) error
```

`cleanartifacts` calls `fsutil.RemoveAll` per path; the batch helper returns
an `error` only if some path failed with a non-permission error.

The `filepath.WalkDir` in `cleanAll` also needs to tolerate EPERM on a
descended-into directory: a `walkFn` that returns an error aborts the walk.
Switch the `err != nil` branch to: if `errors.Is(err, os.ErrPermission)`,
log and `return nil` (skip that subtree) instead of returning the error.
This is the "across the tool" part — both the traversal and the removal
become EPERM-tolerant.

### 5. Exit codes

- `0` — completed; no non-permission errors (permission errors may have been
  logged but are non-fatal).
- `1` — one or more paths failed with a non-permission error.
- `2` — usage / config error (unchanged from Go `flag` default behaviour).

## CLI summary

```
cleanartifacts [flags] [root]

  (default)            walk root, remove target/ and node_modules/ (Pi-owned nm preserved)
  -repo-roots          only remove at repo-root level via .prtag workspace scan
  -targets-only        only remove target/ and node_modules/ with expected structure
  -daemon              run -targets-only on an interval until SIGINT/SIGTERM
  -daemon-interval d   daemon tick interval (default 30m)
  -dry-run             list paths that would be removed without deleting
  root                 directory to clean (default ".")
```

`-dry-run` is added as a safe way to preview, including for the daemon's
first tick during testing.

## Files changed

| File | Change |
|------|--------|
| `cleanartifacts/main.go` | preserve predicate, `-targets-only`, `-daemon`, `-dry-run`, EPERM-tolerant walk + remove, exit-code policy |
| `cleanartifacts/main_test.go` | tests for preservation, targeted structure gate, daemon single-tick, EPERM non-fatal, dry-run |
| `internal/fsutil/rm.go` | new — `RemoveAll` with permission-error tolerance |
| `internal/fsutil/rm_test.go` | new — EPERM/EACCES/non-existent/real-error cases |
| `install-cleanartifacts.sh` | new — LaunchAgent installer for the daemon, reusing `installer/launchagent.sh` |
| `docs/cleanartifacts-plan.md` | this document |

## Test plan

- `cleanAll` still removes stray `target`/`node_modules`, preserves a sibling
  `package.json` `node_modules`, and survives a planted EPERM (use a dir
  mode `0o500` + `chmod`-after-walk fixture, restored in cleanup).
- `-targets-only` leaves a bare `node_modules` with no package context;
  removes one with a sibling `package.json` that is *not* Pi-owned; preserves
  a Pi-rooted `node_modules`.
- `-daemon` runs one tick via a short `-daemon-interval` in a temp root and
  exits on context cancel.
- `-dry-run` prints paths and deletes nothing.
- `fsutil.RemoveAll`: EPERM → nil + logged; EACCES → nil + logged;
  `ErrNotExist` → nil, silent; a real error (e.g. path under a missing root)
  → returned.
- `go test -race ./cleanartifacts/... ./internal/fsutil/...` passes.
- Existing `cleanartifacts` tests stay green (behaviour is additive; the
  EPERM change only relaxes fatality).

## Open questions / deferred

- Whether to add a `-ignore` user-pattern flag — deferred; structural
  preservation covers the Pi case.
- Windows EPERM mapping — out of scope (repo is macOS/Linux-targeted; the
  string-match fallback covers the SMB-on-macOS case that prompted this).
# Sandbox-Level Auto-Interposition Plan

> **Status:** revised proposal for review — no implementation is included here.
>
> **First delivery:** fully committed automatic interposition for **Pi running as
> a macOS sandbox guest**. The guest's existing dylib `execve` hook and
> sandboxd communicate over an authenticated protocol. There is no staged
> `interpose` executable, PATH shim directory, command-wrapper binary, or
> daemon-side execution of guest commands. This is Pi-first—not Pi-only:
> independent sandbox guest targets may be added through the same committed
> policy path before host-mode rollout. The later host-mode/VM test objective is
> scoped in [`host-linux-auto-interposition-goal.md`](host-linux-auto-interposition-goal.md).

## Goal

When the Pi guest (or one of its descendants) calls `execve`, the macOS sandbox
shim asks sandboxd whether that invocation is one of the registered
interposers (`git`, `find`, `grep`, `kill`, `pkill`, `killall`, or `osascript`).
For a registered command, sandboxd evaluates the existing interposer wrapper
against a **remote guest Context**. That context makes required effects—such as
Git snapshot creation and a protected-command terminal confirmation—occur in
the requesting guest process's sandbox rather than in sandboxd or the
launcher. sandboxd then returns the final approved executable path and argv to
the shim, which makes the actual `execve`.

This makes automatic interposition an exec protocol feature of the current
host daemon/guest relationship. It does **not** add another program inside the
guest and does not rely on PATH behavior.

## Current problem

`interpose/core.Context` currently stores only process-derived data:

```go
type Context struct {
    Name, RealBinary, Dir string
    Args, Env             []string
}
```

`core.Run` and several wrappers/helpers then call process-global APIs directly:
`os.Getwd`, `os.Environ`, `/dev/tty`, config globals, filesystem functions, and
`exec.Command`. Those calls are appropriate for an installed host `interpose`
CLI, but they make it impossible for sandboxd to evaluate wrapper policy and
have its resulting effects take place in the guest.

The current macOS shim already intercepts `execve`, but it only sends an
open-style allow/deny request. That does not carry argv safely, cannot ask
wrapper policy to transform argv, cannot perform a pre-exec Git snapshot, and
has no authenticated operation channel by which the daemon can ask the guest
to perform a bounded context operation.

## Security and correctness invariants

1. **No additional guest executable.** Auto-interposition is implemented by
   the loaded macOS shim and sandboxd protocol. No staged dispatcher, PATH
   shim namespace, helper wrapper, or rewritten command binary is introduced.
2. **Guest effects remain guest effects.** A guest `git reset --hard` may
   create a snapshot only through an operation executed in that same guest
   session. It must not make sandboxd run host Git, read host config, use host
   credentials, mutate a host repo, or create a host snapshot.
3. **sandboxd is never a guest shell.** The daemon may evaluate policy and
   issue a capability-bound request to a specific guest session. It must never
   call `exec.Command`, a shell, or a host filesystem operation using a path,
   argv, or command supplied by the guest.
4. **Complete macOS exec coverage.** Decisions are made at the hooked `execve`
   boundary, not by prepending PATH. An absolute `/usr/bin/git`, a modified
   PATH, `execv`, and a direct `execve` are subject to the same dispatch
   decision. All exec-family entry points that can bypass the `execve` wrapper
   must be enumerated and mediated or explicitly denied.
5. **Fail closed for managed commands.** A protocol, policy, session,
   commitment, capability, or guest-operation failure denies a managed command
   rather than executing it without interposition. Unregistered commands use
   the normal sandbox execution decision; they are not accidentally treated as
   managed because their basename happens to match one.
6. **Protected commands stay fail closed.** `kill`, `pkill`, `killall`, and
   `osascript` require their committed allowlist or a fresh terminal PIN.
   `--no-interpose` is never an escape hatch for them.
7. **No recursion or substitution.** The shim receives a final, canonical,
   approved real executable path from the daemon. It cannot reclassify its own
   final execution as a new interposition request, rediscover a binary through
   PATH, or accept a caller-provided “real binary” environment value.
8. **Fully committed Pi policy and code.** For the first delivery, the
   selected logical home has an adopted verified sandbox commitment; Pi's
   executable/code map, sandbox config, auto-interposition policy, and macOS
   shim identity are all covered by the existing relevant commitments and
   rewrite-cache identity. Uncommitted mode is not an equivalent Pi security
   mode and must not silently enable this feature.
9. **Host CLI compatibility is retained.** The ordinary installed
   `interpose` executable remains a separate `HostOperations` adapter and has
   regression tests. Its host behavior must not leak into the sandbox protocol
   path.
10. **The protocol is an untrusted-input boundary.** All strings and byte
    counts from the dylib are bounded and length-delimited. The daemon
    canonicalizes paths, validates argument/environment size and count, binds
    messages to session and transaction IDs, and rejects malformed, replayed,
    cross-PID, or out-of-order messages.

## Architecture

### One abstraction, two operation backends

Keep wrapper-specific reasoning in Go, but move every external effect behind
an invocation context. The wrapper may decide a Git snapshot is required; it
asks the context to perform the typed guest operation. It never uses
`exec.Command` itself.

Use immutable invocation input and a narrow, typed operations interface. Exact
names may change, but the boundary must resemble this:

```go
type Invocation struct {
    CommandPath string   // canonical requested exec path
    CommandName string   // matched registered command, if any
    OriginalArgv []string
    Argv         []string // transformed argv
    Dir          string
    Env          []string // sanitized, bounded guest environment snapshot
    SessionID    string
    TransactionID string
    Origin       Origin   // HostCLI or SandboxExec
}

type Operations interface {
    RunApproved(ctx context.Context, request ApprovedCommand) (Result, error)
    ReadApprovedFile(ctx context.Context, path string) ([]byte, error)
    WriteApprovedFile(ctx context.Context, path string, data []byte, mode fs.FileMode) error
    ConfirmFreshPIN(ctx context.Context, prompt string) error
    Stderr() io.Writer
}

type Context struct {
    Invocation Invocation
    Operations Operations
    Policy     PolicyView
}
```

`ApprovedCommand` must contain a daemon-selected canonical executable path,
exact argv, working directory, sanitized environment, explicit stdio mode, and
a short-lived operation capability. It cannot be a shell string, arbitrary
callback, inherited host descriptor, or an arbitrary guest-selected program.
The final interface should be reduced after refactoring reveals the real
wrapper needs; it must remain typed and capability constrained.

`HostOperations` implements these calls using explicitly captured host cwd,
environment, streams, and host subprocess/filesystem APIs at the `interpose`
CLI boundary.

`RemoteGuestOperations` is used only by the daemon while it handles an active
macOS exec transaction. Each method sends a capability-bound `GUEST_OPERATION`
request to the requesting dylib and waits for that exact transaction's result.
The shim performs the operation locally in the guest process/session and
returns a structured result. This is the required “Context that actually does
stuff in a guest context”; it is not a request for sandboxd to do the work.

The wrapper context should be renamed `InvocationContext` if needed to avoid
confusion with Go's `context.Context`. All operations still accept standard
`context.Context` for cancellation and deadlines.

### Guest-side operation executor in the dylib

The existing loaded macOS dylib gains a small protocol operation executor. It
is library code, not an executable or a command dispatcher.

For an active transaction, it can perform only operations granted by a daemon
capability:

- **`RUN_APPROVED`**: spawn one canonical daemon-approved executable with exact
  argv/cwd/env and guest stdio, wait for it, and return only exit/result data.
  The implementation must use an exec-safe primitive (prefer `posix_spawn`)
  and an explicitly constructed environment; it must not run a shell or search
  PATH. The child remains under the same shim/daemon sandbox policy.
- **`READ_FILE` / `WRITE_FILE`**: operate only on canonical paths and modes in
  the capability. Use these only if conflict restoration cannot be expressed
  through a bounded Git operation; prefer reducing this surface.
- **`CONFIRM_PIN`**: open the guest's `/dev/tty`, write the daemon-provided
  fresh challenge, read the response, and return a match/no-match result. If
  the guest lacks a controlling tty, return failure. It never opens a daemon
  or host terminal and never accepts piped stdin as confirmation.

The daemon signs/binds each capability to the authenticated session ID,
requesting pid, exec transaction ID, operation kind, canonical parameters, and
single-use nonce. The shim verifies the capability before operation; sandboxd
also rejects a result without a matching outstanding operation. A guest cannot
reuse a capability for another process, command, transaction, or target path.

The implementation needs two independent authenticated connections per guest
session (or an equivalently multiplexed protocol):

- an **exec request/response** path, on which the hooked thread waits for the
  final `EXEC_ALLOW`/`EXEC_DENY`; and
- an **operation channel**, serviced by the dylib while the daemon is
  evaluating that transaction and capable of returning nested operation
  results.

Do not attempt nested guest operations on the one blocked exec request stream.
This would deadlock when `Git.Before` requests a snapshot while the original
`execve` waits for its decision. The design must state connection lifecycle,
threading, timeouts, and cleanup before implementation. In particular, it must
not call non-reentrant allocator/stdio functions from an unsafe signal handler;
`execve` is a normal hooked call, but multi-threaded fork/spawn constraints
still apply and must be tested.

### Policy ownership and commitment

Add a strict, versioned `autoInterpose` section to the sandbox config for the
selected logical home:

```json
{
  "version": 1,
  "autoInterpose": {
    "enabled": true,
    "platform": "darwin",
    "commands": ["git", "find", "grep", "kill", "pkill", "killall", "osascript"],
    "policy": {
      "extraProtectedPaths": ["/guest/private"],
      "disableSnapshot": ["/guest/no-snapshots"],
      "commandAllowlist": {
        "kill": [["-0", "{pid}"]],
        "pkill": [],
        "killall": [],
        "osascript": []
      }
    }
  }
}
```

Use the **sandbox-owned policy** design for this release: the Git, find/grep,
and protected-command rules relevant to guest interposition live in the
committed sandbox config. Do not load the current host
`~/.config/interpose/config`, host command allowlist, host `HOME`, or host
anchor as guest policy. The daemon creates an immutable in-memory `PolicyView`
after verifying the selected logical home's sandbox config proof and anchor.

For the first Pi release, auto-interposition is enabled only when all of the
following verify:

1. the logical home is adopted and its sandbox config proof matches its anchor;
2. the target is Pi or a Pi child under the registered sandbox session;
3. Pi's full-file cumulative hash map/authorized map digest is accepted by the
   sandbox policy;
4. the actual dylib's macOS cdhash meets the existing hardened-runtime
   library-load pin and all staged/rewrite inputs match the committed cache
   identity; and
5. `autoInterpose.enabled` and its strict Darwin command policy validate.

If any check fails, launch must fail rather than fall back to an uncommitted
interposition policy or a pre-existing committed cache entry. A later,
explicitly-designed development/test profile may relax adoption, but it is out
of scope for this delivery and must never share
`<home>/Library/Caches/sandbox` with committed sessions.

### Refactor existing wrappers before protocol integration

All wrapper external effects must move to the context boundary first:

- `core.Run` delegates to `ctx.Operations.RunApproved`; `core.Execute` is only
  the host CLI adapter that resolves the host real binary and constructs a
  `HostOperations` context.
- `Git` helpers (`gitRepoRoot`, `gitOutput`, `gitSnapshot`, conflict
  restoration) use context operations. Refactor `internal/restoreconflict` to
  accept an adapter so no hidden direct host `os`/`exec` call remains.
- `Find` and `Grep` consume `ctx.Policy`, not `tcc.ProtectedRoots()` derived
  from global HOME/config. Their argv transforms remain pure unit-tested
  functions.
- `ProtectedCommand` receives an already verified allowlist from `PolicyView`
  and invokes `ConfirmFreshPIN` instead of opening host `/dev/tty`.
- Diagnostics go through `ctx.Operations.Stderr()`.

Do not add a `Guest bool` while retaining direct global calls. That would
create an abstraction that silently performs future effects on the host.

## macOS exec protocol

### Request and decision flow

Replace the current whitespace-delimited `execve`/open reuse with a versioned,
length-delimited protocol. It must be implemented in shared C protocol types
and independently fuzzed/round-trip tested by daemon code.

1. The shim resolves the requested path as it does for current policy checks,
   captures bounded argv, a sanitized/bounded env delta, cwd, pid/process
   identity, session credential, and recursion token state, then sends
   `EXEC_REQUEST`.
2. sandboxd authenticates the session and transaction, canonicalizes the path,
   determines the command identity from a committed command table (not merely
   basename), and applies ordinary sandbox exec policy.
3. If it is unregistered, sandboxd returns `EXEC_DIRECT` only after ordinary
   sandbox authorization. If it is a registered auto-interposed command,
   sandboxd builds `RemoteGuestOperations`, immutable invocation input, and
   committed `PolicyView`; it invokes `Transform` then `Before`.
4. Any `RemoteGuestOperations` call becomes a single-use
   `GUEST_OPERATION_REQUEST`. The shim executes the bounded operation locally
   and returns `GUEST_OPERATION_RESULT`. The daemon validates it before the
   wrapper can continue.
5. On success, sandboxd returns `EXEC_ALLOW` containing the canonical original
   executable path, transformed argv, a sanitized environment delta, and a
   daemon-issued single-use final-exec capability. On rejection/error/timeout,
   it returns `EXEC_DENY` with a bounded diagnostic class.
6. The shim validates the final capability and makes `real_execve` using the
   returned canonical executable and transformed argv. It applies only the
   returned allowed environment delta; arbitrary guest `DYLD_*` and protocol
   control variables remain rejected.

There is no `EXEC_INTERPOSE` response pointing to another binary. The
interposition happens during the protocol evaluation, then the guest executes
the real selected command directly.

`After` cannot be treated as a synchronous wrapper hook after a successful
`execve` because the calling image is replaced. Current wrappers have no
meaningful `After` behavior. Before implementation, replace `After` with an
explicit optional completion-event model or remove it; do not promise a
post-exec callback that cannot occur. If later policies require completion
observability, the daemon may record child exit/disconnect events, but those
are audit events, not a host command execution hook.

### Recursion and child handling

The `RUN_APPROVED` child needed for a pre-exec Git helper inherits the shim.
Its `execve` must be authorized but not recursively re-run the same wrapper.
The capability establishes a one-hop, daemon-tracked internal-exec state:

- the shim includes the internal-exec capability on the helper's exec request;
- sandboxd verifies its session, target, argv, and expiry and returns a direct
  authorization for that exact helper invocation;
- the capability is consumed exactly once; and
- no caller-controlled environment marker can create, clear, or extend it.

This avoids treating helper subprocesses as bypasses while preventing snapshot
creation from recursively snapshotting itself. A normal child of Pi without a
valid internal capability begins a fresh auto-interposition transaction.

### Command and path rules

The command table is constructed at startup from a fixed system command set
and committed sandbox policy. For each enabled command it records canonical
acceptable requested paths (for example the system `git` path), executable
identity as appropriate to the existing sandbox rules, and the wrapper name.
It rejects symlink/path races and command-table entries in writable directories.

The daemon must distinguish an execution request by canonical path and
registered identity. It must not wrap an arbitrary executable named `git` in a
project directory. Requests to non-table paths are ordinary sandbox exec
requests. The policy schema may allow an explicit additional command path only
if its canonical full-file hash is present in the committed code map and its
parent chain meets sandbox ownership/mode requirements.

## Pi-first delivery scope

The first production target is Pi launched through `sandbox/run.sh` on macOS
with a real adopted logical home. It must run with committed config, committed
Pi code-map identity, the verified macOS dylib cdhash constraint, and committed
rewrite cache. The initial integration tests should invoke a small Pi workflow
that uses `git`, `find`, and `grep`, plus controlled protected-command cases,
rather than relying only on synthetic test executables.

Pi is the first integration target, not the only eligible guest. Once an
independent target has a committed code-map/policy/session registration, it can
be added and fixed through this guest-sandbox implementation without waiting
for host-mode work. Host rollout and its Linux VM test goal are deliberately
separate; see
[`host-linux-auto-interposition-goal.md`](host-linux-auto-interposition-goal.md).

Do not first deploy this feature as generic host-wide automatic
interposition. That host work has a different threat boundary and must not be
smuggled into this implementation.

## Linux position and known gaps

Linux automatic interposition is **deferred**. The current Linux launcher is a
namespace/mount setup mechanism, not a native exec protocol peer, and the
first goal does not require it.

All Linux limitations, deferrals, and future VM-assisted alternatives for this
sandbox live in [`sandbox/linux-known-gaps.md`](linux-known-gaps.md). Do not
scatter provisional Linux auto-interposition designs through this plan,
`README` files, or implementation comments. In particular:

- an `autoInterpose.enabled` configuration must be rejected by the Linux
  launcher for this release; it must not silently provide a weaker PATH overlay;
- the existing Linux sandbox behavior remains unchanged;
- later host work may use the host's ability to spawn Linux VMs, where a guest
  kernel/VM monitor can provide an enforceable implementation for behaviors
  Linux's “a process can make syscalls wherever it can reach” model cannot
  faithfully interpose natively.

## Implementation sequence

1. **Freeze and inventory behavior.** Add host-wrapper regression tests for
   output, exit codes, Git snapshots/conflict restoration, find/grep policy,
   `--no-interpose`, and protected-command PIN denial with no tty. Inventory all
   direct `os`, `os/exec`, config-global, and terminal calls in wrappers and
   helper packages.
2. **Create the context capability boundary.** Introduce immutable invocation
   data, `PolicyView`, typed `Operations`, result/error semantics, and
   `HostOperations`. Migrate every wrapper and `restoreconflict`; prohibit
   direct host side-effect APIs in wrapper code by review/test convention.
3. **Add committed Pi auto-interpose config.** Extend `sandbox/config`,
   commitment leaf generation, proof verification, config tests, and docs with
   strict Darwin-only sandbox-owned policy. Add hard launch rejection for
   invalid/not-adopted Pi auto-interposition state.
4. **Design and test protocol primitives.** Version shared message framing,
   session authentication, transaction/capability records, exact size limits,
   cancellation/timeouts, and structured error codes. Add parser fuzz tests,
   replay/cross-session tests, and no-nested-stream deadlock tests before
   changing `exec.c`.
5. **Implement guest operation execution in the dylib.** Add the operation
   channel, capability validation, `posix_spawn`-based `RUN_APPROVED`, minimal
   approved file operations if still necessary, guest tty PIN confirmation,
   result framing, cleanup, and internal-exec capability propagation. No Go
   dispatcher or shell is added to the guest.
6. **Implement daemon remote context and wrapper execution.** On a managed
   `EXEC_REQUEST`, create `RemoteGuestOperations`, evaluate Transform/Before,
   collect transformed argv, and return a final direct exec decision. Ensure
   every operation is attributed to the initiating Pi session and audit only
   metadata/digests—not secret argv/environment values unless explicitly safe.
7. **Replace macOS exec authorization.** Update `sandbox/macos/dylib/exec.c`,
   shared protocol code, and daemon handlers from the old open-style request to
   the versioned exec flow. Mediate `execve`, `execv`, and other relevant
   exec-family paths; reject unmediated variants until they are covered.
8. **Integrate Pi and commitment/cache identity.** Include config/policy,
   daemon protocol version, and dylib runtime identity in the rewrite cache
   identity. Run committed Pi end-to-end tests from the logical home and prove
   no uncommitted/rewrite-cache cross-use exists.
9. **Harden and document.** Update sandbox/interpose documentation, record
   all Linux status only in the central known-gaps document, remove deprecated
   potential control variables, and write an operator migration/recommit path.

## Required tests

### Core/context tests

- A recording `Operations` implementation proves every Git helper command and
  restoration file operation routes through the context.
- Existing host CLI behavior, output, and exit status remain stable through
  `HostOperations`.
- Find/grep use an injected policy rather than the test account's HOME/config.
- Protected-command allowlist and confirmation use `PolicyView` and typed tty
  confirmation; no tty fails closed.
- Context methods reject shell strings, arbitrary executable paths, invalid
  operation kinds, and unexpected descriptors.

### Protocol and dylib tests

- Round-trip, malformed-length, oversized argv/env, embedded NUL, malformed
  UTF-8 policy where applicable, replay, expired capability, wrong pid, wrong
  session, wrong transaction, and duplicate-result tests.
- An active `EXEC_REQUEST` can make a nested approved guest operation without
  deadlocking; timeout/cancel/disconnect tears down only that guest session.
- `RUN_APPROVED` invokes only the capability-bound real executable/argv/cwd/env
  inside the guest; attempted substitution, shell syntax, or PATH lookup fails.
- PIN confirmation reads guest `/dev/tty`; a piped stdin or missing tty cannot
  satisfy it.
- Internal helper exec consumes its one-shot capability and cannot recurse;
  normal child exec begins a new transaction.

### Committed Pi/macOS integration tests

- A committed Pi sandbox session automatically transforms/interposes
  PATH-resolved and absolute-path `git`, `find`, `grep`, and every protected
  command listed in policy.
- A destructive guest Git operation snapshots the allowed guest repo. A host
  repo with the same content/path suffix, host Git config, host credentials,
  host cwd, and host PATH remain untouched.
- Changed sandbox config, auto-interpose policy, Pi hash map, dylib, or protocol
  version invalidates the committed runtime/cache identity. Invalid/missing
  adoption fails launch; it does not use stale committed artifacts.
- Caller-controlled `INTERPOSE_*`, `SANDBOX_*`, `DYLD_*`, PATH, argv, and
  environment attempts cannot choose a different command identity, real path,
  policy, capability, or operation session.
- Direct `execve`, `execv`, absolute command paths, and modified PATH receive
  the same managed-command decision. An uncovered exec-family entry point is
  rejected until supported.
- Exit codes, signals, stdin/stdout/stderr, process groups, and guest cleanup
  match direct guest command behavior. No daemon process becomes a child
  command executor.
- macOS signature tests verify exact dylib cdhash pinning for the hardened
  runtime load, and reject a substituted dylib or altered rewrite artifact.

## Expected implementation areas

- `interpose/core/` and `interpose/wrappers/`, plus
  `internal/restoreconflict`, for context/policy/side-effect refactoring.
- `sandbox/config/`, `sandbox/hashmap/`, `agentcommit/`, and logical-home
  configuration tests for committed Pi auto-interpose policy.
- `sandbox/common/` protocol definitions, `sandbox/macos/dylib/exec.c` and
  support files, and `sandbox/daemon/` for session, transaction, capability,
  remote-operation, and audit code.
- `sandbox/run.sh`, `sandbox/macos/sandbox_wrapper.sh`, rewrite/cache code, and
  macOS tests for Pi-first committed launch wiring.
- [`sandbox/linux-known-gaps.md`](linux-known-gaps.md),
  [`sandbox/host-linux-auto-interposition-goal.md`](host-linux-auto-interposition-goal.md),
  `sandbox/linux/README.md`, `sandbox/daemon/README.md`, and
  `docs/interpose.md` for scoped documentation.

## Non-goals

- A staged interposer/dispatcher binary, command symlink directory, or PATH
  overlay as the auto-interposition mechanism.
- sandboxd spawning or shelling out to execute a guest command.
- Claiming Linux auto-interposition support or adding a partial Linux fallback.
- Generic host-wide automatic interposition before the committed Pi/macOS
  sandbox deployment is complete.
- Making `--no-interpose` available for protected process-control commands.
# Host Pi and Linux Auto-Interposition Goal

> **Status:** scope and successor-goal document. This is not implementation
> authorization for host-wide interposition. The macOS guest protocol work in
> [`auto-interposition-plan.md`](auto-interposition-plan.md) remains the first
> implementation.

## Goal

After Pi can run as a fully committed macOS sandbox guest with automatic
`execve`-level interposition, move Pi to the host control plane. A host Pi may
then create disposable Linux VMs to test and develop a Linux automatic
interposition implementation at an enforcement boundary stronger than a Linux
PATH wrapper or in-process library hook.

The preferred direction is a **gVisor-dependent** Linux test/runtime design.
`ptrace` or a similarly explicit syscall-observation mechanism may be used for
prototyping, diagnostics, or a constrained test harness, but it is not by
itself evidence of a complete hostile-guest security boundary. The ultimate
mechanism must be selected by a separate approved design based on actual
coverage and failure behavior.

## Relationship to the macOS guest work

- **Pi-first, not Pi-only:** Pi is the first end-to-end macOS guest target. Any
  independently committed sandbox guest may use and improve the macOS protocol
  implementation without waiting for host-mode rollout.
- **Separate milestones:** the macOS guest implementation, host Pi move, Linux
  VM harness, and a production Linux enforcement model are independently
  shippable/fixable milestones. A delay in one does not block correctness fixes
  in another.
- **No premature generic host rollout:** host Pi is a control-plane transition,
  not authorization to install global PATH wrappers or enable uncommitted
  auto-interposition for arbitrary host applications.
- **Central Linux status:**
  [`linux-known-gaps.md`](linux-known-gaps.md) remains the authoritative list
  of Linux limitations and deferrals. This document describes the goal and
  acceptance criteria; it must link new gaps there rather than duplicate them.

## Why a Linux VM boundary

A Linux process can make syscalls wherever the kernel permits it. PATH
interposers do not cover absolute execution or direct `execve`; preload hooks
do not cover all binaries/code paths; and a namespace changes resource
visibility but is not inherently an observation/decision point for every
syscall. Do not claim macOS-style exec mediation merely because a wrapper is
present.

A disposable VM gives host Pi a controllable guest kernel/runtime boundary in
which to test syscall/exec mediation, lifecycle controls, network/filesystem
isolation, and failure behavior without treating the host process itself as the
trusted point of interception. gVisor is preferred because it supplies a
well-defined application-kernel boundary and sandbox-oriented syscall handling.
The eventual design must state exactly which gVisor mode, version, host
requirements, unsupported syscalls, and fallback behavior it relies on.

## Required properties for the later Linux design

1. **Commitment continuity:** a host Pi session, VM image/runtime, guest policy,
   target code identity, and auto-interposition policy must be cryptographically
   bound to the committed logical-home sandbox configuration. A VM must not be
   an untracked escape from the sandbox commitment/cache model.
2. **Exec coverage:** the selected mechanism must mediate direct `execve`,
   absolute paths, modified PATH, and relevant exec-family variants. If an
   executable/path/syscall cannot be mediated, document and reject it rather
   than silently execute it without the policy.
3. **Guest-context effects:** Git snapshot/helper activity and protected-command
   confirmation occur in the Linux VM guest, never in host Pi or a host daemon.
   The host control plane must not become a guest-command executor.
4. **Typed protocol/capabilities:** retain the macOS design principle that
   policy evaluation may issue only bounded, session/transaction-bound typed
   operations. No guest shell strings, generic host command RPC, or ambient
   control environment is permitted.
5. **Disposable lifecycle:** each VM has unique session identity, bounded
   resources, explicit process/VM cleanup, no cross-session writable cache,
   and audited teardown. It must not accidentally expose the real host home,
   credentials, terminal, daemon socket, or user library.
6. **Fail-closed managed commands:** unavailable mediation, invalid commitment,
   protocol failure, unsupported syscall/command, or missing terminal for a
   protected prompt denies the managed command. It cannot degrade to PATH-only
   coverage.
7. **Parity is proven, not presumed:** preserve transformed argv, exit/signal
   semantics, stdio, process groups, terminal behavior, and recursive-helper
   handling only where tests show it. Differences from macOS must be recorded
   in `linux-known-gaps.md`.

## Phased successor plan

### 1. Host Pi control-plane preparation

Define how a committed host Pi is launched, how it obtains a scoped VM-launch
capability, where VM artifacts live under the selected logical home, and how
those artifacts participate in cache and code-map identity. Ensure host Pi can
launch only approved VM configurations; it must not accept an arbitrary image,
kernel, gVisor binary, mount, network setting, or command from an untrusted
prompt/guest.

### 2. Disposable Linux VM test harness

Build a deterministic test VM/image with no real user home or credentials.
Host Pi creates it with an explicit, restricted project/data interface, runs
interposition fixtures, collects bounded structured results, and destroys it.
Start with test targets that exercise `git`, `find`, `grep`, direct/absolute
`execve`, modified PATH, and protected-command terminal/no-terminal cases.

The harness may use `ptrace` or a comparable observer to map real syscall and
exec behavior, identify gaps, and test protocol assumptions. Its coverage data
must not be represented as production security enforcement.

### 3. gVisor enforcement prototype

Choose a gVisor deployment mode and implement a narrow proof that its
application-kernel boundary can perform the required command/exec decision
flow. Specify the policy transport, identity binding, guest effect executor,
recursion behavior, unsupported syscall response, and audit data. Compare all
behavior against the macOS protocol tests.

A design that cannot mediate a required path must either reject that path or
remain experimental. Do not add a silent namespace/PATH fallback.

### 4. Production decision and rollout

Only after threat-model review and adversarial VM tests should the project pick
a supported Linux implementation. Update `linux-known-gaps.md` with resolved
and remaining limitations, document exact prerequisites, and require committed
configuration to enable it. Linux support may ship only with explicit scope;
it never retroactively implies generic native Linux parity.

## Acceptance tests for the VM effort

- Host Pi can create and tear down a uniquely identified disposable Linux VM
  without granting it the real host home, credentials, Library/config state,
  daemon socket, or broad host filesystem/network access.
- VM image, kernel/gVisor runtime, guest policy, target hashes, and session
  configuration are verified against the committed host/logical-home identity.
- Direct `execve`, absolute command paths, PATH mutation, and each relevant
  exec-family path yield an observable decision; unsupported paths fail closed.
- Guest `git reset --hard` creates any snapshot only inside the VM project;
  host repositories/config/credentials remain unchanged.
- Protected commands require committed allowlist behavior or a guest terminal
  PIN; no host stdin/tty can satisfy a guest confirmation.
- Protocol replay, cross-VM/session capability use, runtime/image substitution,
  VM escape attempts, daemon-control-plane command execution, and stale cache
  reuse are rejected.
- Signal, exit-code, stdio, child-process, and cleanup behavior are measured
  and differences recorded in the Linux known-gaps document.

## Non-goals

- Declaring the current Linux namespace launcher to be automatic
  interposition-capable.
- Using a PATH wrapper, LD_PRELOAD hook, or `ptrace` experiment as a complete
  production security guarantee without an approved enforcement design.
- Blocking independent fixes and targets in the macOS guest implementation
  until the host or Linux work begins.
- Letting host Pi act as an unbounded VM/command launcher for guest-controlled
  inputs.
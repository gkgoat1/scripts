# Linux Sandbox Known Gaps

> **Status:** living documentation for Linux-specific limitations, deferred
> capabilities, and VM-assisted follow-up work. Update this document—not
> scattered sandbox plans or source comments—when a Linux feature is unavailable
> or only partially enforceable.

## Scope and current implementation

The current Linux sandbox (`sandbox/linux/sandbox.sh` and
`sandbox/linux/probe.c`) provides an allow-list-oriented mount-namespace
launcher. It creates a private namespace/root, exposes selected paths and
runtime dependencies, and starts the requested program there. It is useful
filesystem and namespace isolation, but it is not equivalent to the macOS
shim/daemon protocol architecture.

Linux support must be described based on what the launcher actually enforces,
not on an intended future design.

## Known gap: automatic command interposition

**Status: deferred; not implemented natively on Linux.**

Sandbox-level auto-interposition's first production target is Pi in a fully
committed macOS sandbox. On macOS, the loaded guest dylib can hook `execve` and
communicate with sandboxd to evaluate an interposer context and perform bounded
operations in the guest process. The Linux namespace launcher has no matching
exec protocol peer.

Accordingly, while this gap remains open:

- Linux must reject a sandbox configuration that enables `autoInterpose`; it
  must not silently weaken the feature to a PATH overlay, shell wrapper, or
  best-effort command lookup.
- Linux sandbox documentation and CLI output must say automatic
  interposition is unavailable rather than implying that `git`, `find`,
  `grep`, `kill`, `pkill`, `killall`, or `osascript` receive macOS-equivalent
  treatment.
- No Linux-specific partial mechanism should be added to satisfy an interface
  while bypassing absolute-path execution or direct syscall execution.

## Why native parity is not assumed

On Linux, a process which reaches a syscall interface can generally issue the
syscall directly. PATH wrappers only affect lookup conventions; they do not
mediate absolute paths, `execve`, or a program that invokes syscalls directly.
Library-based hooks likewise do not establish a complete boundary for hostile
or statically linked/native code. A mount namespace is valuable for controlling
visible filesystem resources, but it is not in itself an interposition point
for all behavior the process can request from the kernel.

Do not claim a complete native Linux implementation until its mediation point,
coverage, threat model, kernel/version requirements, failure behavior, and
integration tests are specifically designed and verified.

## Future direction: host-spawned Linux VMs

After the committed Pi/macOS deployment, host-mode work may spawn Linux VMs.
A VM provides a stronger place to implement controls that cannot be faithfully
achieved by a Linux process observing or wrapping another Linux process's
ordinary syscall behavior. Any VM approach must have its own approved plan
covering:

1. guest-kernel and monitor/hypervisor boundary;
2. command/exec mediation point and behavior for direct syscall paths;
3. identity and lifecycle binding between host sandbox session and VM;
4. policy/configuration commitment, immutable image/runtime identity, and
   cache isolation;
5. guest I/O, networking, filesystems, process signals, terminal prompts, and
   cleanup semantics; and
6. adversarial tests proving that a guest cannot escape, replay host protocol
   messages, or turn the host control plane into a command executor.

A VM is not automatically a complete solution. It is a candidate enforcement
boundary that must be designed before implementation.

## Other Linux gaps

Keep each additional issue in this format:

### `<feature or property>`

- **Status:** unavailable, partial, experimental, or resolved.
- **Current behavior:** what the launcher demonstrably does today.
- **Why:** Linux/kernel/runtime constraint or implementation limitation.
- **Operator impact:** safe usage and any explicit rejection/failure behavior.
- **Future work:** link to an approved plan, prerequisites, and required tests.

When a gap is resolved, retain a brief historical note and link to the tests
that demonstrate the supported behavior rather than deleting the record.
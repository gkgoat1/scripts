# Sandbox Refactor Plan

This plan covers four related pieces of work on the cross-platform
sandbox tooling:

1. Move macOS file-access/TCC deny decisions from the injected dylib into the
   daemon, reusing the existing `interpose/policy/tcc` package.
2. Add a `clang-format` script and configuration for the C sources, including
   a new cross-platform common source tree.
3. Teach `internal/proxypass` to honor host HTTP(S)/`NO_PROXY` environment
   variables.
4. Support multiple sandboxes, a common C library usable by both macOS and
   Linux, and transparent socket proxy passthrough via daemon-mediated IPC
   (SCM_RIGHTS fd passing).

Dependencies/ordering:

- Phase A (`clang-format`) is a prerequisite to the large dylib refactor.
- Phase B (policy centralization) can land independently of networking work.
- Phase C (upstream proxy support) is a prerequisite to Phase D.

---

## Phase A: C formatting (`clang-format`)

### Goals

- All C source in `sandbox/` is formatted automatically and consistently.
- The multi-file dylib and common library can be formatted as they are written.

### Steps

- [ ] Add a root `.clang-format`. Proposed starting point:
  ```yaml
  BasedOnStyle: LLVM
  IndentWidth: 4
  ColumnLimit: 100
  AllowShortFunctionsOnASingleLine: Inline
  SortIncludes: false
  ```
  Tweak the style in one review pass; do not re-tune repeatedly.
- [ ] Add `sandbox/format-c.sh`:
  - Requires `clang-format` and exits with a usable error if missing.
  - Runs `clang-format -i` on `sandbox/linux/probe.c`, every `.c`/`.h` in
    `sandbox/common/`, and every `.c`/`.h` in `sandbox/macos/dylib/`.
- [ ] Add a `fmt` target to the root `Makefile` that calls `sandbox/format-c.sh`.
- [ ] Optionally add a `fmt-check` make target for CI.
- [ ] Re-format early files (`probe.c` and the current single-file dylib) once
   the config is stable.

### Files

- New: `.clang-format`, `sandbox/format-c.sh`
- Update: `Makefile`
- Touch for formatting only: `sandbox/linux/probe.c`,
  `sandbox/macos/sandbox.dylib.c`

---

## Phase B: Move TCC deny logic into the daemon

### Goals

- The injected dylib should not make any local TCC decisions.
- TCC decisions are centralized in `sandboxd`, where they reuse the already-maintained
  `interpose/policy/tcc` package.

### Current state

`sandbox/macos/sandbox.dylib.c` has `allowed_path()`, which locally denies paths
containing `~/Documents`, `~/Desktop`, and `~/Downloads` by substring match. The
existing Go package `interpose/policy/tcc` already provides
`NormalizePath()`, `IsProtected()`, and config-based `ExtraProtectedPaths`.

### Design

1. Import `github.com/gkgoat1/scripts/interpose/policy/tcc` in
   `sandbox/daemon/main.go`.
2. Add a daemon-side policy engine:
   - **TCC deny**: `tcc.IsProtected(path)` rejects protected home directories
     (`Library`, `Documents`, `Desktop`, `Downloads`, `Pictures`, `Movies`,
     `Music`, and any `ExtraProtectedPaths` from `~/.config/interpose/config`).
   - **Dotfile deny**: any path component beginning with `.` is denied unless it
     is in the shell-config read-only allow-list.
   - **Read-only dotfiles**: `.zshrc`, `.bashrc`, `.profile`, `.bash_profile`,
     `.zprofile`, `.zlogin` are reported as `RO`.
3. Resolve relative paths at the source. The dylib must convert a relative path
   to an absolute path using the process's current working directory before
   sending an `OPEN` request. This lets `tcc.NormalizePath()` and
   `tcc.IsProtected()` work correctly inside the daemon.
4. Change the `OPEN pid path` protocol response to include the policy decision:
   - `UPDATED` — allowed and the process identity hash was updated.
   - `ALLOWED` — allowed, hash unchanged.
   - `RO` — allowed read-only only, hash unchanged.
   - `DENIED` — path denied.
5. In the dylib:
   - Remove `allowed_path()`.
   - Send `OPEN pid path` for every intercepted `open()`.
   - On `DENIED`, return `-1` with `errno = EACCES`.
   - On `RO`, additionally reject write opens (`O_WRONLY | O_RDWR | O_APPEND |
     O_CREAT | O_TRUNC`) with `EACCES`.
   - On `ALLOWED`/`UPDATED`, proceed to the real `open()`.
6. `execve()` reuses the same `OPEN` request; a `DENIED` response prevents the
   exec.

### Steps

- [ ] Add `"github.com/gkgoat1/scripts/interpose/policy/tcc"` to `sandbox/daemon/main.go`.
- [ ] Implement `pathPolicy(path string, forWrite bool) string` in the daemon using
  `tcc.IsProtected()` plus the dotfile rules.
- [ ] Update `server.command()` to return the new response codes.
- [ ] Keep `updateHash()` as the second part of the decision — only allowed
  paths can trigger an interpreter-hash update, and only for configured
  extensions.
- [ ] Remove `allowed_path()` from `sandbox/macos/sandbox.dylib.c`.
- [ ] In the dylib, resolve relative paths with `getcwd()` before sending the
  `OPEN` request.
- [ ] In the dylib, parse open flags from the variadic argument so `RO` can be
  enforced.
- [ ] Update `sandbox/daemon/README.md` with the new command responses and
  dependency on `interpose/policy/tcc`.
- [ ] Test that protected directories and dotfiles are blocked while shell
  configs are readable but not writable.

### Files

- `sandbox/daemon/main.go`
- `sandbox/macos/sandbox.dylib.c`
- `sandbox/daemon/README.md`

---

## Phase C: Upstream proxy support in `internal/proxypass`

### Goals

- The ephemeral loopback proxy server forwards outbound traffic through the
  host's proxy configuration when set.
- The daemon from Phase D will reuse this proxy resolution logic internally for
  its socket-broker `CONNECT` command.

### Current state

`internal/proxypass/proxypass.go` calls `dialTCP()` directly to the origin for
both plain HTTP and `CONNECT` tunneling. It ignores `HTTP_PROXY`,
`HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`, and credentials.

### Design

1. Use Go's `http.ProxyFromEnvironment` (or an equivalent helper) to decide when
   a destination should go through an upstream proxy.
   - For HTTP requests, pass the original `http.Request.URL`.
   - For `CONNECT` targets, use a synthetic URL so `NO_PROXY` is applied
     consistently.
   - Honor both upper- and lower-case variable names.
2. `dialTCP` behavior:
   - If no proxy applies, dial the origin directly.
   - If a proxy applies, `dialViaProxy(ctx, proxyURL, targetHost)`:
     - Open a TCP connection to the proxy.
     - Send `CONNECT target_host:port HTTP/1.1` with `Host: target_host:port`.
     - If the proxy URL contains userinfo, add `Proxy-Authorization: Basic`.
     - Read the `200 Connection established` response.
     - Return the socket; it is now a tunnel to the origin.
3. For plain HTTP via an upstream proxy, forward the original request to the
   upstream proxy using the full origin URL in the request line, preserving all
   non-hop-by-hop headers.
4. Keep `DefaultDialTimeout` and the existing retry/backoff behavior.
5. Expose no new public API; callers still use `proxypass.Start(ctx)` and
   `s.Env(base)`.

### Steps

- [ ] Add `proxyForHost(scheme, host string) *url.URL` wrapping
  `http.ProxyFromEnvironment`.
- [ ] Implement `dialViaProxy(ctx, proxyURL, targetHost)` with CONNECT handshake
  and credential support.
- [ ] Refactor `handleHTTP` to route through an upstream proxy when applicable.
- [ ] Refactor `handleConnect` to use `dialViaProxy` when applicable.
- [ ] Add tests in `internal/proxypass/proxypass_test.go`:
  - Nested upstream HTTP proxy for plain HTTP.
  - Nested upstream HTTP proxy for `CONNECT` tunneling (HTTPS).
  - `NO_PROXY` bypass.
  - Upper-case / lower-case env vars.
  - Credential propagation.
- [ ] Verify `gitall -proxy` and `pulse -proxy` still work with no caller changes.

### Files

- `internal/proxypass/proxypass.go`
- `internal/proxypass/proxypass_test.go`

---

## Phase D: Single-session daemons, common C library, and socket fd-passing

### Goals

- All guest network sockets are brokered by the daemon via `SCM_RIGHTS` fd
  passing (Option A).
- Sandboxed code sees **no** proxy environment variables and needs **no**
  special internal env variables; the `SANDBOX_DAEMON_SOCKET` variable remains
  visible and accessible to guests.
- Host proxy handling is fully encapsulated in the daemon.
- A common cross-platform C library implements the socket protocol so both
  macOS and Linux can share it.
- Each sandbox invocation gets its own logical daemon: **one session per daemon,
  multiple concurrent sandboxes = multiple, disjoint daemons**.

### Architecture changes

1. **One session per daemon**
   - Remove the long-lived, shared daemon model.
   - `sandbox/run.sh` starts a private `sandboxd` for each invocation, waits for
     it to become ready, runs the target as a child process, then tears the
     daemon down when the target exits.
   - Because each daemon owns exactly one session, there is no session ID, no
     session map, and no per-session proxy URL to communicate through env vars.
   - The control socket is unique per invocation (e.g. under the build directory
     or a temp path). The guest may read `SANDBOX_DAEMON_SOCKET`; the daemon
     treats everything it receives as untrusted data.

2. **Common C source: `sandbox/common/`**
   - `sandbox/common/sandboxd.h` — command strings / opcodes, version, constants.
   - `sandbox/common/socket.h(.c)` — Unix socket helpers including
     `sendmsg`/`recvmsg` with `SCM_RIGHTS`.
   - `sandbox/common/message.h(.c)` — framing helpers, encoding/decoding of
     `REGISTER`, `OPEN`, `CONNECT`, etc.
   - `sandbox/common/path.h(.c)` — guest-side path resolution (relative to cwd).
   - The common code must be POSIX-only: no Mach-O interposition, no Linux
     mount-namespace logic.
   - The macOS dylib and the Linux probe both compile against `sandbox/common/`.
   - Future Linux work (FUSE for filesystem hooks, network namespaces with an
     in-namespace agent) will use the same socket protocol.

3. **macOS dylib becomes multi-file**
   Move `sandbox/macos/sandbox.dylib.c` to `sandbox/macos/dylib/`:
   - `sandbox/macos/dylib/interpose.h` — Mach-O interposer-wide definitions.
   - `sandbox/macos/dylib/daemon.c` — connection to the daemon using
     `sandbox/common/socket.c`.
   - `sandbox/macos/dylib/fs.c` — `open()` hook.
   - `sandbox/macos/dylib/net.c` — `connect()` hook and fd-passing.
   - `sandbox/macos/dylib/exec.c` — `fork()` / `execve()` / `execv()` hooks.
   - `sandbox/macos/dylib/env.c` — `getenv()` hook.
   - `sandbox/macos/dylib/init.c` — constructor.
   - Update `sandbox/macos/sandbox_wrapper.sh` to compile all `.c` files.

4. **Socket broker protocol (`connect()` hook — Option A)**
   - The dylib's `connect(fd, addr, len)`:
     - If the destination is loopback (`127.0.0.1`, `::1`, `localhost`), call
       the real `connect()` and return.
     - Otherwise, send a `CONNECT pid family host port` request over the
       daemon socket.
     - The daemon replies over the same socket, passing a connected fd via
       `SCM_RIGHTS`.
     - On success, the dylib closes the original file descriptor, uses
       `dup2()` to install the brokered fd at the same descriptor number, then
       returns `0`. This preserves the descriptor number the caller expects.
     - On failure, set `errno` and return `-1`.
   - The daemon's `CONNECT` handler:
     - Parse the request fields.
     - Identify the actual peer PID using `LOCAL_PEERPID` (macOS) or equivalent
       Unix socket credentials; do **not** trust the `pid` field in the
       request.
     - Look up the peer process in the daemon's process map.
     - Use the upstream proxy resolution/dial code from Phase C (factored into
       a shared helper) to obtain a connected `net.Conn`.
     - Convert the Go connection to an OS file descriptor.
     - Send `OK\n` plus the fd via `SCM_RIGHTS`.
     - On denial or failure, send `DENIED reason\n` with no fd.
   - No loopback proxy server is exposed to the guest, and no proxy env vars are
     injected. Proxy decision logic is entirely inside the daemon.

### Implementation steps

- [ ] Create `sandbox/common/` with the header and implementation files listed
  above.
- [ ] Implement the daemon-side broker:
  - `CONNECT` command parsing, peer PID validation, and `SCM_RIGHTS` response.
  - Reuse the proxy resolution logic from `internal/proxypass` (refactor into a
    shared helper if needed).
  - Helper to extract a real fd from a Go `net.Conn` for `SCM_RIGHTS`.
- [ ] Switch the daemon to a single-session model:
  - Remove global shared-socket reuse in `sandbox/run.sh`.
  - Generate a unique socket path per invocation.
  - Start `sandboxd`, wait for ping, run the target as a child, wait, then kill
    the daemon.
- [ ] Rewrite the macOS dylib as a multi-file directory using `sandbox/common/`.
- [ ] Implement `connect()` fd-passing in `sandbox/macos/dylib/net.c`.
- [ ] Update `sandbox/macos/sandbox_wrapper.sh` to compile the common library
  plus all macOS dylib `.c` files.
- [ ] Update the Linux probe (`sandbox/linux/probe.c`) to use
  `sandbox/common/socket.c`/`message.c` for registration, keeping the door open
  for future FUSE / network-namespace support.
- [ ] Ensure the `getenv()` hook does **not** hide `SANDBOX_DAEMON_SOCKET`.
  Guest access to the socket is expected; validate on the daemon side.
- [ ] Add defenses against untrusted guest input on the Unix socket:
  - Bounded line reads.
  - Reject unknown/malformed commands.
  - Require peer-PID validation for `REGISTER`, `OPEN`, and `CONNECT`.
  - Cease processing if peer PID is not registered.
- [ ] Update documentation: `sandbox/daemon/README.md`, `sandbox/linux/README.md`,
  and add `sandbox/macos/dylib/README.md`.

### Testing

- [ ] Run two concurrent sandboxes and confirm each has a separate daemon
  process and a separate Unix socket.
- [ ] Confirm `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` are not set inside a
  sandboxed shell but outbound HTTPS still works when the host has an upstream
  proxy configured.
- [ ] Verify loopback connections still work; verify non-loopback `bind()` is
  denied.
- [ ] Verify TCC-protected directories and dotfiles remain blocked while shell
  configs remain readable.

### Files

- New: `sandbox/common/*.h`, `sandbox/common/*.c`, `sandbox/macos/dylib/*.c`,
  `sandbox/macos/dylib/*.h`
- `sandbox/daemon/main.go`
- `sandbox/run.sh`
- `sandbox/macos/sandbox_wrapper.sh`
- `sandbox/linux/probe.c`
- Daemon and platform READMEs

---

## General notes

- `interpose/policy/tcc` is the source of truth for TCC-sensitive paths; any new
  protected directory should be added there so git wrappers, `gitall`, etc. stay
  consistent with the sandbox.
- The daemon runs on the host and therefore can safely read the host's proxy
  environment. The sandboxed guest cannot, because the dylib never forwards proxy
  env vars and the common protocol hides proxy decision logic behind the
  `CONNECT` command.
- Because the guest may be malicious and can talk to the daemon socket, every
  daemon command must validate peer identity and treat the payload as untrusted.
- Future Linux expansion will reuse `sandbox/common/` for in-namespace agents
  that talk to the daemon over the same Unix socket, enabling FUSE filesystem
  hooks and network-namespace proxying without duplicating the wire protocol.
# Sandbox Script Project Plan (macOS & Linux)

## 1. Overview
This project aims to create a cross-platform sandbox wrapper for binaries on macOS and Linux. The goal is to restrict filesystem access based on a set of allow/deny rules and provide a centralized management daemon to encapsulate and terminate sandboxed processes.

### Core Objectives
- **Linux**: Implement lightweight isolation using User Namespaces (`unshare`).
- **macOS**: Implement a more invasive "binary rewriting" approach using dylib injection/redirection and ad-hoc signing.
- **Access Control**: Define directory and file access rules with specific defaults for TCC-protected paths and dotfiles.
- **Lifecycle Management**: A daemon process to track and terminate sandboxed binaries.

---

## 2. Architecture

### 2.1 Linux Implementation (User Namespaces)
The Linux variant will leverage the kernel's namespace capabilities to create a restricted environment.
- **Namespace Setup**: Use `unshare(CLONE_NEWUSER | CLONE_NEWNS | CLONE_NEWUTS | CLONE_NEWPID)`.
- **Mounting**: Use `mount --bind` or `pivot_root` (if available) to create a restricted view of the filesystem.
- **Rule Engine**: 
    - Iterate through the allow-list and bind-mount directories/files into the namespace.
    - Use a "white-list" approach for the root filesystem.

### 2.2 macOS Implementation (Dylib Redirection)
Since macOS lacks a simple user-namespace equivalent for filesystem isolation, this project will use a binary modification approach.
- **Symbol Redirection**: 
    - Analyze the target binary for critical system calls (e.g., `open`, `read`, `write`, `unlink` in `libsystem_kernel.dylib`).
    - Rewrite the binary's load commands to inject a custom `sandbox.dylib`.
    - Move the original symbols to a shadow dylib or intercept them via the custom dylib.
- **Sandbox Logic**: The `sandbox.dylib` will hook into the filesystem calls and check against the rule engine before allowing the call to proceed.
- **Pipeline**:
    - **Analysis**: Inspect binary symbols.
    - **Rewrite**: Modify the Mach-O binary.
    - **Cache**: Store the rewritten binary in `~/.cache/sandbox/` to avoid repeated processing.
    - **Signing**: Apply `codesign --force --sign -` to the rewritten binary to allow execution.

### 2.3 The Sandbox Daemon
A background process will manage the lifecycle of all sandboxed instances.
- **Encapsulation**: The daemon will launch the sandboxed binary as a child process.
- **Tracking**: Maintain a map of `SandboxID -> PID`.
- **Termination**: Provide a CLI interface to trigger `kill -9` on specific sandboxes or all active sandboxes.
- **Communication**: Use a Unix Domain Socket for the wrapper script to communicate with the daemon.

---

## 3. Access Control Rules

### 3.1 Directory Rules
- **Explicit Allow/Deny**: User-defined lists of paths.
- **TCC Protection**: Automatically deny access to `~/Documents`, `~/Desktop`, `~/Downloads`, `~/Library/Messages`, etc., unless explicitly allowed.

### 3.2 File Rules
- **Dotfiles**:
    - Default: Deny all dotfiles (e.g., `.ssh/config`, `.bash_history`).
    - Exception: Git-related dotfiles (e.g., `.gitconfig`) are denied by default.
    - Shell Configs: `.zshrc`, `.bashrc`, `.profile` are allowed as **Read-Only**.

---

## 4. Implementation Roadmap

### Phase 1: Foundation & Daemon
- [ ] Implement the Sandbox Daemon (PID tracking, Socket API).
- [ ] Create the common CLI wrapper for starting/stopping sandboxes.

### Phase 2: Linux Sandbox
- [ ] Implement `unshare` logic for namespaces.
- [ ] Implement bind-mount logic for directory/file rules.
- [ ] Integrate with the Daemon.

### Phase 3: macOS Sandbox (The "Complex" Part)
- [ ] Develop the `sandbox.dylib` with symbol interception logic.
- [ ] Implement the Mach-O binary rewriter.
- [ ] Implement the caching and ad-hoc signing mechanism.
- [ ] Integrate with the Daemon.

### Phase 4: Validation & Testing
- [ ] Test TCC restriction on macOS.
- [ ] Test dotfile isolation on both platforms.
- [ ] Verify daemon-led termination of "runaway" sandboxed processes.

---

## 5. Technical Constraints & Risks
- **macOS SIP**: System Integrity Protection may interfere with binary rewriting of system binaries; the tool will target user-space binaries.
- **Kernel Versions**: Linux user namespaces require specific kernel config (usually enabled in modern distros).
- **Performance**: Mach-O rewriting and dylib interception add overhead to startup and syscalls.
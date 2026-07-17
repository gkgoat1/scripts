# Linux filesystem sandbox

This launcher builds a per-invocation root using a mount namespace, then executes the command inside it. It is intentionally **allow-list based**: no host directory is visible unless it is needed for the executable/runtime or was explicitly allowed.

## Usage

```bash
sandbox/run.sh [sandbox options] -- command [arguments...]
```

Options:

- `--allow PATH` — expose a path read/write. Repeatable.
- `--allow-ro PATH` — expose a path read-only. Repeatable.
- `--deny PATH` — fail if the path is requested via `--allow` or `--allow-ro`. Repeatable.
- `--workdir PATH` — set the sandbox working directory (must be allowed). Defaults to a temporary empty directory.
- `--net` — retain the host network namespace. The default is an isolated network namespace.

The launcher automatically exposes the executable and its dynamic-library dependencies read-only, plus the minimal virtual filesystems `/proc`, `/dev`, `/tmp`, and `/run` within the private mount namespace. Runtime loaders may inspect standard system paths, but the sandbox uses a private root and does not bind the host root tree.

### Defaults

- TCC-style sensitive locations (`Desktop`, `Documents`, `Downloads`, `Library/Messages`, `Library/Mail`, `Library/Safari`) are always denied unless the policy is changed in the source.
- Home-directory dotfiles are denied by default.
- Shell startup files (`.bashrc`, `.bash_profile`, `.profile`, `.zshrc`, `.zprofile`, `.zlogin`) may only be exposed with `--allow-ro`; writes are prevented by a read-only bind mount.
- `.git` metadata and other Git dotfiles remain denied by default.

A process must pass `--allow` / `--allow-ro` for any project or data it needs. For example:

```bash
sandbox/run.sh --allow-ro "$PWD" --workdir "$PWD" -- git status
sandbox/run.sh --allow "$PWD/output" --allow-ro "$PWD/input" -- my-program
```

## Requirements and limitations

For Linux-specific feature gaps and deferred capabilities (including the fact
that sandbox-level automatic interposition is not supported on Linux), see
[`../linux-known-gaps.md`](../linux-known-gaps.md). This launcher rejects a
sandbox configuration that requests `autoInterpose`; it does not substitute a
PATH wrapper or another partial implementation.

- Requires Linux user namespaces and mount namespaces. Some distributions disable unprivileged user namespaces; enable the relevant distribution policy or run through an administrator-approved sandbox facility.
- Requires `unshare`, `mount`, `chroot`, `python3`, and `/proc`.
- This is filesystem isolation, not a complete security boundary for hostile code. The launcher disables setuid privilege escalation with `no_new_privs`, creates new user/PID/UTS/IPC namespaces, and isolates networking by default, but applications with access to an explicitly allowed host resource can still affect it.
- Bind mounts protect path traversal only when all required host paths are selected deliberately. Do not allow a broad parent such as `$HOME` if its child data must remain private.
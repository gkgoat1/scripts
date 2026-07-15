# sandboxd

## Environment-variable redaction (macOS)

Sensitive variables can be allow-listed by the SHA-256 of the executable that
is receiving them. Configure comma-separated rules through
`SANDBOX_ENV_ALLOW`:

```bash
SANDBOX_ENV_ALLOW='OPENAI_API_KEY=HASH,ANTHROPIC_API_KEY=HASH' sandbox/run.sh agent
```

A rule may contain comma-separated hashes for one variable:

```text
OPENAI_API_KEY=HASH1,HASH2
```

When a sandboxed process calls `execve`, the embedded sandbox library asks the
daemon whether the destination executable's exact bytes match an allowed hash
for each environment variable. Every configured variable is retained only for
matching binaries; otherwise its value is replaced with the empty string.
The default is deny, and daemon communication failure is fail-closed.

The hash is the lowercase SHA-256 digest of the executable file, before
rewriting. This lets policy identify the intended original binary rather than a
cache artifact. The daemon owns the comparison, so the process cannot change
the decision locally.

This mechanism protects variables across child-process exec boundaries. It does
not protect a secret already present in a process's memory, command-line
arguments, inherited file descriptors, or IPC channels. Keep the policy narrow
and avoid granting an interpreter or shell hash.

The daemon also always enables the hardened runtime for rewritten macOS
binaries. Original JIT and unsigned-executable-memory entitlements are copied
only after strict signature verification. `get-task-allow` additionally
requires `--allow-get-task-allow` (or `SANDBOX_ALLOW_GET_TASK_ALLOW=1`).
# sandboxd

The macOS daemon keeps a per-process Unix-socket connection open. The embedded
library connects during process startup, registers its PID, and reuses that
connection for `ENV` and `OPEN` requests. After `fork`, the child closes the
inherited descriptor and opens a new `CLOEXEC` connection with its parent PID.

## Environment policy

`SANDBOX_ENV_ALLOW` contains `VARIABLE=SHA256[,SHA256]` rules. `getenv()` is
enforced at lookup time, rather than only during `execve`: unauthorized values
are returned as an empty string. This means an authorized nested agent can
continue to use a key while an intermediate spawner cannot read it. A daemon
failure is fail-closed for configured variables.

## File access policy

`OPEN pid path flags` is the daemon's central file-access decision point. The
daemon imports `interpose/policy/tcc` for TCC-sensitive paths and augments it
with a default dotfile deny list:

- Access to TCC-protected directories (`Documents`, `Desktop`, `Downloads`,
  `Library`, `Pictures`, `Movies`, `Music`, and any configured
  `extra-protected-path`) is denied.
- All dotfiles are denied except shell startup files (`.zshrc`, `.bashrc`,
  `.profile`, `.bash_profile`, `.zprofile`, `.zlogin`), which are allowed
  read-only.

Response codes:

- `DENIED` — the open/exec is blocked.
- `RO` — the path is allowed read-only; if `flags` contain a write mode,
  the request becomes `DENIED`.
- `ALLOWED` — the path is allowed; no interpreter hash update occurred.
- `UPDATED` — the path is allowed and the process identity hash was updated
  (interpreter/code hash update policy).

## Interpreter/code hash updates

```bash
SANDBOX_HASH_UPDATERS='INTERPRETER_HASH=.py,.js,.wasm' sandbox/run.sh python
```

The daemon accepts `--hash-updater BINARY_SHA256=EXT[,EXT]`. An `OPEN pid path`
request updates that process's effective hash only if the current hash is an
authorized interpreter hash and the opened path has an allowed extension. The
new hash is the SHA-256 of the opened file. This makes environment access
follow the interpreted code rather than the interpreter alone.

The daemon does not trust the path supplied by the process for authorization:
it hashes the file itself. Policies should use narrow extensions and exact
interpreter hashes. `SOCK_CLOEXEC` is used for the connection; the daemon also
tracks `REGISTER`, `FORK`, `ENV`, and `OPEN` requests per PID.

Rewritten macOS binaries continue to receive hardened runtime signing. Valid
original JIT and unsigned-executable-memory entitlements are preserved;
`get-task-allow` additionally requires the explicit daemon flag.
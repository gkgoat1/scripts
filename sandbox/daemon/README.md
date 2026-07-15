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

## Interpreter/code hash updates

Interpreters can be authorized to change the process identity hash when they
open a script or bytecode file:

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
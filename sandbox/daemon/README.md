# sandboxd

The macOS daemon keeps a per-process Unix-socket connection open. The embedded
library connects during process startup, registers its PID, and reuses that
connection for `ENV` and `OPEN` requests. After `fork`, the child closes the
inherited descriptor and opens a new `CLOEXEC` connection with its parent PID.

## Sandbox hash-map policy

Sandbox authorization is configured in the selected logical home's
`Library/Application Support/sandbox/config.json`, not with
`SANDBOX_ENV_ALLOW` or `SANDBOX_HASH_UPDATERS` (both are rejected by the
launcher). Start a sandbox for a fixture or alternate home with:

```bash
sandbox/run.sh --home /absolute/test-home command
```

All state is derived from that `--home`: configuration, proof, and hash-map log
are under its `Library/Application Support/sandbox`; a verified committed run
uses its `Library/Caches/sandbox`; and an uncommitted test run uses a private
subdirectory of its `tmp/sandbox`. This permits isolated tests without touching
the real user's `Library` or requiring its commitment anchor.

A process identity is a digest of sorted canonical JSON mapping absolute,
symlink-resolved paths to **full-file SHA-256 hashes**. The main executable and
every subsequently accepted source/code file remain in the map. An update is
accepted only when its resulting complete map digest appears in a configured
transition rule; an extension alone never authorizes a change. The same format
is used on macOS and Linux.

The map log at `Library/Application Support/sandbox/hash-map-log.json` records
`map digest -> map` for operator inspection. It is descriptive only: the daemon
recomputes every candidate map digest and authorization comes only from config
(and, for cache use, a valid Merkle proof/anchor).

`cdhash` is not a code-map file hash. It is used only to pin the exact staged
shim in the macOS hardened-runtime library-load constraint, even when a real
signing identity is available.

## Legacy environment policy

The following historic interface is no longer accepted:

```bash
SANDBOX_HASH_UPDATERS='...' SANDBOX_ENV_ALLOW='...' sandbox/run.sh ...
```

Migrate it to committed sandbox configuration that lists initial/result map
digests and environment grants.

## Previous environment policy (historical behavior)

`SANDBOX_ENV_ALLOW` was the historic environment-policy format. It is retained
below only to describe old invocations; new launchers reject it. `getenv()` is
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

### Policy commitment verification

`ExtraProtectedPaths` and `DisableSnapshot` (from `~/.config/interpose/config`,
shared with `interpose`) are silently-editable config: anyone who can write
that file can narrow the deny-list before the daemon starts. At startup,
`main()` calls `verifyPolicy` (see `policy_verify.go`) to check the config's
current policy leaf against a Merkle root anchored in the
`agentcommit-anchor` LaunchAgent's plist (`commitment`/`commitment/anchor`;
see `docs/agentcommit.md` for the full design). If the anchor was never
installed, behavior is unchanged (live, unverified config, same as before
this existed). If it's installed but the config's commitment doesn't verify
— tampered, or never (re)committed — `s.protectedRoots` falls back to the
fixed built-in roots only (`tcc.DefaultProtectedRoots()`), logging a
`[warn]` line; `ExtraProtectedPaths` is dropped, never trusted unverified.
This can never be more permissive than running with no config at all, so a
tampered/stale commitment degrades protection back to the no-config
baseline rather than failing the daemon closed entirely.

Response codes:

- `DENIED` — the open/exec is blocked.
- `RO` — the path is allowed read-only; if `flags` contain a write mode,
  the request becomes `DENIED`.
- `ALLOWED` — the path is allowed; no interpreter hash update occurred.
- `UPDATED` — the path is allowed and the process identity hash was updated
  (interpreter/code hash update policy).

## Previous interpreter/code hash updates (historical behavior)

The following describes the replaced environment-variable interface and is not
accepted by the current launcher.

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

## Hardened Runtime library validation

Library validation remains enabled: the daemon never adds
`com.apple.security.cs.disable-library-validation`, and it never uses
`DYLD_INSERT_LIBRARIES`. On macOS, the wrapper requires a real, non-ad-hoc
identity in `SANDBOX_CODESIGN_IDENTITY` (optionally
`SANDBOX_CODESIGN_KEYCHAIN`). The wrapper signs the shim with that identity;
the daemon signs each rewritten executable with the same identity and embeds a
library load constraint matching the shim's Team ID and signing identifier.

The rewritten executable and shim are staged together as `program` and `x`,
and the load command is `@executable_path/x`. This makes the accepted library
both identity-bound and path-scoped. A different signed library, an ad-hoc
library, an unsigned library, or an injected library from an unrelated process
fails Hardened Runtime validation. The identity must be a Developer ID,
Apple-development, enterprise, or equivalent identity accepted by the target
macOS; an arbitrary self-signed certificate is not a portable substitute.

Example:

```bash
SANDBOX_CODESIGN_IDENTITY='Developer ID Application: Example, Inc. (TEAMID1234)' \
SANDBOX_CODESIGN_KEYCHAIN="$HOME/Library/Keychains/login.keychain-db" \
sandbox/run.sh /path/to/thin-arm64-program
```

The current implementation intentionally fails closed if the identity is
missing, ad-hoc, or the shim has no Team ID. Existing cached rewrites are
versioned by the signing identity and must not be reused across identities.
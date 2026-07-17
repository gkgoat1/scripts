# Sandbox Hash-Identity and Commitment Hardening Plan

> **Status:** proposal only — no behavior changes are included in this document.
>
> **Scope:** harden macOS sandbox process identities, interpreter/code updates,
> configured authorization, rewrite-cache isolation, and hardened-runtime
> library pinning. A launch may select any logical home directory: its
> `Library` subtree supplies configuration/cache state and its correlated `tmp`
> subtree supplies transient state. This makes an ordinary user's real
> `~/Library` visible to the persistence monitor when an operator adopts a
> commitment, while an isolated test home can run without changing or adopting
> the host user's commitment.

## Objectives and security invariants

This work replaces the daemon's mutable, single-file `process.hash` identity
with an explicit code-set identity. It also moves security-sensitive sandbox
options out of ambient environment variables and makes them eligible for the
existing `agentcommit` Merkle commitment.

The completed design must maintain these invariants:

1. **Every process identity is a hash of a complete code map.** The initial
   executable and every code file accepted through `updateHash` are present in
   one canonical map. Replacing the current identity with just the most
   recently opened source file is prohibited.
2. **Map keys are canonical absolute paths.** A relative path, `~` spelling,
   symlink spelling, or working-directory difference must never produce a
   distinct authorization identity for the same resolved file. The daemon
   canonicalizes the path before policy evaluation, map insertion, and logging.
3. **A map digest is compared, not merely calculated.** An update is accepted
   only when its resulting map digest is explicitly authorized by committed
   configuration. Merely matching an extension is not authorization to change
   the identity.
4. **Hash-map identities bind exact file contents.** Every ordinary map entry,
   including interpreter inputs and all Linux entries, is a SHA-256 digest of
   the complete file bytes. A cdhash is not a substitute for this identity.
   The only cdhash-only decision is the macOS hardened-runtime library load
   constraint described below. The main executable may additionally carry a
   cdhash as signing metadata, but code-map authorization falls back to (and
   compares) its full-file hash unless the value is being used as that macOS
   load constraint.
5. **The hash-map log is descriptive, not an authority.** It records
   `mapDigest -> canonical hash map` so an operator can inspect and select a
   known digest. A forged or missing log entry can cause denial, but cannot
   grant an identity unless the recomputed digest matches and committed config
   authorizes it.
6. **Only a verified configuration may use the committed rewrite cache.** A
   sandbox with no verified commitment, a stale proof, or an unreadable anchor
   gets an isolated non-committed work/cache location and never reads or writes
   that logical home's `Library/Caches/sandbox`.

`file hash`, `cdhash`, and `map digest` are deliberately different terms in
this document:

- A **file hash** is a lower-case SHA-256 of the complete bytes of one regular
  file. It is the value in every process code map, including the main
  executable and on Linux.
- A **cdhash** is the SHA-256 CodeDirectory hash reported by `codesign` for a
  signed macOS executable. It is signing metadata for the main executable and
  the required exact pin for a macOS library-load constraint; it is never used
  to represent arbitrary opened files.
- A **map digest** is `SHA-256` over the versioned canonical JSON representation
  of the whole map. It is a commitment to an ordered set of `(absolute path,
  file hash)` entries and is the value policies compare and operators select.

The outer map digest is necessarily a normal digest; calling it a cdhash would
be incorrect because it is not a CodeDirectory hash.

## Current gaps

The implementation presently has the following weaker behavior:

- `sandbox/daemon/main.go` stores one `process.hash`. `register` computes a
  raw file SHA-256, and `updateHash` replaces it with the raw SHA-256 of the
  last allowed source file. Thus a prior source file disappears from the
  identity. It also has no explicit versioned map format or authorization of
  the resulting cumulative map.
- `SANDBOX_HASH_UPDATERS` and `SANDBOX_ENV_ALLOW` are parsed from environment
  variables in `sandbox/run.sh`; neither policy is a sandbox-specific,
  committed configuration object.
- `fileHash` consumes a pathname and does not establish an absolute canonical
  map key. The current line protocol also cannot represent paths containing
  whitespace safely.
- The macOS rewrite cache is selected through `$HOME/Library/Caches/sandbox`
  in the shell wrappers and `os.UserCacheDir()` in the daemon regardless of
  whether policy is committed.
- The hardened-runtime schema documents a cdhash constraint only for the
  ad-hoc fallback. For a real signing identity it currently documents only
  Team ID plus signing identifier, even though a real identity does not pin
  one exact shim build.

## Canonical hash-map format

Introduce a versioned in-memory and on-disk `HashMap` type. Its logical value
is a JSON object whose `files` member maps **canonical absolute paths** to
lower-case 64-hex-character full-file SHA-256 hashes:

```json
{"files":{"/Users/alice/project/main.py":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","/usr/bin/python3":"89abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},"version":1}
```

The byte representation to digest is canonical and specified, rather than an
incidental result of a Go map encoder:

- object keys are lexicographically sorted by UTF-8 byte value at every level;
- no insignificant whitespace is emitted;
- strings use standard JSON escaping with no HTML escaping;
- paths are canonical absolute paths after `filepath.Clean` and successful
  `EvalSymlinks`; nonexistent, unreadable, empty, or non-absolute results are
  rejected rather than retained as aliases;
- file hashes are lower case 64-hex SHA-256 values and validated before
  serialization;
- `version` is required and included in the bytes.

Compute the map digest as:

```text
SHA-256("sandbox-hash-map-v1\x00" || canonical-json-bytes)
```

A domain prefix prevents the same byte sequence from being reused as another
repository SHA-256 value. Implement explicit canonical serialization and test
its bytes; do not rely only on Go's current JSON map-order behavior.

At `REGISTER`, create a map with the canonical registered executable path and
its full-file hash. On an eligible code open, read the candidate's full-file
hash, insert or replace the entry for that canonical path, canonicalize/digest
the entire
resulting map, and compare that **resulting map digest** to policy before
committing it to the process record. Reopening an unchanged path leaves the
map digest unchanged. A changed file at the same canonical path replaces that
path's value; it does not leave a stale duplicate entry.

The process state should retain the map (or an immutable canonical map object)
and `mapDigest`, not only a string hash. `ENV`, `LIST`, audit output, and all
hash-updater decisions use `mapDigest`.

### Path and file-race handling

The daemon must normalize a path before inserting it in the map, including
resolving symlinks. It must reject a request if normalization fails. The wire
protocol should move from whitespace-separated fields to a bounded JSON
message/frame (or an equally unambiguous length-delimited protocol), so an
absolute path with spaces or escaping characters cannot be parsed differently
by the shim and daemon.

While implementing, evaluate the `OPEN` time-of-check/time-of-use boundary:
the bytes used for the full-file hash must be the same immutable file identity
that was authorized. At minimum, use descriptor-based inspection (`open` with
no-follow protections where applicable, `fstat` before/after hashing) and
reject an unstable file. If the guest must subsequently receive the file,
prefer passing the verified descriptor instead of resolving/opening the path a
second time. This is required before treating a source-file identity update as
security authority.

## Hash-map logging and operator selection

Add a deterministic log scoped to the selected logical home at:

```text
<logical-home>/Library/Application Support/sandbox/hash-map-log.json
```

The log has a versioned, sorted mapping from map digest to the full canonical
map. Its parent is taken from the selected logical home, rather than from an
ambient `HOME`; the same rule applies to every path in this plan. Example:

```json
{
  "entries": {
    "2ea…": {
      "files": {
        "/Users/alice/project/main.py": "0123456789abcdef0123456789abcdef01234567",
        "/usr/bin/python3": "89abcdef0123456789abcdef0123456789abcdef"
      },
      "version": 1
    }
  },
  "version": 1
}
```

The logging command/API will:

1. canonicalize supplied executable/code paths and obtain their complete-file
   SHA-256 hashes;
2. construct and digest the canonical map;
3. merge `mapDigest -> map` idempotently into the log;
4. refuse a conflicting existing entry for the same digest;
5. atomically write the `0600` file and print the digest for copy/paste into
   sandbox configuration.

The exact CLI spelling can be settled during implementation (for example,
`sandboxctl hash-map log …` and `sandboxctl hash-map show <digest>`), but it
must support both printing an existing mapping and logging a supplied mapping.
It must never add a digest to authorization by logging it.

The configuration contains explicit digest references. A representative shape
is:

```json
{
  "version": 1,
  "environmentAllow": {
    "API_TOKEN": ["2ea…"]
  },
  "hashUpdates": [
    {
      "from": "initial-python-map-digest",
      "extensions": [".py", ".js", ".wasm"],
      "allowResult": ["2ea…", "another-known-map-digest"]
    }
  ]
}
```

`from` matches the process's current complete map digest; `allowResult` lists
complete map digests that may result after the candidate path is incorporated.
This makes multi-file interpretation deliberate: log the expected cumulative
map, put its digest in `allowResult`, and recommit config. The daemon must
recompute the candidate map and compare the digest itself; it must not trust a
log lookup, a caller-provided digest, or a file extension alone. The final
schema may factor common extension sets differently, but must preserve this
comparison property and reject duplicate/conflicting rules.

## Configuration, paths, and commitment states

`--home DIR` is an explicit daemon/launcher option selecting a **logical
home**. It defaults to the current account's home for normal use, but it may
name any absolute directory owned by the invoking user. This is intentional:
CI and local tests can use a fresh fixture home without reading, writing, or
requiring adoption of the real user's anchor. Resolve this option once before
launch; reject relative paths, symlink escapes, non-owned directories, and
insecure directory modes. Never derive this root from an untrusted inherited
`HOME` alone.

Create a sandbox configuration package with strict JSON parsing and one
well-known logical-home-scoped layout. Derive all paths from the selected home
plus literal `Library` and `tmp` components:

```text
logicalHome      = canonical value of --home
libraryRoot      = <logicalHome>/Library
tmpRoot          = <logicalHome>/tmp
configDir        = <logicalHome>/Library/Application Support/sandbox
configPath       = <logicalHome>/Library/Application Support/sandbox/config.json
proofPath        = <logicalHome>/Library/Application Support/sandbox/config.json.proof
hashMapLogPath   = <logicalHome>/Library/Application Support/sandbox/hash-map-log.json
anchorPlistPath  = <logicalHome>/Library/LaunchAgents/com.gkgoat.scripts.agentcommit-anchor.plist
committedCacheDir = <logicalHome>/Library/Caches/sandbox
transientRoot    = <logicalHome>/tmp/sandbox
```

`Application Support/sandbox`, `Caches/sandbox`, and `tmp/sandbox` are
correlated children of the same selected logical home. Compute them through
one helper, not from unrelated `$HOME`, `TMPDIR`, `os.UserCacheDir`, and shell
expansions. For a normal real home, legitimate commitment updates write under
its monitored `Library`; for a test home, the same layout is isolated and does
not require the system's anchor. Create security-sensitive directories with
`0700`; validate ownership and reject symlinks or insecure modes before
trusting contents.

The sandbox anchor reader must be parameterized with `anchorPlistPath`; it
must not use only `anchor.PlistPath()` for the invoking account. Similarly,
`agentcommit` and the anchor installer gain an explicit `--home`/sandbox-home
option that writes the sandbox proof and (when requested) the anchor under the
same logical home. An actual user's selected `Library/LaunchAgents` write is
therefore visible to the existing persistence monitor; a fixture home's absent
anchor is intentionally the no-adoption test mode.

Register sandbox configuration as an `agentcommit` leaf (a new sandbox policy
leaf, not a reuse of the interpose leaf). Its canonical payload covers every
field that can change sandbox authority or artifact identity, including:

- environment-variable to authorized-map-digest rules;
- hash-update transition rules and allowed extensions;
- signing/cdhash pinning policy and entitlement options;
- rewrite/shim version and any cache-affecting security mode;
- explicitly configured paths or allow lists, after canonicalization.

Cosmetic display fields may be excluded only with a written rationale. The
config's proof sidecar is written alongside `config.json`, and `agentcommit
commit` gathers it with pulse and interpose leaves. Update the installer,
`agentcommit` CLI help, `docs/agentcommit.md`, and tests to describe the new
registrant.

At daemon startup, classify sandbox configuration as follows:

| State | Conditions | Policy/cache behavior |
|---|---|---|
| **committed** | Anchor exists for the selected logical home, config parses, proof exists, and its sandbox leaf verifies against that anchor root | Load the committed sandbox policy and use only `committedCacheDir`, namespaced by the verified config leaf/map digest and rewrite version. |
| **not adopted** | The selected logical home has no anchor | Run the conservative non-committed baseline. It may use that home's live config for testing, but it must visibly report that it is uncommitted, may not grant any authorization not present in the parsed config, and must not use `Library/Caches/sandbox`. |
| **unverified/invalid** | Anchor/proof/config read or verification fails | Log a specific warning, deny config-dependent hash/environment authorization, and use the same isolated non-committed baseline; never read or write the committed cache. |

The non-committed baseline receives a unique private transient/state directory
under `transientRoot` (for example a `0700` `mkdtemp` directory), deleted on
normal exit where practical. Thus the temporary location is correlated with
`--home`, rather than the host system's `/tmp` or `$TMPDIR`. It has no access
to an existing committed rewrite or shim cache. This avoids an uncommitted run
poisoning, replacing, or merely reusing artifacts from a committed run. The
launch scripts pass the resolved logical-home layout and state to the daemon
rather than each independently choosing a cache directory.

This is intentionally stricter than the current interpose-policy fallback:
sandbox configuration can authorize credentials and code identities, so it
must not remain live and unverified when adoption has not occurred.

## Full-file identity and macOS library-validation cdhash changes

### Individual files and code maps

Keep `fileHash` (renamed only if helpful for clarity) as a complete-file
SHA-256 reader. Use it for every code-map entry: the main executable, every
opened interpreter/module/data-code file, and every supported Linux file. It
must hash the complete descriptor contents, validate stability with `fstat`
before/after reading, and fail closed on an unreadable, non-regular, changing,
or otherwise untrustworthy file. There is no cdhash requirement for an
arbitrary file and no platform-specific reduction of the map's full-file
coverage.

On macOS, obtain the main executable's cdhash separately when it is useful as
signing provenance or to create the hardened-runtime library constraint. Its
availability must never replace the main executable's full-file map hash.
This is a deliberate fallback rule: **outside a macOS library-load constraint,
always use a complete-file SHA-256 hash, not a cdhash.** Linux follows the same
full-file identity format and can therefore use the same committed hash-map,
transition, and environment-authorization configuration as macOS.

The old environment-based updater interface is removed in favor of committed
configuration, but this is a policy-source improvement rather than a change
from full-file hashes to cdhashes.

### Rewritten executable and shim

For both real-identity and ad-hoc macOS modes:

1. obtain and validate the staged shim's SHA-256 cdhash after its final
   signature is in place;
2. include that cdhash in the rewrite cache key and in the committed policy
   identity inputs;
3. embed an exact cdhash library constraint for the staged `x` shim;
4. verify the staged `program` and `x` signatures, expected cdhashes,
   `@executable_path/x` relationship, ownership, and modes before a cache hit
   is returned.

Here, and only here, cdhash is a required security comparison: it is the
macOS library-load constraint that pins the exact staged shim. The rewritten
program's ordinary process map entry remains its complete-file SHA-256 hash.

When a real signing identity is used, retain Hardened Runtime library
validation and retain Team ID/signing identifier constraints as additional
provenance checks where the platform permits conjunction. The exact shim
cdhash is still mandatory: a signing key's availability is not a reason to
relax it to “any artifact signed by this identity.” When ad-hoc mode is
explicitly permitted, its existing disable-library-validation caveat remains,
but the exact cdhash pin is still mandatory.

The implementation phase must validate macOS library-constraint semantics on
the supported OS versions: prove that the Team-ID/signing-ID and cdhash
requirements are conjunctive, not alternative, and that the real-identity
case loads only the pinned shim. If the API cannot express the conjunction,
choose a tested construction that preserves both ordinary library validation
and an exact cdhash check; do not document an untested plist as a security
property.

Update `sandbox/hardened-runtime-library-validation-schema.md` so cdhash
pinning is described as mandatory for **all** signing modes, not an ad-hoc
fallback detail. Update `sandbox/daemon/README.md` to replace raw-SHA updater
examples with config/log/map-digest examples and to document the committed vs.
non-committed cache boundary.

## Implementation sequence

1. **Specify and test primitives.** Add `sandbox/config` and a hash-map
   package with strict schema validation, canonical JSON encoder, digest,
   absolute-path canonicalizer, complete-file SHA-256 reader, optional macOS
   main-executable cdhash reader, and table-driven tests. Include malformed
   JSON, duplicate rules, symlink aliases, relative paths, whitespace paths,
   digest case, and canonical-byte golden tests.
2. **Implement the log and operator tooling.** Add the deterministic mapping
   log, atomic secure writes, `log`/`show` commands, and tests that tampering,
   deletion, conflicting entries, or an incorrect map value cannot authorize a
   map digest. Document the operator flow: log map, add digest transition/
   environment reference to config, then commit.
3. **Integrate `agentcommit`.** Define `sandbox.Config.CommitLeaf()`, gather
   its leaf in `agentcommit/commit.go`, write the proof beside sandbox config,
   extend install/CLI flags, and add verification logic with the state table's
   fail-safe behavior. Test trees containing pulse, interpose, and sandbox
   leaves and test adopted/not-adopted/stale/tampered cases.
4. **Centralize logical-home paths and cache selection.** Add `--home` to the
   launcher/daemon, then replace direct `$HOME` and `os.UserCacheDir` use in
   `sandbox/run.sh`, `sandbox/macos/sandbox_wrapper.sh`, and
   `sandbox/daemon/main.go` with one resolved `Library`/`tmp` layout. Implement
   committed-cache namespacing, secure ownership/mode checks, and ephemeral
   logical-home `tmp` directories. Migrate old cache entries only by rebuilding
   them; never trust them as a cache hit.
5. **Replace process identity/update logic.** Change `process`, `register`,
   `envAllowed`, `addHashUpdater`/its config replacement, `updateHash`, and
   `LIST` to use hash maps and map digests. Replace the ambient updater/env
   variables with committed config. Upgrade the daemon protocol so canonical
   absolute paths are transported unambiguously and implement descriptor/race
   protections.
6. **Harden signing and cache validation.** Refactor `signingInfo`,
   `signHardened`, `rewrite`, and cache-key construction to mandate exact
   cdhash pinning in every mode. Add macOS integration tests for real identity
   and explicit ad-hoc modes, including shim replacement and cache tampering.
7. **Remove legacy paths and update docs.** Delete or reject
   `SANDBOX_HASH_UPDATERS` and `SANDBOX_ENV_ALLOW`, update wrapper usage,
   revise both sandbox documents and `docs/agentcommit.md`, and provide a
   concise migration guide. Do not leave an undocumented compatibility mode
   that uses raw hashes or the committed cache without proof verification.

## Required validation

Automated tests and macOS integration tests must demonstrate at least:

- the same inputs in different registration/order/open order yield the same
  canonical map and digest;
- changing, removing, or adding any original/updated path changes the map
  digest; a one-file replacement cannot discard earlier code entries;
- relative, symlinked, and alternate spellings resolve to one absolute key;
  a missing or unstable path is denied;
- a candidate source with an allowed extension is denied unless the **resulting
  complete map digest** is in the committed transition rule; full-file hashes
  must be used for each entry on both macOS and Linux;
- an authorized map grants an environment value, while its one-file-hash-
  mutated sibling does not;
- hash-map log corruption cannot grant an unconfigured identity;
- an absent anchor in an isolated test home can run against that home's live
  config and correlated `tmp`, but reports its uncommitted status and cannot
  access that home's `Library/Caches/sandbox`; a missing anchor, bad anchor,
  bad proof, changed config, or changed proof in an adopted real home cannot
  access `~/Library/Caches/sandbox`;
- a valid committed configuration can use only its own namespaced cache and a
  changed committed config/rewrite/shim cdhash cannot reuse an old artifact;
- real-identity and ad-hoc paths both pin the exact staged shim cdhash;
  replacing `x` with a different artifact signed by the same real identity is
  rejected in addition to unsigned/different-team/ad-hoc replacements;
- no legacy ambient environment policy is accepted, and no arbitrary file
  identity is reduced from a full-file hash to a cdhash.

## Files expected to change

- New: `sandbox/config/...`, hash-map/cdhash/log package(s), their unit tests,
  and likely sandbox daemon integration tests.
- `sandbox/daemon/main.go` — process identity, update authorization, cache
  choice, signing/cache verification, and protocol handling.
- `sandbox/run.sh`, `sandbox/macos/sandbox_wrapper.sh`, and macOS shim protocol
  files — resolved layout/config handoff and unambiguous request framing.
- `agentcommit/commit.go`, `agentcommit/main.go`,
  `install-agentcommit-anchor.sh`, and their tests — sandbox leaf registration
  and proof creation.
- `sandbox/daemon/README.md`,
  `sandbox/hardened-runtime-library-validation-schema.md`, and
  `docs/agentcommit.md` — configuration, map logging, commitment state, and
  cdhash requirements specifically for macOS library-load constraints.

## Operator migration flow

1. Build/sign the intended main executable and shim when using macOS hardened
   runtime; all code-map entries, signed or not, are recorded by their
   complete-file hashes.
2. Use the new hash-map logging command to record the initial and expected
   cumulative code maps; inspect the printed mappings/digests.
3. Put only the desired map-digest transitions and environment grants in
   `~/Library/Application Support/sandbox/config.json`.
4. Run `install-agentcommit-anchor.sh` (or the updated explicit commit command)
   to write the proof and update the monitored anchor root.
5. Start the sandbox with the corresponding `--home`. Only a verified
   configuration may use that logical home's
   `Library/Caches/sandbox`.

Changing code, a permitted path set, an environment grant, the shim cdhash, or
signing/entitlement policy requires repeating steps 2–4. This is deliberate:
it makes a new authorization an explicit, Merkle-committed operator action
instead of an implicit consequence of a file open.
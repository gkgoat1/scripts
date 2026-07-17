# macOS Hardened Runtime Library-Validation Schema

## Purpose

This document describes the design used to let a sandboxed executable load its
sandbox interposition dylib while retaining Hardened Runtime library
validation. It is intended as a portable reference for other projects.

## Security goal

Permit only the project-controlled shim, signed by the same approved signing
identity as the rewritten executable. Reject:

- ad-hoc or unsigned replacement dylibs;
- dylibs signed by another Team ID;
- dylibs with another signing identifier;
- arbitrary libraries injected by an unrelated process;
- `DYLD_INSERT_LIBRARIES` and related environment-based injection paths.

The preferred design does **not** use `com.apple.security.cs.disable-library-validation`
and does **not** use `DYLD_INSERT_LIBRARIES`. An explicitly selected ad-hoc
fallback is described below; it necessarily uses the entitlement because
macOS rejects ad-hoc libraries under ordinary library validation.

## Trust model

The signing identity is a deployment trust anchor. It must be a real identity
accepted by the target macOS installation, typically Developer ID Application,
Apple development, enterprise, or an equivalent organization-controlled
identity. Ad-hoc signatures have no Team ID and cannot satisfy this design.
A self-signed certificate may be useful for controlled experiments, but is not
a portable production identity and may be rejected by macOS policy.

The signing key must be protected. If an attacker can sign with the same
identity, identity-based library validation cannot distinguish that attacker.

## Artifacts and signatures

For every target program, create a private staging directory containing:

```text
<cache>/<rewrite-key>/program   # rewritten and signed executable
<cache>/<rewrite-key>/x         # copied, signed sandbox dylib
```

The executable has a load command:

```text
@executable_path/x
```

The shim and executable are both signed with the same identity. The daemon
extracts the shim's `TeamIdentifier` and `Identifier`, then signs the program
with a library load constraint equivalent to:

```xml
<plist version="1.0">
<dict>
  <key>team-identifier</key>
  <string>TEAMID1234</string>
  <key>signing-identifier</key>
  <string>com.example.sandbox-shim</string>
</dict>
</plist>
```

The cdhash constraint is required in **every** signing mode. With a real
identity, retain Team ID and signing identifier checks as additional provenance
constraints where the platform supports their conjunction; the signing key is
not a substitute for pinning the exact staged shim. Outside this macOS
library-load constraint, sandbox code maps use complete-file SHA-256 hashes
rather than cdhashes.

The exact identity and Team ID are deployment-specific. The constraint is
embedded using:

```bash
codesign --options runtime \
  --library-constraint library-constraint.plist \
  --entitlements entitlements.plist \
  --sign "$SANDBOX_CODESIGN_IDENTITY" program
```

The shim is signed separately with the same identity. `--keychain` can pin
identity lookup to a known keychain.

## Why both identity and path are used

The load constraint is the security boundary; `@executable_path/x` is a
locality and operational boundary. The path prevents the rewrite from naming
a mutable repository or user-controlled absolute path. The Team ID and
signing identifier prevent a same-path replacement from loading unless it is
signed as the approved project shim.

Do not rely on the path alone. Do not rely on an ad-hoc signature. Do not copy
a signed shim after signing the executable unless the copied file remains
byte-for-byte identical and its signature remains valid.

## Ad-hoc fallback

Ad-hoc signatures have no Team ID, so ordinary Hardened Runtime library
validation cannot accept an ad-hoc dylib. There is no `codesign` flag that
changes this: a Team ID constraint cannot match `not set`, and a cdhash
constraint alone is still rejected by the normal non-platform Team-ID rule.

For local, isolated development only, select `SANDBOX_CODESIGN_IDENTITY=-`.
The implementation then:

1. signs both artifacts ad hoc;
2. embeds a library constraint containing the shim's exact SHA-256 code
   directory hash (`cdhash` as binary plist data);
3. enables `com.apple.security.cs.disable-library-validation` only on the
   rewritten sandbox executable;
4. keeps the relative `@executable_path/x` staging layout;
5. removes/rejects `DYLD_*` injection variables and uses private `0700`
   staging directories.

The cdhash constraint prevents a changed or substituted dylib from loading
through the rewritten executable, which is substantially safer than a bare
`disable-library-validation` entitlement. It is not equivalent to library
validation: an attacker able to execute in the process context may still use
other permitted code-loading mechanisms, and the entitlement disables the
kernel's Team-ID gate for that process. Never use this fallback for distributed
or hostile multi-user workloads. Prefer a real identity whenever available.

## Environment injection policy

The launcher must not set or forward library-injection variables. At minimum,
construct the child environment with these names removed or rejected:

```text
DYLD_INSERT_LIBRARIES
DYLD_LIBRARY_PATH
DYLD_FRAMEWORK_PATH
DYLD_FALLBACK_LIBRARY_PATH
DYLD_INSERT_LIBRARIES
DYLD_SHARED_REGION
DYLD_FORCE_FLAT_NAMESPACE
DYLD_PRINT_LIBRARIES
DYLD_PRINT_LIBRARIES_POST_LAUNCH
DYLD_PRINT_APIS
DYLD_PRINT_BINDINGS
DYLD_PRINT_SEGMENTS
DYLD_PRINT_STATISTICS
DYLD_VERSIONED_LIBRARY_PATH
DYLD_VERSIONED_FRAMEWORK_PATH
```

This is defense in depth, not a replacement for code signing. macOS may strip
some `DYLD_*` variables for protected processes, but a launcher should still
remove them explicitly and reject attempts to reintroduce them through its
own configuration. The sandbox socket variable is intentionally separate and
must be treated as untrusted input; its protocol validates peer credentials
and does not interpret guest-supplied PIDs as authority.

## Signing and cache invariants

The rewrite cache key must include:

- original executable bytes;
- shim bytes;
- signing identity or Team ID/signing identifier;
- entitlement policy;
- rewrite format version.

Never return an old cached executable without checking that its staged shim,
signature, and library constraint still match the active identity. A practical
implementation can use a new rewrite-version prefix whenever the Mach-O
rewriter or signing policy changes.

## Validation checklist

For a deployment identity:

```bash
codesign -dv --verbose=4 shim 2>&1 | egrep 'Identifier|TeamIdentifier'
codesign -d --entitlements :- program
codesign --verify --strict --verbose=4 program
codesign --verify --strict --verbose=4 shim
```

Expected properties:

- both artifacts report the same non-`not set` Team ID;
- the shim identifier equals the identifier in the library constraint;
- the program has Hardened Runtime (`flags=...runtime`);
- the program does not contain `com.apple.security.cs.disable-library-validation`;
- the program's load command names `@executable_path/x`;
- replacing `x` with an ad-hoc, unsigned, different-Team, or different-ID
  library fails at load time;
- an ordinary unrelated signed program cannot load the shim unless its own
  signature and constraints authorize it.

## Limitations

Library validation is not a general malware defense against code already
trusted by the same signing identity, a compromised privileged signer, or
kernel/root-level tampering. It also does not make a writable staging
location safe: keep cache directories private (`0700`), artifacts executable
but not writable by untrusted users, and avoid symlink races.

The current repository's Mach-O rewriter supports thin 64-bit Mach-O inputs.
Universal/fat binaries must be thinned or handled by a slice-aware rewriter
before applying this schema.

## Porting summary

1. Build the shim as a normal dylib.
2. Sign the shim with a real deployment identity.
3. Stage it beside each rewritten executable.
4. Add a relative load command (`@executable_path/x`).
5. Sign the executable with the same identity and Hardened Runtime.
6. Add a library load constraint matching the shim's Team ID and identifier.
7. Remove/reject `DYLD_*` injection variables in the launcher.
8. Validate signatures, constraints, cache invalidation, and replacement
   failures in automated tests.
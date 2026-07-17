# `agentcommit` — Merkle commitment for spawnable commands and security policy

Config-driven tools accumulate a real code-execution/policy-weakening surface: anyone who can
write to `~/.config/pulse/jobs` gets arbitrary shell commands run by `pulse`; anyone who can write
to `~/.config/interpose/config` can silently narrow the deny-list `sandboxd` enforces. `agentcommit`
maintains a single Merkle commitment over the current state of every registered tool's
security-relevant config, anchored somewhere an already-installed persistence monitor (BlockBlock,
LuLu — confirmed actually running on this machine) will notice if it changes: a small, dedicated
LaunchAgent's plist. Tampering with a registrant's config alone is caught by that tool's own
runtime check; tampering sophisticated enough to also "fix" the commitment can only do so by
rewriting the anchor plist, which is the one write an operator would already notice.

## The threat model, in one paragraph

BlockBlock/LuLu watch `~/Library/LaunchAgents/*.plist` at the kernel (Endpoint Security Framework)
level — any write, not just a `launchctl load` call. The Merkle **root** lives there, via a small
purpose-built LaunchAgent (`com.gkgoat.scripts.agentcommit-anchor`) whose only job is to carry the
current root as a `-root <hex>` argument. Per-leaf **proof data** (the sibling hashes needed to
verify one leaf against that root) lives in an ordinary, unwatched sidecar file next to whatever
it's proving. That's safe: `commitment.VerifyProof` always recomputes a leaf's hash fresh from data
the verifier just loaded live, so a tampered proof file can only make verification spuriously
*fail* (safe — falls into the normal "not committed" path), never spuriously *pass* against a root
it wasn't built from, short of a SHA-256 second-preimage. The only way to make a new, malicious
config value verify is to compute a new root and get it back into the anchor plist — which means
rewriting a LaunchAgent plist, which is the monitored write.

## Public API

```go
// commitment
type LeafKind string
const (
    KindCommand LeafKind = "command" // one leaf per spawnable command
    KindPolicy  LeafKind = "policy"  // one coarse leaf per tool's whole policy config
)
type Leaf struct { Tool, ID string; Kind LeafKind; Payload []byte }
func (l Leaf) Key() string

func EncodeKV(fields map[string]string) []byte
func EncodeStringSet(items []string) []byte

type ProofStep struct { Hash [32]byte; Left, Carry bool }
type Proof struct{ Steps []ProofStep }
type Tree struct { /* ... */ }
func Build(leaves []Leaf) (*Tree, error)
func (t *Tree) Root() [32]byte
func (t *Tree) ProofFor(key string) (Proof, error)
func VerifyProof(leaf Leaf, proof Proof, root [32]byte) bool

type ProofFile struct { Root string; Entries map[string]Proof }
func EncodeProofFile(root [32]byte, entries map[string]Proof) []byte
func DecodeProofFile(data []byte) (ProofFile, error)
func RootHex(root [32]byte) string
func ParseRootHex(s string) ([32]byte, error)

// commitment/anchor
const Label = "com.gkgoat.scripts.agentcommit-anchor"
func PlistPath() string
var ErrAnchorNotInstalled error

type PlistToJSON interface{ Convert(path string) ([]byte, error) }
type AnchorReader interface{ ReadRoot() ([32]byte, error) }
type PlistAnchorReader struct { Converter PlistToJSON; Path string }
```

Each registrant's config type owns its own leaf shape, right next to the type's definition, so the
tool that commits a leaf (`agentcommit`) and the tool that verifies it (`pulse`, `sandboxd`) can
never silently drift into computing different shapes for the same data:

```go
// pulse/config
func (j Job) CommitLeaf() commitment.Leaf

// interpose/config
const PolicyLeafID = "policy"
func (c Config) CommitLeaf() commitment.Leaf
```

## Registered leaves

| Tool | Kind | Granularity | Committed fields |
|---|---|---|---|
| `pulse` | `KindCommand` | one leaf per job | `Command` only (not `Interval`/`MaxLoad1` — see Out of scope) |
| `interpose` (shared with `sandboxd`) | `KindPolicy` | one leaf for the whole config | `ExtraProtectedPaths`, `DisableSnapshot` (not `SnapshotPrefix`/`ToolTimeout` — cosmetic, not access-control-relevant) |
| `interpose-command` | `KindPolicy` | one leaf for the entire `kill`/`pkill`/`killall`/`osascript` allowlist | all command names and exact argv rules |

The policy leaf is deliberately coarse — one leaf covering the whole deny-list, not one per
entry — so an attacker can't narrow the list while leaving individual untouched entries' proofs
intact; any change to the list breaks the single leaf's hash.

## The tree: hashing and odd-node convention

Leaf and internal-node hashes are domain-separated (`0x00` / `0x01` prefix, RFC 6962-style), so an
internal-node hash can never be replayed as a valid leaf. `EncodeKV`/`EncodeStringSet`
length-prefix every field, so `Tool="ab"+ID="c"` can never collide with `Tool="a"+ID="bc"`. Leaves
are sorted by `Key()` before building, so the root is independent of registration order. When a
level has an odd number of nodes, the unpaired node is **carried up unchanged**, never duplicated
— duplication is the historically broken convention (the malleability class of bug behind
CVE-2012-2459); carry-up avoids it entirely.

## Anchor read path

`PlistAnchorReader.ReadRoot()` shells out to macOS's built-in `plutil -convert json <path> -o -`
(no new Go dependency, mirroring `extclean`'s `python3`/`tomllib` bridge for Codex's TOML) and
decodes the `-root` argument out of `ProgramArguments`. Only "the plist file doesn't exist" maps
to `ErrAnchorNotInstalled` (verification off — never adopted, fully backward compatible with
tools that never run `agentcommit`). Every other failure — `plutil` missing, a corrupted plist, a
missing `-root` argument, bad hex — is a distinct, opaque error, deliberately **not** collapsed
into "not adopted": otherwise an attacker could blind verification just by breaking the read path
(e.g. deleting `plutil`), and it would look identical to the feature never having been turned on.

## Registrant failure modes (deliberately different per tool)

- **`pulse`** (`pulse/verify.go`, checked in `Scheduler.fire` every tick): a job that fails
  verification is **skipped**, loudly logged (`[error] <job>: commitment verification failed
  ...`), other jobs keep running. Checked per-tick, not just at startup, so fixing a broken anchor
  mid-run self-heals on the next tick without restarting `pulse`. A missing proof entry (a
  brand-new job added straight to the plaintext config, never committed) fails exactly like a
  tampered existing job — both are "this job isn't backed by a valid commitment."
- **`sandboxd`** (`sandbox/daemon/policy_verify.go`, checked once at startup — it's short-lived
  and per-invocation, so per-`OPEN`-request re-verification would be pure overhead): on any
  verification failure, `ExtraProtectedPaths` is dropped entirely and only the fixed built-in
  roots (`tcc.DefaultProtectedRoots()`) are enforced. This can never be *more* permissive than an
  operator with zero custom config already gets, and avoids taking down the entire sandboxed
  process (full fail-closed would deny even the program's own library opens) over one stale policy
  entry. Every fallback logs a `[warn]` line — never silently permissive.

These two failure modes don't have to match, and don't: a scheduler job and a security daemon's
access policy have different failure-safety properties, and each tool's doc/comments state its own
choice and reasoning explicitly rather than inheriting the other's by default.

## Install / usage

```sh
./install-pulse.sh                     # get pulse's LaunchAgent running (no commitment yet)
./install-agentcommit-anchor.sh        # commit current config, install/reload the anchor
```

`install-agentcommit-anchor.sh` builds `agentcommit`, runs `agentcommit commit` (writes
`~/.config/pulse/jobs.proof` if pulse has a config, always writes
`~/.config/interpose/config.proof`) and always writes
`~/.config/interpose/command-allowlist.json.proof` for the process-control
command allowlist, then calls `installer/launchagent.sh`'s existing
`launchagent_install` — reused unmodified — to (re)write the anchor plist with the new root. One
script run, one BlockBlock/LuLu-visible write, regardless of how many leaves changed underneath it.

```sh
./install-agentcommit-anchor.sh --uninstall   # unload/remove the anchor LaunchAgent + binary
```

Uninstalling the anchor doesn't delete the `*.proof` sidecars — without an anchor, every registrant
just reports `ErrAnchorNotInstalled` again (verification off), same as before adopting it.

Manual use:

```sh
./agentcommit.sh commit -pulse-config ~/.config/pulse/jobs   # recompute + write sidecars, print root
./agentcommit.sh anchor -root <hex>                           # what the LaunchAgent actually runs
```

## Adding a new registrant

1. Decide a `LeafKind` (reuse `KindCommand`/`KindPolicy` if it fits; a genuinely new shape of
   "thing that matters" gets a new constant).
2. Add a `CommitLeaf()` method next to your config type's definition (see `pulse/config/leaf.go`,
   `interpose/config/leaf.go`) using `commitment.EncodeKV`/`EncodeStringSet` — never hand-roll
   string concatenation (ambiguity risk without length-prefixing).
3. Wire it into `agentcommit/commit.go`'s leaf-gathering and sidecar-writing.
4. At your tool's own startup (or per-tick if long-running, like `pulse`; startup-only if
   short-lived, like `sandboxd`), read the anchor (`anchor.PlistAnchorReader`), branch on
   `ErrAnchorNotInstalled` vs. any other error vs. `commitment.VerifyProof`'s result, and pick
   **your own** failure mode — it doesn't have to match any other registrant's. Document your
   choice and its reasoning the way `pulse`'s and `sandboxd`'s are documented above.
5. Run `./install-agentcommit-anchor.sh` to recommit and reload the anchor.

## Out of scope for v1

- **Scheduling-parameter tampering**: `pulse`'s `Interval`/`MaxLoad1` are not committed — neither
  is a code-execution risk, just scheduling, so committing them would only create false-positive
  "tampered" noise on cosmetic edits.
- **Temporal/historical audit log**: this is a snapshot-integrity check over the *current* config
  state, not a transparency log proving today's state descends from an authorized history.
- **No protection if `-commit`/the anchor is simply never installed**: by design — manual/dev runs
  of a registrant stay unrestricted unless an operator opts in via `install-agentcommit-anchor.sh`.
- **`agentcommit`'s report mode doesn't cross-check the live installed plist** against the local
  proof sidecars — it only checks live config against the sidecar files. Confirming the *installed*
  plist's `-root` matches is left to the operator noticing (or not noticing) a BlockBlock/LuLu
  prompt when `install-agentcommit-anchor.sh` last ran.
- **Non-PATH tool installs**: the "is this tool installed" style checks elsewhere in this repo
  (e.g. `extclean`) don't apply here — `agentcommit` has no "is X installed" concept; it only cares
  whether a registrant's *config* currently verifies.
- Multi-machine or networked distribution of commitments; a plugin/registry file format beyond
  plain Go code in `agentcommit/sources`-equivalent wiring.

## Tests

```sh
go test ./commitment/... ./commitment/anchor/... ./agentcommit/...
go test ./pulse/...             # verify.go + scheduler.go Verifier wiring
go test ./sandbox/daemon/...    # policy_verify.go
go test ./interpose/policy/tcc/...  # DefaultProtectedRoots/MatchesRoots extraction
make test
```

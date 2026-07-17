# Command-line interposers

Interposers are thin wrappers installed **ahead** of real binaries on `PATH`. Each wrapper
resolves the next matching executable **after itself** on `PATH` and delegates to it, applying
security and backup tweaks first. The process-control wrappers (`kill`, `pkill`, `killall`, and
`osascript`) are deliberately fail-closed: their policy must verify against the committed
`agentcommit` anchor before any argument vector can pass without an interactive confirmation.

## Install

```sh
./install-interposers.sh
# default: ~/.local/bin/interposers/{interpose,git,find,grep,kill,pkill,killall,osascript}
```

This builds the wrappers and also adds the `PATH` export to `~/.profile` (created if
missing) and to `~/.zshrc`/`~/.bashrc` if you already have them, inside a delimited,
namespaced block:

```sh
# >>> scripts:interposers >>>
# managed by scripts installer; edits here will be overwritten - see install script --uninstall
export PATH="$HOME/.local/bin/interposers:$PATH"
# <<< scripts:interposers <<<
```

Re-running the installer updates this block in place rather than duplicating it.
Everything else in your rc files is left untouched. Restart your shell (or re-source the
rc file) to pick up the change.

`PATH` must still include `/usr/bin` (or wherever real tools live) **after** the interposer
directory so delegation works. These are a defense-in-depth guard for programs invoked through
this `PATH`, not a complete macOS containment boundary: a process that can execute an absolute
path such as `/bin/kill`, rewrite its own `PATH`, or modify the installed wrapper can bypass it.
Use OS-level endpoint controls in addition when defending against hostile local code.

To remove the interposers and the `PATH` blocks they added:

```sh
./install-interposers.sh --uninstall
```

The block-editing logic lives in `installer/rcblock.sh`, a small sourceable shell
library (`rcblock_install` / `rcblock_remove`) that future install scripts in this repo
can reuse for their own rc-file entries.

## Wrappers

### git

Before destructive commands, creates a snapshot branch at current `HEAD`:

```
interpose/snapshot/<UTC-timestamp>_<branch>_<shortsha>
```

Triggered by: `reset`, forced `checkout`/`switch`, destructive `restore`, force `push`,
`branch -D`, `clean -f*`, and `revert`.

Snapshots are skipped outside git repos and for repos matching `disable-snapshot` config
prefixes.

### find

When search roots would traverse macOS TCC-sensitive locations (`~/Library`, `~/Documents`,
etc.), injects BSD `find` prune clauses so those directories are never entered.

### grep

Strips protected path operands from recursive searches and injects `--exclude-dir` for
protected directory basenames. Emits warnings on stderr for skipped paths.

### Process-control commands: `kill`, `pkill`, `killall`, `osascript`

These wrappers protect against process-killing/extortion malware. They accept an invocation
without a prompt **only** when its complete argument vector matches the committed command
allowlist. Any non-match — including an unavailable, stale, or uncommitted allowlist — opens
`/dev/tty`, displays a fresh cryptographically-random **six-digit** PIN, and requires the
person at the terminal to type that exact displayed PIN. It refuses to delegate if there is no
controlling terminal, so a background process cannot answer the prompt through stdin. Because
this is a newly emitted challenge rather than a user-chosen value, a process continuously echoing
a fixed response cannot satisfy it. `--no-interpose` is intentionally not an escape hatch for these
four commands.

The default policy is intentionally narrow: it permits only `kill -0 PID` (with optional `--`),
a non-destructive liveness probe. `pkill`, `killall`, and `osascript` have no default-permitted
arguments.

To add an approved operation, create `~/.config/interpose/command-allowlist.json` with JSON
mapping command names to exact argv vectors. `{pid}` is the only wildcard and matches a
non-negative decimal PID:

```json
{
  "kill": [["-0", "{pid}"], ["-TERM", "{pid}"]],
  "pkill": [],
  "killall": [],
  "osascript": []
}
```

After every allowlist edit, run `./install-agentcommit-anchor.sh` to write its inclusion proof
and visibly update the anchored commitment. Until then, changed arguments require the repeated
PIN rather than being silently trusted.

### Escape hatch

Pass `--no-interpose` to skip wrapper transformations (git still runs; git snapshots are
also skipped when this flag is present).

## Configuration

Optional file: `~/.config/interpose/config`

```
extra-protected-path: /path/to/skip
disable-snapshot: /path/to/repo/root
snapshot-prefix: interpose/snapshot
```

`extra-protected-path`/`disable-snapshot` are also read by `sandboxd` (see
`sandbox/daemon/README.md`'s "Policy commitment verification"), which optionally verifies this
config against a Merkle commitment before trusting it — see `docs/agentcommit.md`.

The independent process-control allowlist is JSON at
`~/.config/interpose/command-allowlist.json` (or its embedded default when absent); unlike this
legacy config, it is always required to verify against the commitment before it is trusted.

## Adding a new wrapper

1. Implement `core.Wrapper` in `interpose/wrappers/`.
2. Register the command name in `interpose/main.go`.
3. Add a symlink in `install-interposers.sh`.
4. Add tests under `interpose/wrappers/` and `interpose/core/`.

For a command that needs committed policy, add its policy leaf and proof sidecar to
`agentcommit/commit.go`; do not treat an uncommitted configuration as trusted.

The shared execution flow:

```
ResolveRealBinary → Transform(args) → Before() → exec real binary → After()
```

## Tests

```sh
go test ./interpose/...
make test
```

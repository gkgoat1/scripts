# Command-line interposers

Interposers are thin wrappers installed **ahead** of real binaries on `PATH`. Each wrapper
resolves the next matching executable **after itself** on `PATH` and delegates to it, applying
security and backup tweaks first.

## Install

```sh
./install-interposers.sh
# default: ~/.local/bin/interposers/{interpose,git,find,grep}
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
directory so delegation works.

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

## Adding a new wrapper

1. Implement `core.Wrapper` in `interpose/wrappers/`.
2. Register the command name in `interpose/main.go`.
3. Add a symlink in `install-interposers.sh`.
4. Add tests under `interpose/wrappers/` and `interpose/core/`.

The shared execution flow:

```
ResolveRealBinary → Transform(args) → Before() → exec real binary → After()
```

## Tests

```sh
go test ./interpose/...
make test
```

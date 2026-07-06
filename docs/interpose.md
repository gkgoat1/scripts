# Command-line interposers

Interposers are thin wrappers installed **ahead** of real binaries on `PATH`. Each wrapper
resolves the next matching executable **after itself** on `PATH` and delegates to it, applying
security and backup tweaks first.

## Install

```sh
./install-interposers.sh
# default: ~/.local/bin/interposers/{interpose,git,find,grep}
```

Add to shell rc:

```sh
export PATH="$HOME/.local/bin/interposers:$PATH"
```

`PATH` must still include `/usr/bin` (or wherever real tools live) **after** the interposer
directory so delegation works.

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

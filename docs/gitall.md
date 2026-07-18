# `gitall` repository synchronizer

`gitall` discovers Git repositories under one or more roots and pushes or pulls
them to all of their remotes. It handles nested local-remote chains recursively
so mirrors stay in sync end-to-end, and can fall back to opening or updating a
GitHub pull request when a push fails.

## Usage

```sh
gitall [flags] <push|pull> [root ...]
```

If no roots are given, `.` is used.

## Flags

| Flag | Default | Meaning |
|------|---------|----------|
| `-from` | `any` | Discovery mode: `any` (every `.git` directory) or `prtag` (`.prtag` project markers). |
| `-all` | false | Push tags too; all local branches are always pushed. |
| `-rebase` | false | Pull with `--rebase` (`pull` only). |
| `-m` | `""` | Commit message; if set, stage and commit uncommitted changes before push/pull. |
| `-n` | false | Dry run: print actions without running `git`. |
| `-v` | false | Verbose output. |
| `-proxy` | false | Start a loopback HTTP(S) passthrough proxy and inject it into child `git` processes. |
| `-allow-merge` | `none` | Merge mode on non-fast-forward (`push` only): `none`, `local`, `remote`, or `pr`. Bare `-allow-merge` is shorthand for `pr`. |
| `-pr` | false | Deprecated alias for `-allow-merge=pr`. |
| `-timeout` | `0` | Maximum time any single external command may run (e.g. `30s`). `0` disables. |

## Discovery modes

- `-from any` (default): every directory containing a `.git` entry under the
  roots is treated as a repository.
- `-from prtag`: directories containing a `.prtag` marker are treated as
  project roots and scanned for nested repositories. See `docs/prtag.md`.

`gitall` synchronizes every local branch, not just the currently checked-out
branch. Before each branch is pushed, it is fetched and fast-forwarded (or
merged when enabled) from the corresponding branch on each remote; each branch
is then pushed explicitly. Pulls likewise fetch and pull every local branch.
Branches which do not yet exist on a remote are safely skipped while pulling
and created while pushing.

Repositories must still be clean unless `-m` is supplied. `gitall` temporarily
checks out branches one at a time and restores the original branch (or detached
`HEAD`) when finished, so commits are never accidentally merged, rebased, or
pushed as a different branch.

`-all` remains available to push tags in addition to this default all-branch
behavior.

## Local-remote chains

Local remotes are resolved and handled recursively. Given a chain such as
`~/work -> ~/mirror -> github.com`:

- `gitall push`: pulls upstream into mirror, syncs and pushes every branch in
  work, then syncs and pushes every branch in mirror to GitHub.
- `gitall pull`: pulls every branch through the chain in the opposite direction.

Cycles are prevented by tracking repositories on the current recursion path.

## Merge modes (`-allow-merge`)

`-allow-merge` controls what `gitall` does when a fast-forward sync is not
possible while pushing. It accepts one of four levels:

| Mode | Meaning |
|------|---------|
| `none` (default) | Never merge; report the divergence and continue. |
| `local` | Merge only when the remote is a local (filesystem) path. |
| `remote` | Merge both local and network remotes. |
| `pr` | Same as `remote`, and when a push to a GitHub remote still fails, fall back to opening or updating a PR via `gh`. |

Examples:

```sh
gitall -allow-merge=local push ~/work        # merge into local mirrors only
gitall -allow-merge=remote push ~/work       # merge into any remote
gitall -allow-merge=pr push ~/work           # merge + PR fallback
gitall -allow-merge push ~/work              # shorthand for -allow-merge=pr
```

The old boolean `-allow-merge` flag is retained as a shorthand for `-allow-merge=pr`, and `-pr` is retained as a deprecated alias for the same thing.

## `checkout HEAD` after updates

Whenever `gitall` updates a repository—whether by merge, pull, or pushing into a
local remote—it runs `git checkout HEAD` in that repository afterward. Working
tree mismatches after a remote update are therefore reconciled automatically.
Failures are logged but not fatal.

## Per-repo concurrency guard

Every resolved repository path is protected by a per-invocation mutex. Two
independent source repositories that share a local remote cannot mutate that
remote at the same time, eliminating race conditions on refs, index, and
working tree. The mutex is held for the entire `operate` call and also across
pushes/fetches into local remotes.

## PR targets

When PR fallback is triggered, `gitall` always creates the PR against the
remote named in the failed push, using that remote's configured fetch URL slug
(e.g. `-R owner/repo`). It never infers or falls back to an `upstream` remote.

## Proxy passthrough (`-proxy`)

With `-proxy`, `gitall` starts a temporary loopback HTTP(S) passthrough proxy
for the lifetime of the process and injects the following variables into every
child `git` process:

```text
HTTP_PROXY=http://127.0.0.1:<port>
HTTPS_PROXY=http://127.0.0.1:<port>
NO_PROXY=localhost,127.0.0.1,::1,*.local,[existing values]
```

The proxy binds only to `127.0.0.1` and forwards traffic without inspecting
or modifying it. HTTPS traffic is tunneled via `CONNECT`. The proxy stops
when `gitall` exits.

This lets an existing firewall intercept and tag child (`git` process) traffic
separately from `gitall`'s own traffic, without creating a new firewall setup.
The proxy is opt-in per invocation.

## Timeouts

A per-command timeout can be set with `-timeout` (e.g. `-timeout=30s`) or via
the `GITALL_TIMEOUT` environment variable. A default can also be configured in
`~/.config/interpose/config` with the key `tool-timeout`. `GITALL_TIMEOUT`
takes precedence over the config default, and `-timeout` takes precedence over
both.

## Tests

```sh
go test ./gitall/...
make test
```
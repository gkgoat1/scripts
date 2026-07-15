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
| `-all` | false | Push all branches and tags (`push` only). |
| `-rebase` | false | Pull with `--rebase` (`pull` only). |
| `-m` | `""` | Commit message; if set, stage and commit uncommitted changes before push/pull. |
| `-n` | false | Dry run: print actions without running `git`. |
| `-v` | false | Verbose output. |
| `-proxy` | false | Start a loopback HTTP(S) passthrough proxy and inject it into child `git` processes. |
| `-pr` | false | On push failure to a GitHub remote, open or update a PR via `gh`. |
| `-allow-merge` | false | Merge remote changes when fast-forward is not possible (`push` only). |
| `-timeout` | `0` | Maximum time any single external command may run (e.g. `30s`). `0` disables. |

## Discovery modes

- `-from any` (default): every directory containing a `.git` entry under the
  roots is treated as a repository.
- `-from prtag`: directories containing a `.prtag` marker are treated as
  project roots and scanned for nested repositories. See `docs/prtag.md`.

## Local-remote chains

Local remotes are resolved and handled recursively. Given a chain such as
`~/work -> ~/mirror -> github.com`:

- `gitall push`: pulls upstream into mirror, syncs and pushes work, then syncs
  and pushes mirror to GitHub.
- `gitall pull`: pulls the chain in the opposite direction.

Cycles are prevented by tracking repositories on the current recursion path.

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
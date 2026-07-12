# `extclean` — dead extension/hook/MCP/plugin cleanup

Four AI coding agents — Claude Code, Cursor, Codex CLI, and Pi — each keep their own
hook/MCP-server/plugin/extension registrations on disk. These accumulate stale entries over
time: a Homebrew upgrade moves a versioned binary path, a tool gets uninstalled but its config
file is left behind. `extclean` scans all four and reports (or, with `-apply`, removes) entries
matching two criteria. It reports by default — nothing is changed on disk unless `-apply` is
passed.

## What is scanned

| Tool | Config file(s) | Entries |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | `hooks.<Event>[].hooks[].command` |
| | `~/.claude.json` | global + per-project `mcpServers` |
| | `~/.claude/plugins/installed_plugins.json` | plugin `installPath` |
| | `~/.claude/plugins/known_marketplaces.json` | marketplace `installLocation` |
| Cursor | `~/.cursor/extensions/extensions.json` | extension `relativeLocation` |
| | `~/.cursor/mcp.json` | `mcpServers` |
| Codex | `~/.codex/config.toml` | `marketplaces.*.source`, `plugins."name@marketplace"` (backing cache dir), `mcp_servers.*.command` |
| Pi | `~/.pi/agent/settings.json` | `packages` (`npm:<name>` → `node_modules/<name>`) |

Two removal criteria:

- **Dangling** — the entry's command/path doesn't exist on disk, or (for a bare command name)
  doesn't resolve via `PATH`.
- **Orphaned** — the owning tool isn't installed on this system at all, so every entry in that
  tool's config file is a stale leftover, independent of whether any individual entry still
  resolves.

**Explicitly out of scope**: `~/.claude.json`'s `projects.*` keys, `~/.cursor/projects/*`
workspace-storage folders, and Codex's `[projects."path"]` trust entries — these can also go
stale (a project directory gets deleted) but that's a different pattern from the two criteria
above, and isn't scanned here.

## Command/path resolution rules

Claude Code hook commands are shell one-liners (e.g. `KEY=val KEY2=val2 /path/to/bin arg`), so
they're tokenized (quote-aware) with leading `KEY=value` env-assignments stripped before
resolving the first remaining token. Cursor/Claude `mcpServers.*.command` and Codex
`mcp_servers.*.command` are single literal executable strings (never shell one-liners — Codex's
real `computer-use` example, `"./Codex Computer Use.app/.../SkyComputerUseClient"`, has unquoted
spaces that shell-tokenizing would wrongly split on), so they're resolved directly as one token,
relative paths resolved against `cwd` if the entry has one (Codex), defaulting to `~/.codex/`.
Absolute/relative paths are checked for existence; bare names are resolved via `PATH`.

## The "is this tool installed" check

- Claude Code / Codex / Pi: does `claude` / `codex` / `pi` resolve via `PATH`.
- Cursor: does `/Applications/Cursor.app` exist (no reliable CLI binary for a GUI app).

Known limitation: a non-`PATH` install of Claude Code, Codex, or Pi would false-flag as "not
installed," orphaning that tool's whole config. Harmless in the common case (all three ship a
CLI on `PATH`), but worth knowing.

## Codex TOML handling

Codex's config is TOML; every other tool here is JSON, handled with stdlib `encoding/json`, and
this repo has zero third-party Go dependencies. Rather than adding one just for Codex, reads
shell out to a discovered Python interpreter's stdlib `tomllib` (Python 3.11+):

```sh
python3 -c 'import tomllib,json,sys; json.dump(tomllib.load(open(sys.argv[1],"rb")), sys.stdout)' config.toml
```

Bare `python3` on `PATH` isn't guaranteed to have `tomllib` (a system Python can be older), so a
short candidate list is probed (`python3`, `python3.11`–`python3.13`, `/opt/homebrew/bin/python3`)
for the first one that actually has it.

`-apply` never re-serializes the TOML file (which would risk reordering or losing comments) —
`RemoveTomlTable` does a line-based excision of just the one `[section.header]` block, a direct
port of `installer/rcblock.sh`'s `_rcblock_strip` marker-stripping idea to TOML table
boundaries: find the exact header line, delete through the line before the next top-level
`[...]` header or EOF, and **error rather than guess** if the header appears zero or more than
once. A nested dotted continuation table (e.g. `[mcp_servers.foo.env]` right after
`[mcp_servers.foo]`) also ends the excision there and is left behind — a known, deliberate
limitation of the line-based approach.

## `-apply` behavior and safety

For the three JSON tools, `-apply` decodes the file, removes the specific entry, and
re-encodes with `json.MarshalIndent` — this does **not** preserve the original file's exact
formatting/key order, an accepted trade-off since Claude Code, Cursor, and Pi all rewrite these
files themselves during normal operation (unlike `installer/rcblock.sh`'s hand-curated shell rc
files, where preserving everything outside the block matters more). Every write (JSON or TOML)
goes through a temp-file + atomic rename, so a reader never observes a partial write. Each
finding is applied independently; a failure on one (e.g. the file changed since the scan) is
reported and skipped rather than aborting the run — the final line reports `N applied, M
failed`, and the process exits 1 if any application failed.

## Install / Usage

```sh
./extclean.sh                       # report only
./extclean.sh -apply                # remove everything flagged
./extclean.sh -tool codex           # scope to one agent
./extclean.sh -json                 # machine-readable output
```

No persistent install — this is a manually-run report/cleanup tool, like `cleanartifacts`.

## Out of scope for v1

- Stale project-path entries (see "What is scanned" above).
- Auto-repairing Codex's relative-`cwd` fragility beyond detecting it as dangling if actually
  broken — no attempt to rewrite `cwd` or guess intent.
- Auto-detecting agents beyond these four.
- Interactive per-finding y/n triage — `-apply` is all-or-nothing (optionally scoped by `-tool`).
- Liveness-based staleness checks (Claude Code's own plugin cache uses an `.in_use/<pid>`
  marker-file pattern for this) — `extclean`'s checks are purely existence-based.
- No scheduling/daemon mode, no GUI/TUI.

## Tests

```sh
go test ./extclean/...
```

The Codex TOML-bridge integration test (`TestRealTomlReaderIntegration`) exercises the real
`python3`/`tomllib` subprocess path and skips cleanly if no such interpreter is found in the
test environment; every other test uses injected fakes and never touches a real `~/.claude`,
`~/.cursor`, `~/.codex`, `~/.pi`, or the real `PATH`/installed apps of the machine running the
tests.

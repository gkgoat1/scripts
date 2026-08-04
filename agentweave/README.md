# AgentWeave

AgentWeave is a macOS-first, local-only evidence index for coding-agent
conversations and artifacts. It indexes Claude Code, Cursor, Codex, Pi,
version-gated Antigravity 2.4.3 storage, and explicitly configured artifact
roots. It does not start an agent, store model credentials, or send indexed
content anywhere by itself.

`agentweaved` owns the SQLite/FTS index and a user-only Unix socket. Each
`agentweave mcp` process is a small stdio MCP façade for one client session.
That separation lets an explicit sampled synthesis use the model belonging to
the client that requested it, rather than a daemon-owned model.

## Build and run

```sh
go build -o /usr/local/bin/agentweave ./agentweave/cmd/agentweave
go build -o /usr/local/bin/agentweaved ./agentweave/cmd/agentweaved
agentweave start
agentweave sync
agentweave doctor
```

The default data directory is
`~/Library/Application Support/AgentWeave`; its directory, database files,
PID, log, and Unix socket are created owner-only. `start` launches the shared
daemon. `sync` contacts it when available and otherwise performs one local
sync. `stop`, `status`, and `doctor` are safe diagnostics.

To add hand-authored artifacts, create
`~/Library/Application Support/AgentWeave/config.json` manually:

```json
{
  "artifact_roots": ["/absolute/path/to/project/docs"],
  "deny_globs": ["*/private/*", "*/secrets/*"],
  "poll_seconds": 30
}
```

Artifact roots are never discovered implicitly. Source files matching common
credential/auth names, caches, binaries, SQLite databases, and deny globs are
not read. This is a same-OS-user privacy boundary, not a hardened isolation
boundary against other processes running as that user.

## MCP clients

Apply these manually to the client’s own MCP configuration; v1 never edits
client settings automatically. The generated snippets are also available with
`agentweave config <client>`.

For Claude Code, Cursor, and Antigravity:

```json
{
  "mcpServers": {
    "agentweave": { "command": "agentweave", "args": ["mcp"] }
  }
}
```

For Codex:

```toml
[mcp_servers.agentweave]
command = "agentweave"
args = ["mcp"]
```

Install the Pi bridge from the JS workspace after building or publishing it:

```sh
cd agentweave/js
npm install
npm run build --workspace=agentweave-pi-mcp
pi install /absolute/path/to/agentweave/js/packages/pi-mcp
```

The Pi extension registers the four tools plus `/agentweave <query>`. It opens
a separate stdio MCP connection per Pi tool call. Pi sampling asks for an
interactive approval by default; `--agentweave-allow-sampling` is the explicit
per-run opt-in for noninteractive use. If the active Pi provider cannot sample,
use `generation: "evidence"` and the tool reports that limitation.

## MCP contract

- `agentweave_search(workspace, query, agents?, kinds?, limit?)` returns ranked
  excerpts with stable `aw:` evidence references.
- `agentweave_read(workspace, refs, max_bytes?)` returns bounded source chunks.
- `agentweave_synthesize(workspace, question, selection?, detail?, generation)`
  accepts only `generation: "evidence"` or `"sample"`.
- `agentweave_status()` reports source freshness, errors, and unsupported
  layouts without returning source text.

Search and read require an exact canonical workspace. Cross-project/global
retrieval is deliberately not the default. Retrieval uses SQLite FTS with
deterministic source and recency weighting; there are no embeddings and no
hidden model calls.

`generation: "evidence"` is deterministic and returns an evidence dossier,
including missing support and contradictions. `generation: "sample"` is only
available in a legacy MCP sampling session that negotiated sampling support. It
makes exactly one `sampling/createMessage` request with the selected evidence,
does not supply delegated tools, rejects non-text/tool continuations, and
validates returned citations before returning an answer. A denied or unavailable
sampling capability is an error; it never silently falls back to evidence mode.

## Source support

Adapters are incremental and read-only. Malformed or incomplete trailing JSONL
records are ignored while earlier valid entries are retained. Codex additionally
uses its dedicated thread/spawn and goal metadata databases to preserve parent
lineage. Antigravity is intentionally strict: only an identified 2.4.3
persistent chat layout is accepted. Unknown versions or missing verified layout
produce an `unsupported` diagnostic; cache/blob-storage scraping is never used.

Run `go test ./agentweave/...` for the local adapter, index, socket, citation,
and privacy-boundary test suite.

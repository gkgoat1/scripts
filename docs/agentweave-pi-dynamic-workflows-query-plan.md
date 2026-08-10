# Plan: privacy-preserving retrieval of pi-dynamic-workflows

## Status, evidence, and scope

This is an implementation plan, not an implementation. “Verified” statements below
were confirmed against this repository and the locally installed
`pi-dynamic-workflows` checkout (`3.5.1`, commit `bab5ad7`). “Assumption” marks a
contract that must be confirmed with that package's maintainers or established by
tests before relying on it.

### Verified current state

- AgentWeave is a local, read-only, adapter-driven SQLite/FTS5 evidence index.
  `agentweaved` owns the database and Unix socket; the stdio MCP server and the
  Pi extension are clients. Ingestion follows `ScanAll` → daemon `Sync` → index
  replacement and normally polls every 30 seconds.
- Artifact provenance includes agent, kind, workspace, source path/record,
  timestamps, parent ID, retained text, and stable citeable `aw:` chunk refs.
  Searches and reads require the exact canonical workspace unless
  `include_global` is explicitly selected.
- The existing adapters index Pi conversation JSONL, but do **not** index
  pi-dynamic-workflows saved definitions or persisted workflow runs. The
  generic filesystem adapter intentionally admits only selected text formats.
- The Pi MCP bridge supplies `ctx.cwd` on each call and exposes search, read,
  synthesis, and status. Its only model use is the already explicit,
  client-owned, one-shot sampled synthesis path.
- pi-dynamic-workflows stores saved workflows and runs outside projects by
  default, under `~/.pi/workflows`. Its project directory includes a sanitized
  cwd basename and a truncated hash of canonical cwd. That directory name is
  not safely reversible to the workspace path. Legacy project-local saved/run
  locations remain compatibility reads.
- Saved definitions have a name, description, script, optional parameters,
  location/path, and saved timestamp. Resolution precedence is project, legacy
  project, user, then built-in. Built-ins are generated descriptors rather than
  saved workflow files.
- Persisted run state can contain scripts, args, prompts, results, histories,
  logs, session IDs, and operational data. Full subagent Pi sessions may also
  be persisted separately when the package option is enabled; they extend the
  main Pi transcript and must be indexed with equivalent provenance and
  workspace isolation.
- Existing source replacement is keyed by `source_path`: all artifacts derived
  from one path are replaced together. Therefore one user workflow source must
  not be duplicated as normal workspace artifacts without an identity-model
  change.

### Goal

Enable an AgentWeave user, from an exact workspace, to discover, cite, and
retrieve both saved workflows applicable to that workspace and the workflow-run
transcript material that extends that workspace’s Pi conversations.

- “Find a workflow for adversarial review.”
- “What parameters and phases does our `code-review` workflow have?”
- “Is there a project workflow that verifies a change?”

The first release indexes **saved workflow definitions and persisted workflow
runs**. Definition retrieval may expose scripts as bounded, citeable evidence,
but must favor name, description, parameters, and declared metadata over
boilerplate script text. Run retrieval treats persisted subagent sessions,
histories, results, and logs as transcript evidence, using the same chunking,
citation, workspace-scoping, and retention semantics as the main Pi transcript.
It must retain scope, source, freshness, and shadowing provenance.

### Non-goals and hard boundaries

- Do not execute, resolve for execution, schedule, import, evaluate, or
  otherwise run workflow code. Querying a workflow or its transcript is not
  approval to run it.
- Do not reverse a project-key hash, infer a workspace from a slug, repository
  remote, basename, script content, or nearby working directory.
- Do not crawl arbitrary projects or arbitrary directories under `~/.pi`.
  Discovery must be limited to a versioned verified layout plus an explicit
  workspace attribution mechanism.
- Index manifest-proven workflow runs. Runs are transcript extensions, not a
  separate sensitive-data class: index persisted prompts, histories, results,
  logs, arguments, and optional subagent sessions under the same local evidence,
  scoping, retention, purge, and access rules as the main Pi conversation adapter.
- Do not execute, resolve for execution, schedule, import, evaluate, or
  otherwise run workflow code. Querying a workflow or its transcript is not
  approval to run it.
- Do not make user-scoped or built-in definitions visible in ordinary
  workspace searches merely because they are locally readable.
- Do not treat indexed script text as trusted instructions. It remains
  untrusted source evidence under the existing dossier warning.

## Proposed architecture

### 1. Explicit workspace attribution is the integration seam

Add an opt-in, versioned `agentweave-manifest.json` written by
pi-dynamic-workflows for every project state it creates or saves. The package,
not AgentWeave, knows canonical cwd when it derives its project key. The
manifest belongs at:

```text
~/.pi/workflows/projects/<project-key>/agentweave-manifest.json
```

Initial format:

```json
{
  "schema_version": 1,
  "workspace": "/absolute/canonical/project/path",
  "project_key": "widget-a1b2c3d4e5f6",
  "saved_dir": "/Users/me/.pi/workflows/projects/widget-a1b2c3d4e5f6/saved",
  "updated_at": "2025-01-01T12:00:00.000Z"
}
```

The manifest contains paths and identifiers only, never scripts, arguments,
or results. It must be atomically written, use owner-only permissions where
the host permits them, and canonicalize cwd with the same documented policy
used by AgentWeave (`CanonicalWorkspace`). AgentWeave must canonicalize again;
it must not trust a manifest declaration blindly.

**Assumption to validate:** the external package can add this manifest without
changing saved-workflow resolution or its atomic-write compatibility contract.
If it cannot, phase 1 can use an explicit local bridge registration from the
AgentWeave Pi extension, but registration must be authenticated by the existing
same-user socket protocol and persist only canonical workspace/project-key
mapping. It must not turn each query into a filesystem scan.

### 2. Dedicated native adapter

Add `PiWorkflowsAdapter` rather than extending `FilesystemAdapter`. Workflow
JSON requires scope-aware extraction, manifest validation, strict field
allowlisting, and atomic-write handling; generic configured artifact roots are
the wrong trust boundary.

Add these first-class identifiers in `agentweave/core/model.go`:

```go
AgentPiWorkflows Agent = "pi_workflows"
KindWorkflow     Kind  = "workflow"
KindWorkflowRun  Kind  = "workflow_run"
```

Register the adapter in `DefaultAdapters()` in `agentweave/core/adapters.go`.
The adapter is read-only and may only enumerate the manifest, saved-definition,
and persisted-run paths explicitly confirmed in the pi-dynamic-workflows
layout, for example:

```text
~/.pi/workflows/projects/*/agentweave-manifest.json
~/.pi/workflows/saved/*.json
```

For a valid manifest it may read only the declared `saved_dir`, after proving
that its resolved path remains beneath that manifest's expected project root.
It must ignore `.bak`, `.lock`, logs, temporary names, run files, and all
other files. It must never follow a manifest to an arbitrary directory or a
symlink escape.

### 3. Definition model and index model

Keep the normal `Artifact` as the citeable, chunked evidence carrier and add
workflow-specific relational projections. Run records are normalized into
transcript-like artifacts/chunks and retain manifest-proven canonical workspace,
run ID, workflow name where available, record timestamp, source path/record,
and parent relationships to definitions and sessions. Avoid duplicate chunks
when the same underlying Pi session JSONL is already indexed by `PiAdapter`:
use a shared source identity (canonical path plus record identity/content
fingerprint), or delegate those session files to the Pi transcript parser.
Run-specific state with no transcript analogue is indexed as `workflow_run`
evidence.

Add `workflow_definitions` in `agentweave/core/index.go` migrations:

```sql
CREATE TABLE workflow_definitions (
  artifact_id TEXT PRIMARY KEY REFERENCES artifacts(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  scope TEXT NOT NULL,
  declared_workspace TEXT NOT NULL DEFAULT '',
  project_key TEXT NOT NULL DEFAULT '',
  precedence INTEGER NOT NULL,
  saved_at_ms INTEGER NOT NULL DEFAULT 0,
  script_hash TEXT NOT NULL,
  parameters_json TEXT NOT NULL DEFAULT '[]',
  meta_json TEXT NOT NULL DEFAULT '{}',
  meta_parse_status TEXT NOT NULL DEFAULT 'unavailable',
  package_version TEXT NOT NULL DEFAULT '',
  visibility TEXT NOT NULL DEFAULT 'workspace',
  indexability TEXT NOT NULL DEFAULT 'full'
);
CREATE TABLE workflow_runs (
  artifact_id TEXT PRIMARY KEY REFERENCES artifacts(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL,
  workflow_name TEXT NOT NULL DEFAULT '',
  workspace TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  record_type TEXT NOT NULL,
  parent_definition_id TEXT NOT NULL DEFAULT '',
  started_at_ms INTEGER NOT NULL DEFAULT 0,
  finished_at_ms INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT '',
  source_fingerprint TEXT NOT NULL
);
CREATE INDEX workflow_runs_lookup_idx
  ON workflow_runs(workspace, workflow_name, run_id, started_at_ms);
CREATE INDEX workflow_definitions_lookup_idx
  ON workflow_definitions(declared_workspace, name, precedence);
CREATE INDEX workflow_definitions_name_idx ON workflow_definitions(name);
```

A definition artifact has a stable ID formed from source path, scope, canonical
workspace or project key, and workflow name. The content fingerprint is based
on normalized relevant fields (`name`, `description`, `parameters`, `script`),
not JSON formatting. The FTS document has separable logical fields (or clearly
delimited content) in this priority order:

1. name and description;
2. parameter names, types, defaults, and descriptions;
3. safely extracted literal metadata/phases and derived capability labels;
4. script body at low weight.

Store scope (`project`, `legacy_project`, `user`, later `builtin`), resolution
precedence, declared workspace, project key, script hash, source timestamp,
and metadata parsing status. A malformed or unavailable metadata parse must
not claim phases/capabilities it could not establish.

The Go adapter must never launch Node or import the Pi package merely to parse
a script. Initially it may index the saved JSON fields and mark literal script
metadata as unavailable/invalid. A later narrowly scoped Go parser may accept
only the required literal `export const meta = ...` form; it must fail closed
on expressions, imports, or anything requiring evaluation.

### 4. Scope, shadowing, and global definitions

Project definitions are indexed only when an exact canonical workspace is
proven by a valid manifest or explicit bridge registration. Legacy project
files are indexed only for an explicit known workspace (configured artifact
root or registration); they are not globally discovered.

User definitions are indexed once with `Artifact.Workspace == ""` and
`visibility = "user"`. This respects source-path replacement and means they
cannot leak into normal workspace search. Add a separate
`include_user_workflows` request filter; do not require callers to use the
broader `include_global` switch just to find an intentional user workflow.

Return every matching variant only when requested for diagnostics. Normal
workflow discovery returns the effective definition first according to the
verified resolution order, and labels alternatives as shadowed. Precedence is
resolution behavior, not a truth or security score.

Built-ins are phase 2. They are metadata-only synthetic artifacts emitted from
a bridge report that includes package version/revision and a deterministic
manifest hash. Do not index generated scripts. Built-ins are opt-in at query
time because availability is tied to an installed active package, not merely a
stale checkout on disk.

## Ingestion, lifecycle, and query behavior

### Parsing and failure handling

A persisted run must be parsed using pi-dynamic-workflows’ confirmed schema and
emitted in its natural event/history order. Persisted session transcripts use
the same normalization and chunking policy as `PiAdapter`; the workflow adapter
adds run provenance rather than a lossy summary. The adapter may enumerate only
run files at exact names/locations under a manifest-proven project root, ignores
temporaries, never follows symlinks outside that root, and retains last-known-
good evidence across transient malformed writes just as it does definitions.

Read primary JSON only. On a missing or transiently unreadable primary, retry
once after a small bounded delay. Never index `.bak` as an independent record.
If a formerly valid primary becomes malformed, retain its last successfully
indexed revision, mark source status stale/error with the file-specific reason,
and replace it only after a successful parse and transaction. This avoids a
brief write race deleting useful evidence.

Manifest validation must check schema version, canonical workspace, expected
project key/root, and resolved containment of `saved_dir`. A failed validation
is a security diagnostic and yields no artifact. A project bucket without a
manifest is `metadata_incomplete`/`unsupported`, never heuristically assigned.

Existing index pruning needs an adapter-aware expected-source set so a
transient invalid file does not cause deletion of its last-known-good records.
This behavior must be designed explicitly before applying the generic
`pruneMissingSources()` path.

### Search and read

Extend `SearchRequest` with `IncludeUserWorkflows bool`; retain
`IncludeGlobal` unchanged for backwards compatibility. Search behavior is:

- exact workspace workflows are eligible by default;
- user workflows require `include_user_workflows: true`;
- built-ins later require `include_builtin_workflows: true`;
- empty workspace remains rejected unless an existing explicit global mode is
  selected; this feature does not weaken that requirement;
- workflow results retain ordinary `aw:` refs and need `agentweave_read` for
  larger source text.

Add a workflow-aware search path in the index that joins structured metadata
for filtering/deduplication and uses deterministic field-aware ranking. Rank
name/description and parameter/phase matches above script hits, prefer exact
workspace scope, apply a documented shadowed penalty, and retain current FTS
and recency behavior. Scores and returned reasons must be reproducible; no
model reranking is permitted.

Add `KindWorkflowRun` and bounded run filters (`workflow_name`, `run_id`,
`session_id`, plus time/status where appropriate) without changing generic
transcript search defaults. Generic workspace searches include manifest-proven
workflow-run transcript artifacts just as they include main Pi transcripts.
A dedicated `agentweave_find_workflows` convenience tool remains phase 2; it
may provide run-aware diagnostics but is not required for run evidence to be
searchable and readable.

## API, tool, and configuration changes

1. **Core:** update `model.go` types and requests/results; define typed
   workflow metadata rather than loose maps at the adapter/index boundary.
2. **MCP:** update `agentweave_search` JSON schema and handler in
   `agentweave/mcpserver/server.go` with `include_user_workflows`. Preserve all
   current fields and defaults. Add the dedicated finder only in phase 2.
3. **Pi bridge:** update TypeBox schema and request type in
   `agentweave/js/packages/pi-mcp/src/index.ts`. It continues to inject only
   `ctx.cwd`; it does not send workflow source or call a model to discover it.
4. **Status:** extend `SourceStatus` carefully with optional `last_attempt_at`,
   `last_success_at`, and `stale_since` fields, and add meaningful states such
   as `metadata_incomplete` and `stale` without changing old callers' handling.
5. **Config:** add narrow explicit settings in `core.Config`, for example
   `workflow_indexing` (default enabled for definitions and manifest-proven
   runs once the adapter ships). It must use the same retention and purge
   configuration that governs Pi transcript indexing; do not add a separate
   `index_workflow_runs` opt-in switch. Existing config files remain valid.

## Provenance, privacy, and trust

- Preserve exact `Artifact.Agent`, `Kind`, workspace, source path/record,
  timestamps, stable ref, and workflow scope in all result paths. Add parent
  relationships only if later run summaries create children; relation storage
  alone is not a retrieval feature.
- The local same-user socket is not a stronger multi-user authorization
  boundary. Nevertheless, manifests, paths, scopes, and source text must be
  treated as untrusted input and validated before use.
- Scripts may contain secrets. Definition script indexing is a conscious local
  evidence expansion: make it configurable, honor deny globs where applicable,
  bound indexed/read text, and document opt-out. Run ingestion stays off by
  default because its sensitivity is substantially higher.
- With `persistAgentSessions=true`, run data can duplicate Pi conversation
  transcripts already indexed by `PiAdapter`. Deduplicate shared persisted
  session sources rather than suppressing workflow material; relation metadata
  must preserve that a transcript was produced by a workflow run.
- Retrieval never bypasses workspace authorization because a caller already
  has an `aw:` ref. Reads must enforce the same workspace/user-global decision
  as searches and must never fall back to direct filesystem reads.

## Backward compatibility and migration

- All existing adapters, sources, searches, citations, dossiers, and MCP tool
  calls preserve their behavior when workflow indexing is absent or disabled.
- A missing `~/.pi/workflows` is `unavailable`, not a sync failure.
- Older pi-dynamic-workflows installations without manifests get only safe user
  definition indexing. Project hash directories remain deliberately
  unattributed. Users can upgrade the package or explicitly register/configure
  a known workspace; AgentWeave never guesses.
- Existing project saved definitions become discoverable on the next poll after
  a valid manifest is created. No source rewrite or workflow migration is
  required.
- Add an idempotent SQLite migration and test opening an existing index. Keep
  old database records readable and ensure deletion cascades remove workflow
  metadata with artifacts.

## Phased implementation

### Phase 0 — contracts and fixtures

- Confirm manifest write semantics, saved-file schema/name validation, project
  key algorithm, version reporting, and atomic-write behavior with the
  pi-dynamic-workflows repository.
- Define fixtures for valid manifest/project definition, user definition,
  project/user shadowing, invalid JSON, `.bak` beside primary, absent manifest,
  containment/symlink escape, worktree separation, and stale-last-good state.
- Targets: `agentweave/core/adapters_test.go`, `core/index_test.go`,
  `daemon/daemon_test.go`, and a new `core/workflows_test.go`.

### Phase 1 — saved definitions and workflow-run transcript retrieval

- Add types including `KindWorkflowRun`, `PiWorkflowsAdapter`, manifest
  reader/parser, strict path checks, run-state/session parsers or delegation to
  Pi transcript parsing, status diagnostics, adapter registration, and
  cross-adapter deduplication identity.
- Add schema migrations, transactional definition/run metadata writes/deletes,
  scoped generic search filtering, deterministic workflow result ordering, and
  stale preservation.
- Index every manifest-proven persisted run record with normal transcript-like
  chunks; preserve run/session/definition parent links and use the same
  retention/purge semantics as Pi conversations.
- Add `include_user_workflows` through daemon transport, MCP schema/handler,
  and Pi extension TypeBox schema.
- Targets: `agentweave/core/model.go`, `core/config.go`, `core/adapters.go`,
  new `core/workflows.go`, `core/index.go`, `agentweave/daemon/daemon.go` and
  transport types as needed, `mcpserver/server.go`, and
  `js/packages/pi-mcp/src/index.ts`.
- In the external dependency, target `src/workflow-paths.ts`,
  `src/run-persistence.ts`/`src/workflow-manager.ts` or the actual save path,
  and existing atomic persistence helper to write the manifest. This change is
  separately versioned and must be optional/compatible.

### Phase 3 — richer discovery and built-ins

- Publish bridge-reported versioned built-in descriptors and index them as
  metadata-only global artifacts.
- Add `agentweave_find_workflows`, structured parameter/capability and
  run-status filters, explicit `include_builtin_workflows`, returned
  provenance/reasons, and shadowing diagnostics.
- Targets: `core/index.go`, `mcpserver/server.go`, Pi bridge source/tests, and
  the external package's built-in registry/extension integration.

## Test plan and validation

Use real temporary files, directories, symlinks where supported, SQLite, and
socket paths rather than mocks for boundary behavior. Include at least:

- valid project and user definitions plus manifest-proven workflow runs;
  stable IDs, content fingerprint updates, transcript chunking/order,
  citations, parent links, transactional replacement, and no duplicate evidence
  for a shared Pi session source;
- exact workspace isolation, including sibling worktrees and canonical-path
  variants; user definitions absent without explicit inclusion;
- project/user/built-in shadowing order and diagnostic variant behavior;
- absent homes, no manifest, corrupt manifest, malformed JSON, invalid name,
  future fields, missing primary, `.bak` handling, atomic-write retry, and
  retention of a last-known-good artifact;
- rejected path traversal, manifest outside root, symlink escape, and denied
  paths; prove no arbitrary recursive scan;
- query limits, FTS-operator quoting, low script weighting, filters, and
  deterministic order/scores;
- MCP and Pi schema forwarding; preserve current evidence/sample behavior and
  prove workflow discovery itself invokes no model or workflow runtime;
- migration from an old database, source deletion cascade, and status
  freshness/stale diagnostics;
- race-enabled polling/sync tests where mutations occur during scans.

Run the repository gate:

```sh
make test
npm run build --workspaces --prefix agentweave/js
npm run test --workspaces --prefix agentweave/js
```

Also run the external package's relevant test/build commands when changing its
manifest contract. Record exact commands and versions in the implementation
PR, since the current repository CI does not run the AgentWeave JS workspace.

## Observability and rollout

Status must report adapter root, schema/version compatibility, discovered and
indexed counts, invalid/skipped count, last attempt/success, stale time, and
safe diagnostic reasons without printing workflow scripts. Logs must not emit
scripts, args, prompts, or results.

Roll out behind workflow indexing with the same local-data disclosure and
retention defaults as Pi transcript indexing. First deploy the external manifest
writer, verify owner-only files and manifests for several real workspaces, then
enable the AgentWeave adapter. Verify exact-workspace run and definition
discovery, cross-adapter deduplication, and purge/reindex behavior before
advertising the Pi search filter. Track malformed-layout and manifest-validation
diagnostics, not query contents.

## Acceptance criteria

1. A valid manifest-backed project saved workflow is discoverable and readable
   only from its exact canonical workspace and has stable citeable evidence.
2. A hashed project directory lacking valid attribution never appears as a
   guessed project workflow.
3. A user workflow is not returned unless `include_user_workflows` is explicit;
   enabling it never exposes arbitrary unrelated global artifacts.
4. Malformed/transient inputs, sidecars, and path escapes neither crash sync nor
   delete known-good evidence; status makes the condition visible.
5. Search remains bounded, lexical, deterministic, provenance-bearing, and
   client-model-free; no retrieval action executes workflow code.
6. Existing AgentWeave clients/configurations/indexes continue to work, and all
   Go race tests plus AgentWeave JS build/tests pass.
7. Manifest-proven workflow runs—including their persisted prompts, histories,
   results, logs, arguments, and subagent transcript material—are
   searchable/readable as extensions of the main transcript, have stable
   citeable evidence and run provenance, and do not duplicate already indexed
   Pi session records.

## Open questions and risks

- **Manifest ownership:** confirm which pi-dynamic-workflows write path can
  guarantee manifest update whenever project saved state is created/changed.
  A stale manifest must be diagnosable without weakening containment checks.
- **Canonicalization parity:** AgentWeave currently cleans absolute paths; the
  external package's resolved cwd/symlink policy must be made identical or a
  versioned translation contract must be documented and tested.
- **Script exposure policy:** saved scripts can contain credentials despite
  local-only storage. Decide whether initial indexing should be metadata-only
  by default, whether script reads need a separate opt-in, and how deny rules
  apply to records outside artifact roots.
- **Metadata parsing:** a hand-written literal parser needs an explicit grammar
  and adversarial fixtures. Until then, do not imply parsed phases or
  capabilities.
- **User/global UX:** decide whether user workflows should be included by a
  separate finder default after a user-visible consent control, while generic
  evidence search remains strict.
- **Built-in contract:** descriptor args/capabilities and package version must
  come from a stable external API; generated source is intentionally not a
  compatible index contract.
- **Run parsing and identity:** confirm the persisted run/session schemas,
  atomic-write lifecycle, and how a shared Pi session is identified so the
  adapter can preserve all transcript material while producing one canonical
  evidence source rather than duplicates.
- **Retention parity:** document precisely which existing Pi transcript
  retention/purge settings apply to workflow-run paths, and ensure a transcript
  purge removes derived workflow-run metadata and chunks consistently.

# Plan: LM Studio local-model thinking-control inventory and wrapper generator

## Goal

Create a script that recursively inventories local `.gguf` files and, for
**explicitly approved LM Studio model-family profiles**, creates an adjacent or
separate LM Studio `model.yaml` wrapper that exposes a thinking toggle for
models that already support it. The script must support exclusion lists,
dry-run mode, deterministic reports, and validation.

This replaces the initial premise of rewriting GGUF files to “claim thinking
support.” Current first-party LM Studio documentation does not establish a GGUF
metadata key that turns arbitrary local models into thinking-capable LM Studio
models. The documented LM Studio mechanism is a virtual `model.yaml` model:
its `metadataOverrides.reasoning: true` is presentation metadata, while a
`customFields` boolean can set a named variable in an existing Jinja prompt
template. That template variable and the model’s emitted thought protocol must
already be genuinely supported by the model.

**The script must never label a model as thinking-capable merely because its
description says so, its filename contains “reasoning,” or a metadata flag was
added.** It must report “unverified” until the exact profile’s preconditions
and LM Studio acceptance tests pass.

## Research basis and version gate

The implementation and its profile registry are based on these primary
sources, revalidated before each release:

- [LM Studio `model.yaml` documentation](https://lmstudio.ai/docs/app/modelyaml):
  documents `metadataOverrides.reasoning`, `customFields`, and the
  `setJinjaVariable` effect; explicitly says metadata overrides do not make
  functional changes.
- [LM Studio Prompt Template documentation](https://lmstudio.ai/docs/app/advanced/prompt-template):
  says LM Studio automatically configures prompt templates from model-file
  metadata and allows model-specific Jinja overrides.
- [model.yaml draft specification](https://github.com/modelyaml/modelyaml/blob/main/README.md):
  documents `enableThinking` / `setJinjaVariable: enable_thinking` as an
  example.
- [LM Studio Responses API documentation](https://lmstudio.ai/docs/developer/openai-compat/responses):
  documents a request-level `reasoning.effort` parameter; it is not evidence
  that a GGUF can honor a thinking control.

Pin the tool’s supported LM Studio release range in its `--version` output and
profile registry. Treat the open upstream report that compatible side-loaded
GGUFs may lack an automatic switch as a regression case, not as authoritative
platform behavior: [issue #1759](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1759).

## Non-goals and safety boundary

- Do not alter a GGUF merely to assert `reasoning`, `thinking`, or
  `enableThinking` metadata.
- Do not alter tensors, tokenizer vocabulary, quantization, model weights, or
  user prompts by default.
- Do not create or guess a Jinja template for an unknown model family.
- Do not claim that enabling a switch gives a model reasoning ability, hidden
  chain-of-thought, or correct `<think>`/other thought delimiters.
- Do not automatically register/import a wrapper into LM Studio, edit LM
  Studio’s private application state, or overwrite existing model wrappers.
- Do not follow directory symlinks outside the operator-provided root.

An optional future **GGUF migration** mode is allowed only after LM Studio
publishes an exact, versioned local-GGUF metadata contract. It must be a new
profile, disabled by default, with its own compatibility tests. It is not part
of v1.

## Proposed command interface

Implement a standalone Python command, for example
`lmstudio-thinking-wrapper.py`, using a pinned GGUF parser. It reads GGUF
metadata but writes only generated wrapper/report files in v1.

```sh
# Inventory only. Nothing is written.
python3 lmstudio-thinking-wrapper.py /models \
  --exclude 'archive/**' \
  --exclude-file .lmstudio-thinking-exclude \
  --dry-run --report lmstudio-thinking-report.json

# Generate wrappers only for known compatible model-family profiles.
python3 lmstudio-thinking-wrapper.py /models \
  --profile qwen3-enable-thinking-v1 \
  --exclude '**/*-F16.gguf' \
  --wrapper-root /models/lmstudio-wrappers \
  --generate --yes
```

| Flag | Meaning |
| --- | --- |
| `ROOT` | Required directory scanned recursively; resolve once to an absolute path. |
| `--profile NAME` | Repeatable approved family-specific wrapper profile. Omitted means inventory/classification only. |
| `--exclude PATTERN` | Repeatable root-relative glob for files/directories. Directory matches prune their subtree. |
| `--exclude-file PATH` | Repeatable exclusion-list file. |
| `--include PATTERN` | Optional root-relative allow-list; exclusions take precedence. |
| `--dry-run` | Parse, classify, and show planned wrappers without writing. Default unless `--generate` is passed. |
| `--generate` | Generate wrappers after successful preflight; requires `--yes` in a noninteractive run. |
| `--wrapper-root PATH` | Required in generate mode. Dedicated output directory outside the scanned input tree is recommended. |
| `--on-existing error|skip|replace` | Existing generated wrapper policy; default `error`. `replace` only accepts a tool-owned wrapper with a matching manifest. |
| `--report PATH` | Write aggregate JSON report; otherwise emit JSON Lines to stdout. |
| `--fail-fast` | Stop at first parse/profile/output failure. |
| `--verify-gguf` | Reopen and validate GGUF metadata parsing. Enabled by default. |
| `--lmstudio-version VERSION` | Required in generate mode; must match the profile’s tested compatibility range. |
| `--yes` | Explicitly confirms generation after the preflight summary. |

No `--force-thinking`, arbitrary `--metadata KEY=VALUE`, arbitrary
`--template`, or generic `--reasoning-key` option is permitted. Those options
would turn a narrow compatibility tool into a metadata misrepresentation tool.

## Exclusion-list format and discovery

The exclusion format is intentionally small and deterministic:

```text
# Root-relative globs; comments and blank lines are ignored.
archive/**
**/*-F16.gguf
experimental/private-model.gguf
```

1. Convert each candidate path to normalized `/`-separated path relative to
   `ROOT`; do not evaluate patterns from the working directory.
2. `**` may cross directories and `*` does not cross `/`.
3. A matching directory prunes all descendants; a matching file skips that
   file. All exclusions are unioned; there is no order-dependent `!` negation
   in v1.
4. Exclusions override includes. In verbose JSON output, retain the source
   list/flag and matching rule for every skipped `.gguf`.
5. An invalid/unreadable list is a CLI error, not an empty list.
6. Walk directories in sorted normalized-path order; inspect regular files with
   a case-insensitive final `.gguf` extension only. Skip symlinks and record
   them without following them.

## LM Studio wrapper design

### Generated artifact

For every approved candidate, generate one isolated virtual-model directory
under `--wrapper-root`, for example:

```text
lmstudio-wrappers/
  qwen3-8b-q4_k_m--a1b2c3d4/
    model.yaml
    manifest.json
```

Use a stable name derived from the source basename plus a short source hash so
different quantizations never collide. Do not place `model.yaml` next to a
sideloaded GGUF unless LM Studio documents that location as supported for the
pinned version. The documented import/publish flow, not private app data, is
the supported handoff.

`model.yaml` is derived solely from a reviewed profile. Its concrete `base`
source/reference must follow the then-current documented LM Studio model.yaml
import/virtual-model workflow. Resolve that integration during an initial
implementation spike; do not invent a file-path `base` syntax not documented
by LM Studio. If a local arbitrary GGUF cannot be represented through the
supported model.yaml workflow, stop after inventory and report that limitation
rather than falling back to GGUF mutation.

### Profile requirements

Profiles are declarative, source-controlled, individually versioned, and must
include:

- profile name and tested LM Studio version range;
- exact model-family/architecture predicates and an optional approved model-ID
  allow-list; never select solely by filename;
- exact expected `tokenizer.chat_template` signature(s), parsed as Jinja where
  possible;
- the known supported variable, e.g. `enable_thinking` for a documented Qwen
  family profile;
- expected output reasoning delimiters/protocol and a probe prompt;
- exact wrapper content, including:

  ```yaml
  metadataOverrides:
    reasoning: true
  customFields:
    - key: enableThinking
      displayName: Enable Thinking
      description: Controls whether this already-compatible model thinks before replying
      type: boolean
      defaultValue: true
      effects:
        - type: setJinjaVariable
          variable: enable_thinking
  ```

- validation rules for both enabled and disabled modes;
- source citations and a fixture hash/version.

A profile must reject a file as `unsupported` when the architecture,
template signature, or model-family identity is not recognized. It must not
rewrite the GGUF’s `tokenizer.chat_template`; a mismatched/missing template is
a rejection, not a template-injection opportunity.

### Initial implementation sequence

1. Build the inventory parser and report format with **no profiles capable of
   generation**.
2. In LM Studio’s installed host version, manually take one known-good official
   wrapper/model pair and record the exported/imported artifact layout and
   local-model binding mechanism. Use only documented UI/CLI/model.yaml flows.
3. Implement one narrow profile only after confirming its source GGUF contains
   the profile’s expected template and that LM Studio’s generated control
   actually sets the named Jinja variable.
4. Add a second fixture from the same family but incompatible template to prove
   rejection works.
5. Expand profiles only on independently tested family/version pairs.

## Classification and execution flow

1. Parse flags, resolve root/output paths, load exclusions and selected
   profiles, and reject an output path nested in `ROOT` unless explicitly
   excluded (to prevent generated artifacts being rediscovered).
2. Walk deterministically; build a manifest for every GGUF: relative path,
   size, mtime, source SHA-256, metadata summary, selected/excluded decision,
   and any parser error.
3. For each included valid GGUF, inspect—not mutate—`general.architecture`,
   model identifiers, `tokenizer.chat_template`, and profile-required metadata.
4. Classify each file as `excluded`, `invalid`, `unknown`, `unsupported`,
   `compatible-unverified`, `already-generated`, `planned`, `generated`, or
   `failed`. “Compatible” means only that a profile’s static preconditions
   match; it is not a behavioral claim until runtime verification.
5. Print the aggregate plan and require `--generate --yes` for writes.
6. Generate wrapper and manifest through a temporary directory, validate YAML
   against the profile schema, fsync as supported, and atomically rename the
   complete wrapper directory into `--wrapper-root`.
7. Never modify the source GGUF. Re-stat/re-hash it before recording success;
   report a source-change race as failure.
8. Emit per-file JSON records and an aggregate report. Exit `0` when all
   selected files were successfully inventoried/generated or intentionally
   skipped; `1` for candidate failures/unsupported conflicts; `2` for CLI or
   preflight failures; `130` for interruption.

## Verification in LM Studio

Static GGUF/template inspection is necessary but insufficient. A generated
wrapper becomes `runtime-verified` only when all of the following have been
recorded against the pinned LM Studio version:

1. Import/load it through the documented LM Studio workflow, with no private
   state edits.
2. LM Studio displays the profile’s `Enable Thinking` custom field.
3. With it enabled, a profile probe causes the model to emit the profile’s
   expected thinking delimiters or documented reasoning channel; the UI keeps
   those tokens in a thinking/reasoning display as expected.
4. With it disabled, the rendered prompt differs as expected and the model
   does not enter that enabled thought protocol (subject to the model’s known
   behavior).
5. An API check, where applicable, loads the model and sends the documented
   `/v1/responses` request. Record response fields/events separately from the
   UI test; `reasoning: {"effort":"low"}` is request-level behavior and not
   proof of the UI wrapper contract.
6. The original GGUF hash before/after is identical, because it was never
   rewritten.

A model that reasons aloud as ordinary prose but does not emit the profile’s
protocol must be reported as `incompatible-with-LM-Studio-thinking-display`,
not falsely repaired through metadata.

## Reporting

Use JSON Lines for per-file results and an optional aggregate report. A record
must include path, source hash, classification, exclusion rationale, matched
profile/version, template predicate result, wrapper directory/manifest hash,
LM Studio version gate, static checks, and runtime verification state.

Example:

```json
{
  "path": "qwen/Qwen3-8B-Q4_K_M.gguf",
  "status": "planned",
  "source_sha256": "…",
  "profile": "qwen3-enable-thinking-v1",
  "template_check": "passed",
  "wrapper_path": "lmstudio-wrappers/qwen3-8b-q4_k_m--a1b2c3d4",
  "runtime_verification": "pending"
}
```

Do not report `thinking_supported: true`; use the evidence-bearing states
above. Console output may truncate templates; the report should record a hash
and predicate evidence instead of entire possibly large prompt templates.

## Test plan

### Unit tests

- Root-relative glob matching, includes/exclusions, comments/blank lines,
  malformed list errors, sorted traversal, directory pruning, and symlink
  skipping.
- GGUF parsing success/failure and extraction of only required metadata.
- Profile matching: architecture/model identity/template predicate required;
  filename-only matches rejected.
- Wrapper rendering: schema, `reasoning: true`, `enableThinking`, boolean
  type, exact Jinja-variable effect, stable naming, manifest hashes, no source
  file write.
- Existing-wrapper policies, atomic output behavior, cleanup, source-change
  races, `--yes` enforcement, and exit codes.

### Fixtures and integration tests

- Small generated GGUF fixtures: known good template, absent template,
  wrong variable, unsupported architecture, corrupt file, and excluded file.
- A regression fixture representing a model that produces plain prose CoT
  without the profile’s delimiters: it must not reach `runtime-verified`.
- A real known-good family/profile fixture (or a locally documented test model)
  run in the pinned LM Studio version: verify visible control, enabled/disabled
  prompt behavior, thought-channel behavior, and report transition to
  `runtime-verified`.
- Verify the original GGUF SHA-256 is unchanged for every invocation and that
  a rerun is idempotent (`already-generated` or `planned`, depending on output
  policy).

## Documentation and acceptance criteria

Document installation, the LM Studio version gate, supported profiles,
exclusion syntax, the output/import procedure, restoration/deletion of a
wrapper, report/exit codes, and these central limitations: metadata is not
reasoning; `metadataOverrides.reasoning` is presentation-only; the template
must already honor the variable; and the model must actually produce a
recognized protocol.

The v1 implementation is acceptable only when:

- dry-run inventory recursively classifies GGUFs and explains exclusions;
- exclusion rules prevent listed files/subtrees from even entering wrapper
  generation;
- no source GGUF is changed, byte-for-byte;
- unsupported or description-only “reasoning” models cannot receive a wrapper;
- one profile produces a valid documented LM Studio wrapper for a known-good
  model and the pinned host LM Studio version exposes/uses its control;
- every generated wrapper has a manifest and can be reproduced from the same
  source/profile; and
- rerunning is deterministic and idempotent.
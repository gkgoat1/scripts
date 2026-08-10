# Plan: evidence-gated LM Studio local reasoning metadata enabler

## Goal

Build a script that runs **against the host’s LM Studio data directory** and
enables the existing LM Studio `Enable Thinking` UI control for local,
Hugging-Face-downloaded GGUFs whose actual GGUF architecture is `qwen35` or
`gemma4` and whose existing Jinja template supports the control.

The VM has a shared VirtIOFS mount of the host’s LM Studio directory at:

```text
/Volumes/My Shared Files/.lmstudio
```

The directory appears writable from the VM, but it is the host application’s
live state. The script must therefore default to **evidence-only planning**.
It must not edit anything until it has generated and passed a local evidence
bundle proving the selected LM Studio release’s internal data model and a
single-model canary experiment have both succeeded.

The intended edit is **LM Studio metadata/state**, not GGUF rewriting. GGUF
weights and metadata are never modified by this tool.

## Local evidence already observed

The mounted directory provides unusually strong, local evidence that this is
feasible, while also showing why it must be version-gated:

### 1. Local models and downloaded provenance

`/Volumes/My Shared Files/.lmstudio/.internal/model-data.json` is a serialized
map of model IDs to source records. It contains both raw Hugging Face GGUF
records and LM Studio Hub virtual-model records. Example relationships currently
observed:

```text
google/gemma-4-26b-a4b
  source: hub, transitive: false

lmstudio-community/gemma-4-26B-A4B-it-GGUF/gemma-4-26B-A4B-it-Q4_K_M.gguf
  source: huggingface, transitive: true
```

It also contains non-transitive direct Hugging Face local models, including
Qwen 3.5 and Gemma 4 fine-tunes. This establishes that LM Studio tracks the
Hub-to-Hugging-Face association in local state; it is not represented only by
the GGUF filename.

### 2. Installed official virtual-model definitions

The host has downloaded catalog artifacts in
`hub/models/<owner>/<name>/model.yaml`. The local file:

```text
hub/models/google/gemma-4-26b-a4b/model.yaml
```

contains a full installed example for a Gemma 4 GGUF, including:

```yaml
base:
  - key: lmstudio-community/gemma-4-26b-a4b-it-gguf
    sources:
      - type: huggingface
        user: lmstudio-community
        repo: gemma-4-26B-A4B-it-GGUF
config:
  operation:
    fields:
      - key: llm.prediction.reasoning.parsing
        value:
          enabled: true
          startString: "<|channel>thought"
          endString: "<channel|>"
customFields:
  - key: enableThinking
    type: boolean
    defaultValue: true
    effects:
      - type: setJinjaVariable
        variable: enable_thinking
metadataOverrides:
  architectures: [gemma4]
  reasoning: true
```

The installed Qwen catalog definition:

```text
hub/models/qwen/qwen3.6-27b/model.yaml
```

contains the corresponding `qwen35` metadata override, an
`enableThinking` field that sets `enable_thinking`, and a Jinja template that
renders `<think>` when thinking is enabled and an empty `<think>...</think>`
block when disabled. It also defines `preserveThinking`; that extra field is
out of scope for v1.

These are host-local artifacts from the installed LM Studio version and are
stronger implementation evidence than a copied web snippet. They must still be
revalidated before every write because they are an internal, version-dependent
storage format.

### 3. Cached model facts identify eligible candidates

`/Volumes/My Shared Files/.lmstudio/.internal/gguf-metadata-cache.json` is a
cache keyed by the host’s absolute GGUF path. It includes cached source mtime,
size, architecture (`arch`), model name, and `chatTemplate`. The current cache
shows both target architectures and enables a zero-GGUF-write eligibility scan.

Observed examples show that architecture alone is insufficient:

- Multiple local Qwen 3.5/Qwen 3.6 GGUFs use a template containing
  `enable_thinking` and can be candidates.
- At least one local Qwen 3.5 model has `arch: qwen35` **but its cached
  template does not contain `enable_thinking`**. It must be excluded from this
  tool’s automatic change set.
- Several Gemma 4 GGUFs have `enable_thinking` and expected thought markers;
  their templates differ materially, so the profile must validate the control
  semantics rather than treating every `gemma4` template as identical.

### 4. The internal model index is observable

`.internal/model-index-cache.json` exposes the currently indexed local GGUFs,
including the GGUF architecture/template. It is useful after a canary for
observing whether the application recognized a new association. It is a cache,
not an authoritative write target.

## Boundary: supported artifacts vs. deliberate internal integration

LM Studio publicly documents `model.yaml`, `customFields`,
`setJinjaVariable`, and the local `lms import` flow. It does **not** document
the private `.internal/model-data.json` serialization or a public command for
attaching a virtual-model wrapper to an already downloaded direct Hugging Face
entry.

Therefore v1 has two explicitly separated modes:

1. **`audit` (safe/default):** read only local state and GGUF metadata cache;
   create a signed/hash-bound evidence report and a proposed change manifest.
2. **`canary` / `apply` (opt-in experimental):** make the minimally necessary
   edits to the **documented virtual-model artifacts plus the locally observed
   association record**, only after a stop-the-app backup and after evidence
   gates pass.

The script must describe this as an **unsupported internal integration**, not
as an LM Studio public API. A future LM Studio release may change the files,
serialized map encoding, identifier semantics, caching, or reload behavior.

## Script location and execution model

Develop one portable Python script in this repository, for example
`lmstudio_enable_thinking.py`. It must receive an explicit LM Studio root:

```sh
# From this VM, against the mounted host directory; audit only.
python3 lmstudio_enable_thinking.py audit \
  --lmstudio-root '/Volumes/My Shared Files/.lmstudio' \
  --report ./lmstudio-thinking-audit.json
```

For actual modification, require that LM Studio has been fully quit on the
host, then invoke with an explicit danger acknowledgement:

```sh
python3 lmstudio_enable_thinking.py canary \
  --lmstudio-root '/Volumes/My Shared Files/.lmstudio' \
  --model-id 'TeichAI/gemma-4-26B-A4B-it-Claude-Opus-Distill-v2-GGUF/gemma-4-26B-A4B-it-Claude-Opus-Distill.q4_k_m.gguf' \
  --profile gemma4-enable-thinking-v1 \
  --i-understand-this-edits-lmstudio-internals
```

No write is allowed unless all of the following are true:

- the caller explicitly chooses `canary` or `apply`;
- LM Studio’s process is confirmed stopped **on the host** (a VM-only process
  check is insufficient; require operator-provided host confirmation or a
  host-side checker); and
- `--i-understand-this-edits-lmstudio-internals` is present.

`apply` is disabled until a prior canary evidence bundle is supplied and passes
hash/version/behavior checks.

## Evidence gate

Before any change, the script must produce an immutable JSON evidence bundle
containing source hashes, parse results, and all discovered internal schema
facts. `canary`/`apply` must consume that bundle and reject it if anything
changed.

### Preconditions checked by `audit`

1. Resolve `--lmstudio-root` and confirm expected descendants exist:
   `.internal/model-data.json`, `.internal/gguf-metadata-cache.json`,
   `.internal/model-index-cache.json`, and `hub/models`.
2. Record filesystem identity, permissions, mount type, symlink resolution, and
   LM Studio application/CLI version when observable. Refuse unknown future
   schema versions rather than assuming the current format.
3. Parse the tagged serialization in `model-data.json` and preserve it
   losslessly. Validate that it is the observed map/metadata representation;
   never run generic formatting or reserialize unrelated entries.
4. Parse every installed `hub/models/**/model.yaml` with a safe YAML parser and
   identify known-good local examples for `qwen35` and `gemma4`.
5. Parse cache records; then, for every selected GGUF, re-read its lightweight
   header/metadata with a pinned GGUF reader. A stale cache alone cannot
   authorize a change.
6. Require, for each candidate:
   - source identity exists in `model-data.json` as a Hugging Face record;
   - GGUF architecture equals the selected profile’s architecture;
   - template has the expected `enable_thinking` conditional, not merely the
     substring somewhere in a comment or unrelated macro;
   - template output markers match the profile’s expected protocol;
   - model has not already been associated with a compatible reasoning wrapper;
   - no split-GGUF/multipart ambiguity exists; and
   - source file path, size, mtime, and SHA-256 match report evidence.
7. Record exclusions and failures. Exclusion patterns are root-relative globs,
   support `**`, prune excluded directories, and always override included
   configured candidates.

### Canary proof requirements

The script must reject `apply` until a canary report records all of the
following **after LM Studio is restarted on the host**:

1. The new/modified local virtual model appears in LM Studio independently of
   cache artifacts.
2. It resolves to the exact target raw Hugging Face GGUF, verified by source
   identifier/path/hash—not a downloaded duplicate.
3. The UI exposes `Enable Thinking`.
4. The control causes `enable_thinking` to reach the target’s Jinja template.
5. With thinking enabled, a family-specific probe emits/uses the expected
   thought protocol; with it disabled, the family-specific template emits the
   expected non-thinking prompt behavior in a fresh conversation.
6. The raw GGUF SHA-256 remains unchanged.
7. Restarting LM Studio again retains the association and control.

The report must include the host LM Studio version, selected model identifier,
wrapper and metadata-state pre/post hashes, screenshots or operator attestation
references, and probe results. “The switch is visible” alone is insufficient.

## Eligible profile definitions

Profiles are source-controlled Python data or external TOML/YAML definitions
with their full rules versioned and hashed in the evidence bundle.

### Qwen: `qwen35-enable-thinking-v1`

Required facts:

- `metadata.gguf.arch == "qwen35"`.
- Template has a real conditional using `enable_thinking` in its generation
  path, equivalent to the locally installed `qwen/qwen3.6-27b` reference.
- The output protocol is the profile-approved Qwen `<think>` behavior.

The wrapper’s custom field is exactly:

```yaml
customFields:
  - key: enableThinking
    displayName: Enable Thinking
    description: Controls whether the model will think before replying
    type: boolean
    defaultValue: true
    effects:
      - type: setJinjaVariable
        variable: enable_thinking
```

Do not add `preserveThinking` in v1, even though the installed Qwen 3.6
reference has it.

### Gemma: `gemma4-enable-thinking-v1`

Required facts:

- `metadata.gguf.arch == "gemma4"`.
- The existing template genuinely responds to `enable_thinking` in its
  generation path.
- The template’s output protocol matches one of the reviewed Gemma 4 template
  variants. The exact parsing configuration must be derived from the local
  installed Gemma reference and tested in the canary—not guessed from a
  different fine-tune.

The custom field is the same exact `enableThinking`/
`setJinjaVariable: enable_thinking` field. The profile may add:

```yaml
config:
  operation:
    fields:
      - key: llm.prediction.reasoning.parsing
        value:
          enabled: true
          startString: "<|channel>thought"
          endString: "<channel|>"
```

only for templates whose canary output proves that exact channel. Other Gemma
4 variants may use different markers and must be separate profiles. A
wrong parser config can hide, corrupt, or misclassify the model’s output.

## Internal canary change hypothesis

The implementation spike must test—and record—not assume this hypothesis:

1. A virtual-model artifact lives under
   `hub/models/<owner>/<model>/model.yaml` with its `manifest.json`.
2. The virtual artifact’s `base[].key` identifies a concrete Hugging Face
   source key.
3. `model-data.json` maps the new virtual key to a Hub source and the existing
   concrete entry to a Hugging Face source with `transitive: true`, matching
   the installed catalog relationship.
4. On restart, LM Studio reindexes this association and exposes the wrapper’s
   custom fields when loading the virtual model.

This is a **hypothesis**, supported by the observed host state but not an
external public contract. The canary must apply it only to a new private
wrapper ID such as:

```text
local-thinking/gemma4-26b-canary
```

It must never overwrite `google/gemma-4-26b-a4b`, the original concrete model
entry, or any existing Hub artifact. If LM Studio rejects the association,
shows a duplicate/downloader prompt, resolves the wrong model, or fails to
reload cleanly, the script rolls back exactly its own files/entries from a
transaction backup and stops.

## Transaction and backup design

Because the target is live application state, an apply operation is a small
transaction:

1. Require LM Studio stopped and re-check every evidence hash.
2. Create a timestamped backup directory outside `.lmstudio`, containing byte
   copies and SHA-256s of **only** files to change: `model-data.json`, the
   tool-created hub artifact paths, and any tool-proven necessary cache/index
   files. Never back up or rewrite GGUFs.
3. Create wrapper directories under a temporary sibling directory; validate
   `model.yaml` and `manifest.json` against the locally observed reference
   shape.
4. Update `model-data.json` using a parser/encoder that preserves the tagged
   representation and every unrelated map item byte-for-byte where possible.
   If byte-preserving update cannot be implemented, fail closed; do not run a
   whole-file pretty printer over proprietary state.
5. Fsync temporary data, atomically rename files/directories, and fsync parent
   directories where supported.
6. Write a transaction manifest with old/new hashes and exact changed JSON map
   entries.
7. Ask the operator to start LM Studio and perform the canary verification.
8. `rollback` restores only transaction-manifest-listed paths after LM Studio
   is stopped. It never deletes arbitrary `hub/models` contents.

## Commands

```sh
# Default, no writes: enumerate candidates and produce evidence.
lmstudio-enable-thinking audit \
  --lmstudio-root '/Volumes/My Shared Files/.lmstudio' \
  --exclude 'archive/**' \
  --exclude-file ./lmstudio-thinking.exclude \
  --report ./evidence.json

# Experimental one-model only. Requires audit evidence and stopped host app.
lmstudio-enable-thinking canary \
  --lmstudio-root '/Volumes/My Shared Files/.lmstudio' \
  --evidence ./evidence.json \
  --model-id '…exact Hugging Face concrete model ID…' \
  --profile gemma4-enable-thinking-v1 \
  --backup-root ./lmstudio-thinking-backups \
  --i-understand-this-edits-lmstudio-internals

# Only after a successful recorded canary; model IDs are explicit or selected
# from a reviewed evidence report—not all architecture matches by default.
lmstudio-enable-thinking apply \
  --lmstudio-root '/Volumes/My Shared Files/.lmstudio' \
  --evidence ./approved-evidence.json \
  --canary-proof ./canary-proof.json \
  --selection ./reviewed-model-ids.txt \
  --backup-root ./lmstudio-thinking-backups \
  --i-understand-this-edits-lmstudio-internals

# Restore one recorded transaction after LM Studio is stopped.
lmstudio-enable-thinking rollback \
  --transaction ./lmstudio-thinking-backups/<transaction>/manifest.json \
  --i-understand-this-edits-lmstudio-internals
```

There is no `--all-qwen35-and-gemma4` switch in v1. Bulk selection must be an
explicit reviewed list produced by audit and constrained by its model IDs,
source hashes, profiles, and template hashes.

## Test plan

### Offline/unit

- Decode/encode fixture copies of the observed `model-data.json` tagged map;
  prove only intended entries differ.
- Validate model.yaml/manifest rendering against redacted reference fixtures
  from the host’s installed Qwen and Gemma artifacts.
- Test schema/version rejection, path traversal, symlink containment,
  exclusions, stale cache, source hash/mtime race, bad template, wrong arch,
  duplicate wrapper ID, and existing association handling.
- Test all transaction failure points and exact rollback restoration.
- Test that every GGUF fixture hash stays unchanged.

### Host canary

- Choose one verified `gemma4` candidate with a known profile/template match,
  create `local-thinking/gemma4-…-canary`, restart LM Studio, and run the full
  enabled/disabled behavior test.
- Repeat separately for one verified `qwen35` candidate.
- Update profile allow-lists/template hashes based on actual canary results;
  reject all other candidates until they match a verified profile.
- Only then test a small second batch before producing any broader reviewed
  selection file.

## Acceptance criteria

- The audit report demonstrates the host’s actual candidate architecture,
  template behavior, provenance, and exact intended state changes before any
  write.
- No GGUF file is ever changed.
- Canary writes only a new namespaced virtual-model artifact and its explicit
  association record, never overwriting vendor/Hub models.
- A successful canary proves LM Studio’s visible toggle and enabled/disabled
  runtime behavior after restart; failure rolls back cleanly.
- Bulk apply requires an approved canary proof plus an explicit hash-bound
  selection list, and excludes any model that does not demonstrably support
  the appropriate family/template profile.
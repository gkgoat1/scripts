# Plan: host-side LM Studio reasoning metadata enabler for local Qwen 3.5 and Gemma 4 GGUFs

## Goal

Create a **host-side** script that makes LM Studio expose the documented
`Enable Thinking` control for eligible local GGUF models in the `qwen35` and
`gemma4` families. The models are downloaded from Hugging Face and their model
families are already correctly represented in the GGUF metadata, but the local
LM Studio entries lack the LM Studio virtual-model metadata/custom field.

The script will not claim to add reasoning to arbitrary models. For the two
specified families, LM Studio’s own catalog definitions demonstrate the exact
metadata contract to reproduce:

```yaml
metadataOverrides:
  architectures:
    - qwen35                 # or gemma4
  reasoning: true
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

This custom field sets `enable_thinking` in the existing Jinja prompt template;
it does not change model weights. The underlying template must already accept
that variable and the model must already implement the expected thought
protocol.

## Crucial VM/host architecture

**The final script must execute on the machine where LM Studio runs: the host,
not this VM.** It needs access to the host’s `lms` CLI, its LM Studio model
registry, and the physical directory that is symlinked into the host’s
`~/.lmstudio/models`.

The VM may be used to develop/test the script and to inspect a shared model
mount, but it cannot make LM Studio discover metadata merely by writing a
`model.yaml` where the host application cannot see or import it. Choose one
supported execution transport:

1. **Preferred — host checkout:** keep this repository or a small exported
   script/config package on the host and run it directly there.
2. **Acceptable — explicit SSH runner:** run `ssh host 'python3 ...'` only
   after the script has verified the host’s paths, LM Studio CLI version, and
   commands. The model filesystem must be visible on that host.
3. **Acceptable — shared folder plus host command:** the VM writes only a
   reviewable manifest/config to a shared directory; a host-side launcher
   reads it and invokes `lms` locally.

Do not attempt to write host `~/.lmstudio` state from the VM over an assumed
path mapping, and do not edit LM Studio’s private database/config files.

## Evidence and confirmed family contract

LM Studio documents the `model.yaml` feature as follows:

- `metadataOverrides.reasoning: true` identifies reasoning capability for
  model presentation; LM Studio explicitly says metadata overrides do **not**
  make functional changes.
- A boolean custom field with a `setJinjaVariable` effect can expose an
  `Enable Thinking` toggle. The existing Jinja template must use the named
  variable.

Sources:

- [LM Studio `model.yaml` documentation](https://lmstudio.ai/docs/app/modelyaml)
- [model.yaml specification](https://github.com/modelyaml/modelyaml/blob/main/README.md)
- [LM Studio Prompt Template documentation](https://lmstudio.ai/docs/app/advanced/prompt-template)

LM Studio’s own model-catalog pages currently show this exact custom field and
variable for both target architectures:

| Family | Catalog architecture | Toggle variable | Catalog reference |
| --- | --- | --- | --- |
| Qwen 3.5 | `qwen35` | `enable_thinking` | [qwen/qwen3.5-9b](https://lmstudio.ai/models/qwen/qwen3.5-9b) |
| Gemma 4 | `gemma4` | `enable_thinking` | [google/gemma-4-12b](https://lmstudio.ai/models/google/gemma-4-12b) |

The Gemma catalog definition additionally includes
`llm.prediction.reasoning.parsing` in its defaults. Treat any such inference
setting as profile-specific, version-pinned behavior; do not blindly apply
sampling or parsing defaults from a web page to every local quantization.

## What must be solved before implementation

LM Studio documents `lms import` for a local file (including `--symbolic-link`
and `--user-repo`) and documents `model.yaml` virtual models backed by Hub
sources. It does **not**, in the documentation reviewed here, specify how an
arbitrary existing local GGUF becomes the `base` of a local `model.yaml`
virtual model.

The implementation must solve this through a single host-side spike, not a
guess:

1. Select one local Qwen 3.5 GGUF and import/re-import it on the host through
   the documented command, preserving source bytes:

   ```sh
   lms import /absolute/path/to/model.gguf \
     --symbolic-link --user-repo local-thinking/qwen35-pilot
   ```

   `--symbolic-link` is documented by LM Studio and avoids a weight copy. If
   the file is already imported, use `--dry-run` first and do not create
   duplicates.
2. Clone the official virtual-model definition on the host:

   ```sh
   lms clone qwen/qwen3.5-9b
   ```

   This yields an inspectable `model.yaml` without downloading weights.
3. Determine through documented `lms`/LM Studio UI behavior whether the cloned
   model can point at the import’s concrete local key. Record the **actual
   supported base/reference syntax and import step**. Do not invent a `file:`
   source or patch LM Studio’s internal records.
4. Modify only a copy under a private artifact ID, e.g.
   `local-thinking/qwen35-9b-pilot`, preserving the catalog’s custom field and
   substituting only the verified local base binding.
5. Import/use it through the documented host workflow and confirm that the
   toggle appears and affects the existing prompt template. Repeat for one
   Gemma 4 model.

**Stop condition:** If LM Studio cannot bind a local imported model to a local
virtual model through a supported visible CLI/UI workflow, this tool cannot
reliably auto-enable the custom field for arbitrary Hugging Face downloads. In
that case, retain the inventory and use LM Studio’s per-model Prompt Template
UI override as the supported workaround; do not mutate GGUFs or private host
state. The next research task would be an LM Studio feature request or official
local-wrapper support—not an unsafe filesystem hack.

## Script design

Name the host command `lmstudio-enable-thinking-local` and keep it
configuration-driven. Its normal operation is **plan first**; it writes only
after a reviewed plan and `--apply --yes`.

```sh
# On the LM Studio host: discover only.
lmstudio-enable-thinking-local \
  --models-root ~/.lmstudio/models \
  --config ~/.config/lmstudio-thinking/models.toml \
  --dry-run --report ~/lmstudio-thinking-plan.json

# On the host after the pilot binding works: create/update eligible wrappers.
lmstudio-enable-thinking-local \
  --models-root ~/.lmstudio/models \
  --config ~/.config/lmstudio-thinking/models.toml \
  --apply --yes --report ~/lmstudio-thinking-result.json
```

Required options:

| Option | Purpose |
| --- | --- |
| `--models-root PATH` | Host-visible model root. Resolve and report the symlink target. |
| `--config PATH` | Required profile/source mapping configuration; no filename-only automatic family assignment. |
| `--exclude PATTERN` | Repeatable root-relative glob exclusion. |
| `--exclude-file PATH` | Repeatable exclusion-list file. |
| `--dry-run` | Default. Build and validate the plan; do not invoke modifying `lms` commands. |
| `--apply --yes` | Both are required before any import/wrapper operation. |
| `--on-existing error|skip|replace` | Existing generated local-wrapper policy; default `error`. |
| `--report PATH` | JSON report with commands/results and source hashes. |
| `--lmstudio-version VERSION` | Required in apply mode; must satisfy tested profile range. |
| `--lms PATH` | Optional explicit host executable path; default finds `lms` on `PATH`. |

Never add arbitrary `--architecture`, `--jinja-variable`, `--metadata KEY=…`,
or `--base` flags. Source-controlled profiles and model mappings are the safety
boundary.

## Configuration format

The config declares the Hugging Face source identity and selected profile for
every model set. This avoids guessing an origin from a filename.

```toml
# ~/.config/lmstudio-thinking/models.toml

[defaults]
exclude = ["archive/**", "**/*-F16.gguf"]

[[model]]
path = "Qwen/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q4_K_M.gguf"
huggingface_repo = "Qwen/Qwen3.5-9B-GGUF"
local_user_repo = "local-thinking/qwen35-9b-q4-k-m"
profile = "qwen35-enable-thinking-v1"

[[model]]
path = "google/gemma-4-12b-it-gguf/gemma-4-12b-it-Q4_K_M.gguf"
huggingface_repo = "google/gemma-4-12B-it"
local_user_repo = "local-thinking/gemma4-12b-q4-k-m"
profile = "gemma4-enable-thinking-v1"
```

Validation rules:

- `path` is relative to the canonical `--models-root`, resolves to a regular
  `.gguf`, and cannot escape the root.
- `local_user_repo` is unique across the config and must use the expected
  `<user>/<repo>` form accepted by `lms import`.
- The source SHA-256, extracted `general.architecture`, and
  `tokenizer.chat_template` profile predicate are captured during planning and
  must match immediately before apply.
- A Qwen profile accepts only `qwen35`; a Gemma profile accepts only `gemma4`.
  Mismatches fail closed even if the filename/config says otherwise.
- A model must have a template that supports the profile’s
  `enable_thinking` contract. Missing/unrecognized templates are
  `unsupported`, not “fixed.”
- Exclusions override config entries; report the rule that excluded each file.

## Family profiles

### `qwen35-enable-thinking-v1`

Static requirements:

- GGUF `general.architecture == "qwen35"`.
- Existing template is a reviewed Qwen 3.5 template containing/using the
  `enable_thinking` Jinja variable in the expected control path.
- Profile is tested against the specified LM Studio version range and a known
  Qwen 3.5 fixture.

Generated virtual-model metadata includes:

```yaml
metadataOverrides:
  domain: llm
  architectures: [qwen35]
  compatibilityTypes: [gguf]
  reasoning: true
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

### `gemma4-enable-thinking-v1`

Static requirements:

- GGUF `general.architecture == "gemma4"`.
- Existing template is a reviewed Gemma 4 instruction-template variant that
  honors `enable_thinking`.
- The profile separately verifies LM Studio’s configured thought parsing for
  the Gemma version in use; a visible toggle alone is insufficient.

Use the same documented custom field with `enable_thinking`, and set
`metadataOverrides.architectures: [gemma4]` and `reasoning: true`. Add
`llm.prediction.reasoning.parsing` only after copying its exact, versioned
value from the official Gemma catalog definition during the host spike and
proving it behaves correctly for the local model.

Gemma 4 behavior is version-sensitive: an LM Studio bug report says a prior
release did not natively handle Gemma 4’s reasoning implementation, and another
reports a later regression where disabling it stopped working. These are
upstream issue reports rather than an API contract, so pin and test the host
version before bulk rollout:

- [issue #1743](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1743)
- [issue #2046](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/2046)

## Safe execution algorithm

1. **Host preflight:** verify `lms` is installed; read its version; resolve
   `--models-root` and print symlink/canonical path; ensure output/report paths
   are outside the recursive source root; refuse on unsupported version.
2. **Discover:** recursively scan regular GGUFs in sorted order without
   following nested symlinks; normalize paths root-relatively and apply
   includes/exclusions before parse.
3. **Inspect:** use a pinned read-only GGUF parser to obtain architecture and
   template hash/predicate evidence. Never write GGUFs.
4. **Plan:** validate every config entry and profile. Produce planned host `lms`
   import/clone/wrapper operations and source hashes. Any unknown, template
   mismatch, duplicate binding, already-loaded file, or unclear local-base
   association is a separate result—not silently skipped as success.
5. **Confirm:** dry-run is default. In apply mode, print counts and require
   both `--apply` and `--yes`.
6. **Apply sequentially:** for each approved entry, re-hash/re-stat source;
   execute only the host-side documented import/binding steps proven in the
   spike; generate a temporary wrapper/manifest; validate it; atomically place
   it in the chosen local wrapper workspace; invoke the documented import/use
   command.
7. **Validate:** verify the source hash is still equal, record generated hashes
   and host command output, and request/perform the runtime test below.
8. **Report:** JSON Lines plus aggregate result. Exit `0` only if every
   selected entry completes or was already proven equivalent; `1` if any model
   is unsupported/failed/conflicted; `2` for CLI/preflight/config errors.

## Runtime verification

Automated static checks can prove only the wrapper’s contents. For each family,
perform the first rollout model’s host LM Studio validation manually (or via a
supported host UI automation harness):

1. Load the generated local virtual model—not merely the raw GGUF entry.
2. Confirm LM Studio renders **Enable Thinking**.
3. Send the profile’s known probe in enabled mode and verify the expected
   thought channel/panel and final answer.
4. Disable the control, start a fresh chat, and verify the rendered prompt and
   generated output follow the family profile’s non-thinking behavior.
5. Restart LM Studio and repeat enough to prove the host binding persists.
6. Record result, screenshots/log IDs, host LM Studio version, model SHA-256,
   wrapper hash, and test timestamp in the report.

A model that emits plain prose reasoning but no protocol LM Studio recognizes
is `display-incompatible`; do not certify it merely because it appears to
reason. A model that lacks `enable_thinking` in its template remains
unsupported.

## Test plan

- Unit-test TOML/config validation, symlink-root resolution, root-relative glob
  exclusions, profile matching, architecture/template rejection, deterministic
  planning, and no-write dry runs.
- Use tiny valid GGUF fixtures with `qwen35`, `gemma4`, wrong architecture,
  missing template, wrong variable, and corrupted metadata. Assert no fixture
  bytes change.
- Mock the host `lms` executable to test exact invocation order, failure
  behavior, no duplicate imports, and cleanup of temporary wrapper output.
- On the real host, test one known Qwen 3.5 and one known Gemma 4 local
  Hugging Face GGUF through the binding spike before enabling `--apply` for
  more files.
- Regression-test exact cloned catalog metadata for both profiles whenever the
  supported LM Studio version range changes.

## Acceptance criteria

- The tool runs on the LM Studio host (directly or through a deliberate host
  transport), not only in the VM.
- It never modifies GGUF bytes.
- It exposes the documented `Enable Thinking` custom field for a verified
  local Qwen 3.5 and a verified local Gemma 4 model through a supported
  LM Studio local-binding workflow.
- It refuses any model whose GGUF architecture/template does not meet its
  family profile, despite filename or Hugging Face description.
- It supports recursive discovery and exclusion lists, produces reproducible
  reports, and maps every change to a source hash/profile/host LM Studio
  version.
- Bulk apply is available only after both family pilots pass runtime validation
  on the host.
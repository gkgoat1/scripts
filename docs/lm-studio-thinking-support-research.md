# LM Studio research: thinking controls for local GGUF models

**Research date:** 2026-08-05  
**Scope:** Whether rewriting GGUF metadata alone can make a local model expose
LM Studio’s “Enable Thinking” control or correctly produce a thought panel.

## Findings

1. **LM Studio uses GGUF metadata to select the prompt template, but its
   documented thinking control is a `model.yaml` feature—not a documented GGUF
   metadata key.** The Prompt Template documentation says LM Studio
   automatically configures the prompt template from model-file metadata and
   permits a per-model override. [LM Studio: Prompt Template](https://lmstudio.ai/docs/app/advanced/prompt-template)

2. **The documented way to expose an “Enable Thinking” UI control is a virtual
   `model.yaml` wrapper.** LM Studio’s `model.yaml` documentation defines:

   ```yaml
   metadataOverrides:
     reasoning: true
   customFields:
     - key: enableThinking
       type: boolean
       defaultValue: true
       effects:
         - type: setJinjaVariable
           variable: enable_thinking
   ```

   The documentation expressly says the Jinja template needs an
   `enable_thinking` variable for that custom field to work. It also says
   `metadataOverrides` is presentation-only and “is not used for any functional
   changes to the model.” [LM Studio: model.yaml](https://lmstudio.ai/docs/app/modelyaml)
   [model.yaml Draft specification](https://github.com/modelyaml/modelyaml/blob/main/README.md)

3. **The model must already support the target control protocol.** Toggling the
   documented custom field merely sets a Jinja variable. It cannot make an
   arbitrary model understand `enable_thinking`, produce reasoned tokens, or
   emit the delimiters LM Studio needs to display a thought block. A local
   prompt-template override is available through LM Studio’s app, but changing
   it must be model-family-specific. [LM Studio: Prompt Template](https://lmstudio.ai/docs/app/advanced/prompt-template)

4. **Local/sideloaded GGUFs are not currently documented as automatically
   gaining the control from a compatible template.** An open issue in LM
   Studio’s official bug tracker reports that a side-loaded Qwen3.5 GGUF whose
   template supports `enable_thinking` did not show the UI switch, while a
   separate installed local wrapper did. This is user-reported issue evidence,
   not a platform guarantee; it is useful as a regression case. [LM Studio
   issue #1759](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1759)

5. **`reasoning` in LM Studio’s OpenAI-compatible Responses endpoint is an API
   request parameter, not a GGUF metadata contract.** The published endpoint
   example sends `"reasoning": { "effort": "low" }` for a loaded model. It
   must be tested against each model and cannot be enabled by merely adding a
   GGUF key. [LM Studio: Responses](https://lmstudio.ai/docs/developer/openai-compat/responses)

## Conclusion for the requested tool

A recursive GGUF rewriter should **not** claim that it will make models support
thinking in LM Studio. No first-party LM Studio documentation located in this
research specifies a GGUF metadata key that safely and functionally creates
LM Studio’s thinking control for arbitrary local files.

The correct implementation target is a **non-destructive inventory and
wrapper-generator**:

- inspect GGUF metadata/templates recursively and honor exclusions;
- classify models as compatible, incompatible, unknown, or already wrapped;
- create a separate, versioned LM Studio `model.yaml` virtual-model wrapper
  only for explicitly approved, family-specific profiles whose existing Jinja
  template already supports the configured variable;
- set wrapper `metadataOverrides.reasoning: true` and a custom `enableThinking`
  field that sets the template’s exact variable; and
- validate the wrapper manually in the installed LM Studio version and through
  a known prompt/API test.

Do not rewrite model weights or mutate GGUF metadata by default. If a future
LM Studio release documents an exact GGUF metadata contract for local thinking
controls, add a separate opt-in migration profile with versioned evidence and
round-trip tests.
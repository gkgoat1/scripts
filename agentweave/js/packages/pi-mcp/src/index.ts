import type { ExtensionAPI, ExtensionContext } from "./pi-types.js";
import { Type } from "typebox";
import { AgentWeaveClient, type SamplingRequest } from "./client.js";

type ToolContent = { type: "text"; text: string };

export default function agentWeaveExtension(pi: ExtensionAPI): void {
  pi.registerFlag("agentweave-allow-sampling", {
    description: "Allow explicit AgentWeave synthesis calls to use the current Pi model without a confirmation dialog",
    type: "boolean",
    default: false,
  });

  pi.registerTool({
    name: "agentweave_search",
    label: "AgentWeave Search",
    description: "Search evidence from other local coding agents in the current workspace.",
    parameters: Type.Object({
      query: Type.String(),
      filters: Type.Optional(Type.Object({
        agents: Type.Optional(Type.Array(Type.String())),
        kinds: Type.Optional(Type.Array(Type.String())),
        include_global: Type.Optional(Type.Boolean()),
      })),
      limit: Type.Optional(Type.Number({ minimum: 1, maximum: 50 })),
    }),
    execute: async (_id: string, params: { query: string; filters?: { agents?: string[]; kinds?: string[]; include_global?: boolean }; limit?: number }, _signal: AbortSignal, _update: unknown, ctx: ExtensionContext) => toolCall(ctx, pi, "agentweave_search", { workspace: ctx.cwd, ...params }),
  });
  pi.registerTool({
    name: "agentweave_read",
    label: "AgentWeave Read",
    description: "Read cited AgentWeave evidence chunks from the current workspace.",
    parameters: Type.Object({ refs: Type.Array(Type.String(), { minItems: 1, maxItems: 30 }), max_bytes: Type.Optional(Type.Number({ minimum: 1, maximum: 24576 })) }),
    execute: async (_id: string, params: { refs: string[]; max_bytes?: number }, _signal: AbortSignal, _update: unknown, ctx: ExtensionContext) => toolCall(ctx, pi, "agentweave_read", { workspace: ctx.cwd, ...params }),
  });
  pi.registerTool({
    name: "agentweave_synthesize",
    label: "AgentWeave Synthesize",
    description: "Return evidence or, only with generation='sample', a single evidence-grounded synthesis from the active Pi model.",
    parameters: Type.Object({
      question: Type.String(),
      selection: Type.Optional(Type.Array(Type.String(), { maxItems: 30 })),
      detail: Type.Optional(Type.String()),
      generation: Type.Union([Type.Literal("evidence"), Type.Literal("sample")]),
    }),
    execute: async (_id: string, params: { question: string; selection?: string[]; detail?: string; generation: "evidence" | "sample" }, _signal: AbortSignal, _update: unknown, ctx: ExtensionContext) => toolCall(ctx, pi, "agentweave_synthesize", { workspace: ctx.cwd, ...params }),
  });
  pi.registerTool({
    name: "agentweave_status",
    label: "AgentWeave Status",
    description: "Report AgentWeave source health and index freshness.",
    parameters: Type.Object({}),
    execute: async (_id: string, _params: Record<string, never>, _signal: AbortSignal, _update: unknown, ctx: ExtensionContext) => toolCall(ctx, pi, "agentweave_status", { workspace: ctx.cwd }),
  });
  pi.registerCommand("agentweave", {
    description: "Search AgentWeave evidence in the current workspace: /agentweave <query>",
    handler: async (args, ctx) => {
      if (!args.trim()) {
        ctx.ui.notify("Usage: /agentweave <query>", "warning");
        return;
      }
      try {
        const result = await callAgentWeave(ctx, pi, "agentweave_search", { workspace: ctx.cwd, query: args, limit: 5 });
        ctx.ui.notify(JSON.stringify(result).slice(0, 1200), "info");
      } catch (error) {
        ctx.ui.notify(errorMessage(error), "error");
      }
    },
  });
}

async function toolCall(ctx: ExtensionContext, pi: ExtensionAPI, name: string, params: Record<string, unknown>) {
  try {
    const result = await callAgentWeave(ctx, pi, name, params);
    return { content: [{ type: "text", text: JSON.stringify(result, null, 2) } satisfies ToolContent], details: result };
  } catch (error) {
    return { content: [{ type: "text", text: errorMessage(error) } satisfies ToolContent], details: {}, isError: true };
  }
}

async function callAgentWeave(ctx: ExtensionContext, pi: ExtensionAPI, name: string, params: Record<string, unknown>): Promise<unknown> {
  const client = await AgentWeaveClient.connect({ cwd: ctx.cwd, sampler: request => sampleWithPi(ctx, pi, request) });
  try {
    return await client.call(name, params);
  } finally {
    await client.close();
  }
}

async function sampleWithPi(ctx: ExtensionContext, pi: ExtensionAPI, request: SamplingRequest) {
  if (!ctx.model) throw new Error("Pi has no active model; request generation='evidence' instead");
  if (!pi.getFlag("agentweave-allow-sampling")) {
    if (!ctx.hasUI) throw new Error("sampling requires --agentweave-allow-sampling in noninteractive Pi modes");
    const approved = await ctx.ui.confirm("AgentWeave sampling", "Send the selected local evidence to your active Pi model for one synthesis?");
    if (!approved) throw new Error("sampling denied by user");
  }
  const provider = ctx.modelRegistry.getProvider(ctx.model.provider);
  if (!provider) throw new Error(`Pi provider ${ctx.model.provider} is unavailable; request generation='evidence' instead`);
  const auth = await ctx.modelRegistry.getApiKeyAndHeaders(ctx.model);
  if (!auth.ok) throw new Error(`Pi cannot authorize parent-model sampling: ${auth.error}`);
  const stream = provider.streamSimple(ctx.model, {
    systemPrompt: request.systemPrompt,
    messages: request.messages.map(message => ({ role: message.role, content: message.text, timestamp: Date.now() })),
  }, {
    maxTokens: request.maxTokens,
    temperature: request.temperature,
    signal: ctx.signal,
    apiKey: auth.apiKey,
    headers: auth.headers,
    env: auth.env,
    reasoning: ctx.thinkingLevel,
  });
  const response = await stream.result();
  if (response.stopReason === "toolUse") throw new Error("Pi sampling attempted tool use; AgentWeave does not run sampling tool loops");
  if (response.stopReason === "error" || response.stopReason === "aborted") throw new Error(response.errorMessage ?? "Pi sampling failed");
  const text = response.content.filter((part): part is { type: "text"; text: string } => part.type === "text").map(part => part.text).join("\n");
  if (!text.trim()) throw new Error("Pi sampling returned no text");
  return { model: response.responseModel ?? response.model ?? ctx.model.id, text };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export { AgentWeaveClient } from "./client.js";

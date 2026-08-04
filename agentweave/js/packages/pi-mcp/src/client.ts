import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { CreateMessageRequestSchema } from "@modelcontextprotocol/sdk/types.js";

export type SamplingMessage = { role: "user" | "assistant"; text: string };

export interface SamplingRequest {
  systemPrompt?: string;
  messages: SamplingMessage[];
  maxTokens: number;
  temperature?: number;
}

export interface SamplingResponse {
  model: string;
  text: string;
}

export type ParentSampler = (request: SamplingRequest) => Promise<SamplingResponse>;

export interface AgentWeaveClientOptions {
  command?: string;
  args?: string[];
  cwd?: string;
  sampler: ParentSampler;
}

/**
 * A per-invocation MCP client. Its sampler is closed over the active Pi tool
 * context, preventing one Pi session from accidentally using another one's
 * model or approval policy.
 */
export class AgentWeaveClient {
  private constructor(
    private readonly client: Client,
    private readonly transport: StdioClientTransport,
  ) {}

  static async connect(options: AgentWeaveClientOptions): Promise<AgentWeaveClient> {
    const client = new Client(
      { name: "agentweave-pi-mcp", version: "0.1.0" },
      { capabilities: { sampling: {} } },
    );
    client.setRequestHandler(CreateMessageRequestSchema, async request => {
      const messages = request.params.messages.map(message => ({
        role: message.role,
        text: contentText(message.content),
      }));
      if (messages.some(message => !message.text)) {
        throw new Error("AgentWeave accepts text-only, no-tools sampling requests");
      }
      const response = await options.sampler({
        systemPrompt: request.params.systemPrompt,
        messages,
        maxTokens: Math.min(request.params.maxTokens, 1600),
        temperature: request.params.temperature,
      });
      return {
        model: response.model,
        role: "assistant" as const,
        content: { type: "text" as const, text: response.text },
        stopReason: "endTurn",
      };
    });
    const transport = new StdioClientTransport({
      command: options.command ?? processEnvironment().AGENTWEAVE_COMMAND ?? "agentweave",
      args: options.args ?? ["mcp"],
      cwd: options.cwd,
      stderr: "inherit",
    });
    await client.connect(transport);
    return new AgentWeaveClient(client, transport);
  }

  async call<T>(name: string, args: Record<string, unknown>): Promise<T> {
    const result = await this.client.callTool({ name, arguments: args });
    if (result.isError) {
      throw new Error(textResult(result.content) || `AgentWeave ${name} failed`);
    }
    const text = textResult(result.content);
    if (!text) {
      throw new Error(`AgentWeave ${name} returned no text result`);
    }
    return JSON.parse(text) as T;
  }

  async close(): Promise<void> {
    await this.transport.close();
  }
}

function processEnvironment(): Record<string, string | undefined> {
  // Pi runs this package in Node, but using a structural global keeps the
  // reusable library type-checkable without bundling @types/node.
  return (globalThis as unknown as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};
}

function contentText(content: unknown): string {
  if (typeof content === "object" && content !== null && "type" in content) {
    const block = content as { type?: unknown; text?: unknown };
    return block.type === "text" && typeof block.text === "string" ? block.text : "";
  }
  if (Array.isArray(content)) {
    return content.map(contentText).filter(Boolean).join("\n");
  }
  return "";
}

function textResult(content: unknown): string {
  if (!Array.isArray(content)) return "";
  return content
    .filter((block): block is { type: "text"; text: string } => typeof block === "object" && block !== null && (block as { type?: string }).type === "text" && typeof (block as { text?: unknown }).text === "string")
    .map(block => block.text)
    .join("\n");
}

/**
 * Structural surface used by this extension. Pi supplies the concrete types
 * at runtime; keeping them structural means this reusable package does not
 * bundle, pin, or load a Pi implementation (the Pi package remains a peer).
 */
export interface ExtensionAPI {
  registerFlag(name: string, options: { description?: string; type: "boolean" | "string"; default?: boolean | string }): void;
  getFlag(name: string): boolean | string | undefined;
  registerTool(tool: Record<string, unknown>): void;
  registerCommand(name: string, options: { description?: string; handler: (args: string, ctx: ExtensionContext) => Promise<void> | void }): void;
}

export interface ExtensionContext {
  cwd: string;
  hasUI: boolean;
  signal: AbortSignal;
  thinkingLevel?: unknown;
  model?: { provider: string; id: string };
  ui: {
    confirm(title: string, message: string): Promise<boolean>;
    notify(message: string, level?: "info" | "warning" | "error"): void;
  };
  modelRegistry: {
    getProvider(name: string): {
      streamSimple(model: NonNullable<ExtensionContext["model"]>, context: unknown, options: Record<string, unknown>): {
        result(): Promise<{
          stopReason?: string;
          errorMessage?: string;
          responseModel?: string;
          model?: string;
          content: Array<{ type: string; text?: string }>;
        }>;
      };
    } | undefined;
    getApiKeyAndHeaders(model: NonNullable<ExtensionContext["model"]>): Promise<
      | { ok: true; apiKey?: string; headers?: Record<string, string>; env?: Record<string, string> }
      | { ok: false; error: string }
    >;
  };
}

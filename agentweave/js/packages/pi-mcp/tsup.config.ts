import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/index.ts", "src/client.ts"],
  format: ["esm"],
  dts: true,
  clean: true,
  // Pi is intentionally absent: it is a runtime peer that supplies the
  // extension API. Every other dependency must travel with this package.
  platform: "node",
  shims: true,
  banner: {
    js: 'import { createRequire } from "node:module"; const require = createRequire(import.meta.url);',
  },
  noExternal: ["@modelcontextprotocol/sdk", "typebox"],
});
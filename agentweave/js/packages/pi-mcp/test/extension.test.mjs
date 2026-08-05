import { cp, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { spawn } from "node:child_process";
import assert from "node:assert/strict";
import test from "node:test";
import extension from "../dist/index.js";

test("registers the Pi 0.82-compatible tool and command surface", () => {
  const flags = [];
  const tools = [];
  const commands = [];
  extension({
    registerFlag(name, options) { flags.push({ name, options }); },
    getFlag() { return false; },
    registerTool(tool) { tools.push(tool); },
    registerCommand(name, options) { commands.push({ name, options }); },
  });
  assert.deepEqual(flags.map(item => item.name), ["agentweave-allow-sampling"]);
  assert.deepEqual(tools.map(item => item.name), ["agentweave_search", "agentweave_read", "agentweave_synthesize", "agentweave_status"]);
  assert.deepEqual(commands.map(item => item.name), ["agentweave"]);
});

test("loads after node_modules cleanup", async () => {
  const packageDir = fileURLToPath(new URL("..", import.meta.url));
  const temporaryRoot = await mkdtemp(join(tmpdir(), "agentweave-pi-mcp-"));
  const copiedPackage = join(temporaryRoot, "agentweave-pi-mcp");

  try {
    await cp(packageDir, copiedPackage, { recursive: true, filter: path => !path.endsWith("node_modules") });
    const result = await run(process.execPath, ["--input-type=module", "--eval", `import(${JSON.stringify(pathToFileURL(join(copiedPackage, "dist/index.js")).href)})`], {
      cwd: copiedPackage,
    });
    assert.equal(result.code, 0, result.stderr);
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
});

function run(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: ["ignore", "ignore", "pipe"] });
    let stderr = "";
    child.stderr.on("data", chunk => { stderr += chunk; });
    child.on("error", reject);
    child.on("close", code => resolve({ code, stderr }));
  });
}

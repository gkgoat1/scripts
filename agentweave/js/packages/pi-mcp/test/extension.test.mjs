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

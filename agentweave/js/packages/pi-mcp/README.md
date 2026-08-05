# agentweave-pi-mcp

Pi extension and reusable stdio MCP bridge for AgentWeave. Install it with `pi install npm:agentweave-pi-mcp`, start `agentweaved`, then use the `agentweave_*` tools or `/agentweave <query>`.

The release build bundles the MCP SDK, TypeBox, and their transitive dependencies into `dist`; Pi itself remains a peer dependency and is loaded only through Pi's extension API. This lets Pi continue loading the installed extension after cleanup removes `node_modules` directories.

`generation: "sample"` is explicit. It asks for confirmation by default and makes exactly one completion with Pi's active model; evidence mode never calls a model.

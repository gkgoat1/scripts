package main

import "testing"

func TestConfigSnippets(t *testing.T) {
	tests := map[string]string{
		"claude":      "{\n  \"mcpServers\": {\n    \"agentweave\": {\n      \"command\": \"agentweave\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}\n",
		"cursor":      "{\n  \"mcpServers\": {\n    \"agentweave\": {\n      \"command\": \"agentweave\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}\n",
		"antigravity": "{\n  \"mcpServers\": {\n    \"agentweave\": {\n      \"command\": \"agentweave\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}\n",
		"codex":       "[mcp_servers.agentweave]\ncommand = \"agentweave\"\nargs = [\"mcp\"]\n",
		"pi":          "pi install npm:agentweave-pi-mcp\n# Then start agentweaved; the extension launches its own stdio MCP client per tool call.\n",
	}
	for client, want := range tests {
		if got := configSnippet(client); got != want {
			t.Errorf("%s config snapshot mismatch\nwant:\n%s\ngot:\n%s", client, want, got)
		}
	}
}

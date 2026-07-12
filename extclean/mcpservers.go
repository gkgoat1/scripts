package main

import "fmt"

// mcpServerEntry is the shape shared by Claude Code's and Cursor's
// mcpServers maps: {"name": {"command": "...", "args": [...]}}. The command
// field here is always a single literal executable name/path (never a
// shell one-liner), so it's resolved via ResolveCommandToken directly, the
// same as Codex's mcp_servers.*.command.
type mcpServerEntry struct {
	Command string `json:"command"`
}

// scanMCPServersMap scans a decoded {name: entry} mcpServers map shared by
// Claude Code (~/.claude.json, global and per-project) and Cursor
// (~/.cursor/mcp.json).
func scanMCPServersMap(tool, configFile string, rootKeyPath []string, servers map[string]mcpServerEntry, baseDir string, orphaned bool, pc PathChecker, pr PathResolver) []Finding {
	var findings []Finding
	for name, entry := range servers {
		loc := Locator{JSONMapKey: &JSONMapKeyLocator{RootKeyPath: rootKeyPath, MapKey: name}}
		if orphaned {
			findings = append(findings, Finding{
				Tool:       tool,
				Kind:       KindMCPServer,
				Name:       name,
				ConfigFile: configFile,
				Reason:     ReasonOrphaned,
				Detail:     fmt.Sprintf("%s is not installed on this system; this MCP server registration is a stale leftover", tool),
				Locator:    loc,
			})
			continue
		}
		ok, detail := ResolveCommandToken(entry.Command, baseDir, pc, pr)
		if !ok {
			findings = append(findings, Finding{
				Tool:       tool,
				Kind:       KindMCPServer,
				Name:       name,
				ConfigFile: configFile,
				Reason:     ReasonDangling,
				Detail:     detail,
				Locator:    loc,
			})
		}
	}
	return findings
}

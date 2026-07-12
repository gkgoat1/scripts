package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// ClaudeScanner scans Claude Code's hooks, MCP servers, plugins, and
// marketplaces.
type ClaudeScanner struct {
	fr      FileReader
	pc      PathChecker
	pr      PathResolver
	ic      InstalledChecker
	homeDir string
}

func NewClaudeScanner(fr FileReader, pc PathChecker, pr PathResolver, ic InstalledChecker, homeDir string) *ClaudeScanner {
	return &ClaudeScanner{fr: fr, pc: pc, pr: pr, ic: ic, homeDir: homeDir}
}

func (s *ClaudeScanner) settingsPath() string {
	return filepath.Join(s.homeDir, ".claude", "settings.json")
}

func (s *ClaudeScanner) appJSONPath() string {
	return filepath.Join(s.homeDir, ".claude.json")
}

func (s *ClaudeScanner) installedPluginsPath() string {
	return filepath.Join(s.homeDir, ".claude", "plugins", "installed_plugins.json")
}

func (s *ClaudeScanner) knownMarketplacesPath() string {
	return filepath.Join(s.homeDir, ".claude", "plugins", "known_marketplaces.json")
}

type claudeHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type claudeHookGroup struct {
	Matcher string            `json:"matcher"`
	Hooks   []claudeHookEntry `json:"hooks"`
}

type claudeSettings struct {
	Hooks map[string][]claudeHookGroup `json:"hooks"`
}

type claudeProjectEntry struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type claudeAppJSON struct {
	MCPServers map[string]mcpServerEntry     `json:"mcpServers"`
	Projects   map[string]claudeProjectEntry `json:"projects"`
}

type installedPluginEntry struct {
	InstallPath string `json:"installPath"`
}

type installedPluginsFile struct {
	Plugins map[string][]installedPluginEntry `json:"plugins"`
}

type marketplaceEntry struct {
	InstallLocation string `json:"installLocation"`
}

// Scan runs every Claude Code check and returns the combined findings.
func (s *ClaudeScanner) Scan() ([]Finding, error) {
	var findings []Finding

	hooks, err := s.scanHooks()
	if err != nil {
		return nil, err
	}
	findings = append(findings, hooks...)

	mcp, err := s.scanMCPServers()
	if err != nil {
		return nil, err
	}
	findings = append(findings, mcp...)

	plugins, err := s.scanPlugins()
	if err != nil {
		return nil, err
	}
	findings = append(findings, plugins...)

	marketplaces, err := s.scanMarketplaces()
	if err != nil {
		return nil, err
	}
	findings = append(findings, marketplaces...)

	return findings, nil
}

func (s *ClaudeScanner) scanHooks() ([]Finding, error) {
	path := s.settingsPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var settings claudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.ClaudeCodeInstalled()

	var findings []Finding
	for event, groups := range settings.Hooks {
		for _, group := range groups {
			for _, h := range group.Hooks {
				if h.Type != "command" || h.Command == "" {
					continue
				}
				loc := Locator{JSONArrayIdx: &JSONArrayIdxLocator{RootKeyPath: []string{"hooks", event}, MatchName: h.Command}}
				if orphaned {
					findings = append(findings, Finding{
						Tool: "claude", Kind: KindHook, Name: event + ": " + h.Command, ConfigFile: path,
						Reason: ReasonOrphaned,
						Detail: "Claude Code is not installed on this system; this hook is a stale leftover",
						Locator: loc,
					})
					continue
				}
				ok, detail := ResolveCommand(h.Command, s.homeDir, s.pc, s.pr)
				if !ok {
					findings = append(findings, Finding{
						Tool: "claude", Kind: KindHook, Name: event + ": " + h.Command, ConfigFile: path,
						Reason: ReasonDangling, Detail: detail, Locator: loc,
					})
				}
			}
		}
	}
	SortFindings(findings)
	return findings, nil
}

func (s *ClaudeScanner) scanMCPServers() ([]Finding, error) {
	path := s.appJSONPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var app claudeAppJSON
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.ClaudeCodeInstalled()

	var findings []Finding
	findings = append(findings, scanMCPServersMap("claude", path, []string{"mcpServers"}, app.MCPServers, s.homeDir, orphaned, s.pc, s.pr)...)
	for projectPath, proj := range app.Projects {
		rootKeyPath := []string{"projects", projectPath, "mcpServers"}
		findings = append(findings, scanMCPServersMap("claude", path, rootKeyPath, proj.MCPServers, s.homeDir, orphaned, s.pc, s.pr)...)
	}
	SortFindings(findings)
	return findings, nil
}

func (s *ClaudeScanner) scanPlugins() ([]Finding, error) {
	path := s.installedPluginsPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f installedPluginsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.ClaudeCodeInstalled()

	var findings []Finding
	for name, entries := range f.Plugins {
		for _, e := range entries {
			loc := Locator{JSONArrayIdx: &JSONArrayIdxLocator{RootKeyPath: []string{"plugins", name}, MatchName: e.InstallPath}}
			if orphaned {
				findings = append(findings, Finding{
					Tool: "claude", Kind: KindPlugin, Name: name, ConfigFile: path,
					Reason: ReasonOrphaned,
					Detail: "Claude Code is not installed on this system; this plugin registration is a stale leftover",
					Locator: loc,
				})
				continue
			}
			if !s.pc.Exists(e.InstallPath) {
				findings = append(findings, Finding{
					Tool: "claude", Kind: KindPlugin, Name: name, ConfigFile: path,
					Reason: ReasonDangling,
					Detail: fmt.Sprintf("installPath not found: %s", e.InstallPath),
					Locator: loc,
				})
			}
		}
	}
	SortFindings(findings)
	return findings, nil
}

func (s *ClaudeScanner) scanMarketplaces() ([]Finding, error) {
	path := s.knownMarketplacesPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var marketplaces map[string]marketplaceEntry
	if err := json.Unmarshal(data, &marketplaces); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.ClaudeCodeInstalled()

	var findings []Finding
	for name, m := range marketplaces {
		loc := Locator{JSONMapKey: &JSONMapKeyLocator{MapKey: name}}
		if orphaned {
			findings = append(findings, Finding{
				Tool: "claude", Kind: KindMarketplace, Name: name, ConfigFile: path,
				Reason: ReasonOrphaned,
				Detail: "Claude Code is not installed on this system; this marketplace registration is a stale leftover",
				Locator: loc,
			})
			continue
		}
		if !s.pc.Exists(m.InstallLocation) {
			findings = append(findings, Finding{
				Tool: "claude", Kind: KindMarketplace, Name: name, ConfigFile: path,
				Reason: ReasonDangling,
				Detail: fmt.Sprintf("installLocation not found: %s", m.InstallLocation),
				Locator: loc,
			})
		}
	}
	SortFindings(findings)
	return findings, nil
}

// removeClaudeHook removes the first hook entry matching command under
// hooks.<event>[].hooks[], reaching into the nested per-matcher-group
// "hooks" array (a shape the generic removeArrayElement helper doesn't fit,
// since the array to filter is nested one level inside each element of the
// outer per-event array).
func removeClaudeHook(root any, event, command string) (bool, error) {
	m, err := navigateMap(root, []string{"hooks"})
	if err != nil {
		return false, err
	}
	val, ok := m[event]
	if !ok {
		return false, fmt.Errorf("event %q not found", event)
	}
	groups, ok := val.([]any)
	if !ok {
		return false, fmt.Errorf("expected array at hooks.%s, got %T", event, val)
	}
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hooksVal, ok := group["hooks"]
		if !ok {
			continue
		}
		hooksArr, ok := hooksVal.([]any)
		if !ok {
			continue
		}
		newArr, removed := filterOutFirst(hooksArr, func(el any) bool {
			he, ok := el.(map[string]any)
			return ok && he["command"] == command
		})
		if removed {
			group["hooks"] = newArr
			return true, nil
		}
	}
	return false, nil
}

// RemoveFinding removes f from its config file, for --apply.
func (s *ClaudeScanner) RemoveFinding(f Finding) error {
	switch f.Kind {
	case KindHook:
		loc := f.Locator.JSONArrayIdx
		if loc == nil || len(loc.RootKeyPath) != 2 {
			return fmt.Errorf("claude: malformed hook locator for %q", f.Name)
		}
		event := loc.RootKeyPath[1]
		command := loc.MatchName
		return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
			removed, err := removeClaudeHook(root, event, command)
			return root, removed, err
		})
	case KindMCPServer:
		loc := f.Locator.JSONMapKey
		if loc == nil {
			return fmt.Errorf("claude: malformed mcp locator for %q", f.Name)
		}
		return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
			removed, err := removeMapKey(root, loc.RootKeyPath, loc.MapKey)
			return root, removed, err
		})
	case KindPlugin:
		loc := f.Locator.JSONArrayIdx
		if loc == nil || len(loc.RootKeyPath) != 2 {
			return fmt.Errorf("claude: malformed plugin locator for %q", f.Name)
		}
		return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
			return removeArrayElement(root, loc.RootKeyPath, func(el any) bool {
				m, ok := el.(map[string]any)
				return ok && m["installPath"] == loc.MatchName
			})
		})
	case KindMarketplace:
		loc := f.Locator.JSONMapKey
		if loc == nil {
			return fmt.Errorf("claude: malformed marketplace locator for %q", f.Name)
		}
		return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
			removed, err := removeMapKey(root, loc.RootKeyPath, loc.MapKey)
			return root, removed, err
		})
	default:
		return fmt.Errorf("claude: unsupported finding kind %q", f.Kind)
	}
}

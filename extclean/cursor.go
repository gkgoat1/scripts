package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// CursorScanner scans Cursor's VS Code-style extensions manifest and its
// MCP server registrations.
type CursorScanner struct {
	fr      FileReader
	pc      PathChecker
	pr      PathResolver
	ic      InstalledChecker
	homeDir string
}

func NewCursorScanner(fr FileReader, pc PathChecker, pr PathResolver, ic InstalledChecker, homeDir string) *CursorScanner {
	return &CursorScanner{fr: fr, pc: pc, pr: pr, ic: ic, homeDir: homeDir}
}

func (s *CursorScanner) extensionsDir() string {
	return filepath.Join(s.homeDir, ".cursor", "extensions")
}

func (s *CursorScanner) extensionsJSONPath() string {
	return filepath.Join(s.extensionsDir(), "extensions.json")
}

func (s *CursorScanner) mcpJSONPath() string {
	return filepath.Join(s.homeDir, ".cursor", "mcp.json")
}

type cursorExtensionIdentifier struct {
	ID string `json:"id"`
}

type cursorExtensionEntry struct {
	Identifier       cursorExtensionIdentifier `json:"identifier"`
	RelativeLocation string                    `json:"relativeLocation"`
}

type cursorMCPFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// Scan runs both Cursor checks and returns the combined findings.
func (s *CursorScanner) Scan() ([]Finding, error) {
	var findings []Finding

	ext, err := s.scanExtensions()
	if err != nil {
		return nil, err
	}
	findings = append(findings, ext...)

	mcp, err := s.scanMCPServers()
	if err != nil {
		return nil, err
	}
	findings = append(findings, mcp...)

	return findings, nil
}

func (s *CursorScanner) scanExtensions() ([]Finding, error) {
	path := s.extensionsJSONPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var entries []cursorExtensionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.CursorInstalled()

	var findings []Finding
	for _, e := range entries {
		loc := Locator{JSONArrayIdx: &JSONArrayIdxLocator{MatchName: e.Identifier.ID}}
		if orphaned {
			findings = append(findings, Finding{
				Tool: "cursor", Kind: KindExtension, Name: e.Identifier.ID, ConfigFile: path,
				Reason: ReasonOrphaned,
				Detail: "Cursor is not installed on this system; this extension registration is a stale leftover",
				Locator: loc,
			})
			continue
		}
		manifest := filepath.Join(s.extensionsDir(), e.RelativeLocation, "package.json")
		if !s.pc.Exists(manifest) {
			findings = append(findings, Finding{
				Tool: "cursor", Kind: KindExtension, Name: e.Identifier.ID, ConfigFile: path,
				Reason: ReasonDangling,
				Detail: fmt.Sprintf("package.json not found at %s", manifest),
				Locator: loc,
			})
		}
	}
	SortFindings(findings)
	return findings, nil
}

func (s *CursorScanner) scanMCPServers() ([]Finding, error) {
	path := s.mcpJSONPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var mcp cursorMCPFile
	if err := json.Unmarshal(data, &mcp); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.CursorInstalled()
	findings := scanMCPServersMap("cursor", path, []string{"mcpServers"}, mcp.MCPServers, s.homeDir, orphaned, s.pc, s.pr)
	SortFindings(findings)
	return findings, nil
}

// RemoveFinding removes f from its config file, for --apply.
func (s *CursorScanner) RemoveFinding(f Finding) error {
	switch f.Kind {
	case KindExtension:
		loc := f.Locator.JSONArrayIdx
		if loc == nil {
			return fmt.Errorf("cursor: malformed extension locator for %q", f.Name)
		}
		return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
			return removeArrayElement(root, loc.RootKeyPath, func(el any) bool {
				m, ok := el.(map[string]any)
				if !ok {
					return false
				}
				ident, ok := m["identifier"].(map[string]any)
				return ok && ident["id"] == loc.MatchName
			})
		})
	case KindMCPServer:
		loc := f.Locator.JSONMapKey
		if loc == nil {
			return fmt.Errorf("cursor: malformed mcp locator for %q", f.Name)
		}
		return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
			removed, err := removeMapKey(root, loc.RootKeyPath, loc.MapKey)
			return root, removed, err
		})
	default:
		return fmt.Errorf("cursor: unsupported finding kind %q", f.Kind)
	}
}

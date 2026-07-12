package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TomlReader turns a TOML file into the same JSON-decoded shape
// encoding/json would produce, so Codex's config.toml can be read without
// adding this repo's first third-party Go dependency.
type TomlReader interface {
	ReadTomlAsJSON(path string) ([]byte, error)
}

// realTomlReader shells out to a discovered Python interpreter's stdlib
// tomllib. Bare `python3` on PATH is not guaranteed to have tomllib (it's
// Python 3.11+ stdlib; a system Python can be older), so a short candidate
// list is probed and the first working interpreter is cached.
type realTomlReader struct {
	pr     PathResolver
	interp string
}

func newRealTomlReader(pr PathResolver) *realTomlReader {
	return &realTomlReader{pr: pr}
}

const tomllibToJSONScript = `import tomllib, json, sys
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
json.dump(data, sys.stdout)`

func (r *realTomlReader) ReadTomlAsJSON(path string) ([]byte, error) {
	interp, err := r.findInterpreter()
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(interp, "-c", tomllibToJSONScript, path).Output()
	if err != nil {
		return nil, fmt.Errorf("running %s to parse %s: %w", interp, path, err)
	}
	return out, nil
}

var tomllibCandidates = []string{"python3", "python3.13", "python3.12", "python3.11", "/opt/homebrew/bin/python3"}

func (r *realTomlReader) findInterpreter() (string, error) {
	if r.interp != "" {
		return r.interp, nil
	}
	var tried []string
	for _, c := range tomllibCandidates {
		path := c
		if strings.HasPrefix(c, "/") {
			if _, err := os.Stat(path); err != nil {
				continue
			}
		} else {
			resolved, err := r.pr.LookPath(c)
			if err != nil {
				continue
			}
			path = resolved
		}
		tried = append(tried, path)
		if exec.Command(path, "-c", "import tomllib").Run() == nil {
			r.interp = path
			return path, nil
		}
	}
	return "", fmt.Errorf("no python3 interpreter with tomllib found (tried: %s)", strings.Join(tried, ", "))
}

// CodexScanner scans Codex's marketplaces, plugins, and MCP servers.
type CodexScanner struct {
	tr      TomlReader
	fr      FileReader
	pc      PathChecker
	pr      PathResolver
	ic      InstalledChecker
	homeDir string
}

func NewCodexScanner(tr TomlReader, fr FileReader, pc PathChecker, pr PathResolver, ic InstalledChecker, homeDir string) *CodexScanner {
	return &CodexScanner{tr: tr, fr: fr, pc: pc, pr: pr, ic: ic, homeDir: homeDir}
}

func (s *CodexScanner) configPath() string {
	return filepath.Join(s.homeDir, ".codex", "config.toml")
}

type codexMarketplace struct {
	Source string `json:"source"`
}

type codexPlugin struct {
	Enabled *bool `json:"enabled"`
}

type codexMCPServer struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	Enabled *bool  `json:"enabled"`
}

type codexConfig struct {
	Marketplaces map[string]codexMarketplace `json:"marketplaces"`
	Plugins      map[string]codexPlugin      `json:"plugins"`
	MCPServers   map[string]codexMCPServer   `json:"mcp_servers"`
}

// splitPluginKey splits a "name@marketplace" plugin key on the last '@',
// since a plugin name itself could in principle contain '@'.
func splitPluginKey(key string) (name, marketplace string, ok bool) {
	i := strings.LastIndex(key, "@")
	if i < 0 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

func disabledSuffix(enabled *bool) string {
	if enabled != nil && !*enabled {
		return " (currently disabled)"
	}
	return ""
}

// Scan runs every Codex check and returns the combined findings.
func (s *CodexScanner) Scan() ([]Finding, error) {
	path := s.configPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	jsonData, err := s.tr.ReadTomlAsJSON(path)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var cfg codexConfig
	if err := json.Unmarshal(jsonData, &cfg); err != nil {
		return nil, fmt.Errorf("decode toml-as-json for %s: %w", path, err)
	}

	orphaned := !s.ic.CodexInstalled()
	var findings []Finding

	for name, mp := range cfg.Marketplaces {
		loc := Locator{TOMLTable: &TOMLTableLocator{Header: fmt.Sprintf("[marketplaces.%s]", name)}}
		if orphaned {
			findings = append(findings, Finding{
				Tool: "codex", Kind: KindMarketplace, Name: name, ConfigFile: path,
				Reason: ReasonOrphaned,
				Detail: "Codex is not installed on this system; this marketplace registration is a stale leftover",
				Locator: loc,
			})
			continue
		}
		if !s.pc.Exists(mp.Source) {
			findings = append(findings, Finding{
				Tool: "codex", Kind: KindMarketplace, Name: name, ConfigFile: path,
				Reason: ReasonDangling,
				Detail: fmt.Sprintf("source not found: %s", mp.Source),
				Locator: loc,
			})
		}
	}

	for key, pl := range cfg.Plugins {
		name, marketplace, ok := splitPluginKey(key)
		if !ok {
			continue
		}
		loc := Locator{TOMLTable: &TOMLTableLocator{Header: fmt.Sprintf("[plugins.%q]", key)}}
		suffix := disabledSuffix(pl.Enabled)
		if orphaned {
			findings = append(findings, Finding{
				Tool: "codex", Kind: KindPlugin, Name: key, ConfigFile: path,
				Reason: ReasonOrphaned,
				Detail: "Codex is not installed on this system; this plugin registration is a stale leftover" + suffix,
				Locator: loc,
			})
			continue
		}
		cacheDir := filepath.Join(s.homeDir, ".codex", "plugins", "cache", marketplace, name)
		if !s.pc.IsNonEmptyDir(cacheDir) {
			findings = append(findings, Finding{
				Tool: "codex", Kind: KindPlugin, Name: key, ConfigFile: path,
				Reason: ReasonDangling,
				Detail: fmt.Sprintf("cache dir missing or empty: %s%s", cacheDir, suffix),
				Locator: loc,
			})
		}
	}

	for name, m := range cfg.MCPServers {
		loc := Locator{TOMLTable: &TOMLTableLocator{Header: fmt.Sprintf("[mcp_servers.%s]", name)}}
		suffix := disabledSuffix(m.Enabled)
		if orphaned {
			findings = append(findings, Finding{
				Tool: "codex", Kind: KindMCPServer, Name: name, ConfigFile: path,
				Reason: ReasonOrphaned,
				Detail: "Codex is not installed on this system; this MCP server registration is a stale leftover" + suffix,
				Locator: loc,
			})
			continue
		}
		// A relative cwd (e.g. ".") is anchored to this server's own
		// per-server directory, ~/.codex/<name>/ -- confirmed against the
		// real "computer-use" entry on this machine, whose command only
		// resolves from ~/.codex/computer-use/, not ~/.codex/ directly.
		baseDir := filepath.Join(s.homeDir, ".codex", name)
		if m.Cwd != "" && filepath.IsAbs(m.Cwd) {
			baseDir = m.Cwd
		} else if m.Cwd != "" {
			baseDir = filepath.Join(baseDir, m.Cwd)
		}
		ok, detail := ResolveCommandToken(m.Command, baseDir, s.pc, s.pr)
		if !ok {
			findings = append(findings, Finding{
				Tool: "codex", Kind: KindMCPServer, Name: name, ConfigFile: path,
				Reason: ReasonDangling, Detail: detail + suffix, Locator: loc,
			})
		}
	}

	SortFindings(findings)
	return findings, nil
}

// RemoveTomlTable returns content with the exact table header line matching
// header removed, along with every line through (but not including) the
// next top-level "[...]" header or EOF. It errors rather than guessing if
// header appears zero or more than once, mirroring installer/rcblock.sh's
// _rcblock_strip. This is a line-based excision, not a TOML parse-and-
// re-serialize, so every other section's formatting/comments/ordering is
// preserved byte-for-byte.
//
// A nested dotted continuation table (e.g. "[mcp_servers.foo.env]"
// immediately following "[mcp_servers.foo]") also matches the "next
// top-level header" boundary and ends the excision there -- a known,
// deliberate limitation: such a subtable is left in the file rather than
// removed along with its parent.
func RemoveTomlTable(content []byte, header string) ([]byte, error) {
	text := string(content)
	hasTrailingNewline := strings.HasSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if hasTrailingNewline {
		lines = lines[:len(lines)-1]
	}

	matchIdx := -1
	count := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			count++
			matchIdx = i
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("table %q not found", header)
	}
	if count > 1 {
		return nil, fmt.Errorf("table %q appears %d times; refusing to guess which to remove", header, count)
	}

	endIdx := len(lines)
	for i := matchIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			endIdx = i
			break
		}
	}

	newLines := append([]string{}, lines[:matchIdx]...)
	newLines = append(newLines, lines[endIdx:]...)

	// Collapse a blank seam left at the excision boundary: either a
	// dangling trailing blank line (the table removed extended to EOF, so
	// the separator blank before it now has nothing after it), or a
	// blank-blank seam where a separator survives on both sides.
	switch {
	case endIdx == len(lines) && len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) == "":
		newLines = newLines[:len(newLines)-1]
	case matchIdx > 0 && matchIdx < len(newLines) &&
		strings.TrimSpace(newLines[matchIdx-1]) == "" && strings.TrimSpace(newLines[matchIdx]) == "":
		newLines = append(newLines[:matchIdx], newLines[matchIdx+1:]...)
	}

	out := strings.Join(newLines, "\n")
	if hasTrailingNewline {
		out += "\n"
	}
	return []byte(out), nil
}

// RemoveFinding removes f's table from Codex's config.toml, for --apply.
func (s *CodexScanner) RemoveFinding(f Finding) error {
	loc := f.Locator.TOMLTable
	if loc == nil {
		return fmt.Errorf("codex: unsupported locator for finding %q", f.Name)
	}
	content, err := s.fr.ReadFile(f.ConfigFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", f.ConfigFile, err)
	}
	newContent, err := RemoveTomlTable(content, loc.Header)
	if err != nil {
		return fmt.Errorf("%s: %w", f.ConfigFile, err)
	}
	return atomicWriteFile(f.ConfigFile, newContent)
}

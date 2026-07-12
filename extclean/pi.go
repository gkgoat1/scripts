package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// PiScanner scans Pi's (~/.pi/agent/settings.json) "packages" list, its one
// and only extension-equivalent registration for this MVP.
type PiScanner struct {
	fr      FileReader
	pc      PathChecker
	ic      InstalledChecker
	homeDir string
}

func NewPiScanner(fr FileReader, pc PathChecker, ic InstalledChecker, homeDir string) *PiScanner {
	return &PiScanner{fr: fr, pc: pc, ic: ic, homeDir: homeDir}
}

func (s *PiScanner) settingsPath() string {
	return filepath.Join(s.homeDir, ".pi", "agent", "settings.json")
}

// Scan reports dangling or orphaned Pi packages.
func (s *PiScanner) Scan() ([]Finding, error) {
	path := s.settingsPath()
	if !s.pc.Exists(path) {
		return nil, nil
	}
	data, err := s.fr.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var settings struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	orphaned := !s.ic.PiInstalled()

	var findings []Finding
	for _, entry := range settings.Packages {
		name, ok := strings.CutPrefix(entry, "npm:")
		if !ok {
			// Not an npm: package reference -- not a shape this MVP understands.
			continue
		}

		if orphaned {
			findings = append(findings, Finding{
				Tool:       "pi",
				Kind:       KindPackage,
				Name:       name,
				ConfigFile: path,
				Reason:     ReasonOrphaned,
				Detail:     "pi is not installed on this system; this package registration is a stale leftover",
				Locator:    Locator{JSONArrayIdx: &JSONArrayIdxLocator{RootKeyPath: []string{"packages"}, MatchName: entry}},
			})
			continue
		}

		manifest := filepath.Join(s.homeDir, ".pi", "agent", "npm", "node_modules", name, "package.json")
		if !s.pc.Exists(manifest) {
			findings = append(findings, Finding{
				Tool:       "pi",
				Kind:       KindPackage,
				Name:       name,
				ConfigFile: path,
				Reason:     ReasonDangling,
				Detail:     fmt.Sprintf("package.json not found at %s", manifest),
				Locator:    Locator{JSONArrayIdx: &JSONArrayIdxLocator{RootKeyPath: []string{"packages"}, MatchName: entry}},
			})
		}
	}
	return findings, nil
}

// RemoveFinding removes f from Pi's packages list, for --apply.
func (s *PiScanner) RemoveFinding(f Finding) error {
	loc := f.Locator.JSONArrayIdx
	if loc == nil {
		return fmt.Errorf("pi: unsupported locator for finding %q", f.Name)
	}
	return ApplyJSONRemoval(s.fr, f.ConfigFile, func(root any) (any, bool, error) {
		return removeArrayElement(root, loc.RootKeyPath, func(el any) bool {
			str, ok := el.(string)
			return ok && str == loc.MatchName
		})
	})
}

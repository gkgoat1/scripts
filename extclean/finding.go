package main

import (
	"fmt"
	"io"
	"sort"
)

// Kind identifies what sort of registration a Finding refers to.
type Kind string

const (
	KindHook        Kind = "hook"
	KindMCPServer   Kind = "mcp"
	KindPlugin      Kind = "plugin"
	KindMarketplace Kind = "marketplace"
	KindExtension   Kind = "extension"
	KindPackage     Kind = "package"
)

// Reason is why a Finding was flagged.
type Reason string

const (
	// ReasonDangling: the entry's command/path doesn't exist or doesn't
	// resolve via PATH.
	ReasonDangling Reason = "dangling"
	// ReasonOrphaned: the entry's owning tool isn't installed at all, so
	// every entry in its config file is a stale leftover.
	ReasonOrphaned Reason = "orphaned"
)

// JSONMapKeyLocator identifies a single key in a JSON object, addressed by
// walking RootKeyPath from the document root and then removing MapKey.
type JSONMapKeyLocator struct {
	RootKeyPath []string
	MapKey      string
}

// JSONArrayIdxLocator identifies an element of a JSON array by a stable
// identity value (MatchName) rather than a raw index, since scan and apply
// aren't guaranteed to run back-to-back against an unchanged file.
type JSONArrayIdxLocator struct {
	RootKeyPath []string
	MatchName   string
}

// TOMLTableLocator identifies an exact TOML table header line to excise.
type TOMLTableLocator struct {
	Header string
}

// Locator carries enough information for --apply to re-locate and remove a
// Finding's entry without re-scanning. Exactly one field is set, matching
// the shape of the config file the Finding came from.
type Locator struct {
	JSONMapKey   *JSONMapKeyLocator   `json:"jsonMapKey,omitempty"`
	JSONArrayIdx *JSONArrayIdxLocator `json:"jsonArrayIdx,omitempty"`
	TOMLTable    *TOMLTableLocator    `json:"tomlTable,omitempty"`
}

// Finding is one flagged registration.
type Finding struct {
	Tool       string  `json:"tool"`
	Kind       Kind    `json:"kind"`
	Name       string  `json:"name"`
	ConfigFile string  `json:"configFile"`
	Reason     Reason  `json:"reason"`
	Detail     string  `json:"detail"`
	Locator    Locator `json:"locator"`
}

// SortFindings sorts findings deterministically: tool, then kind, then name.
func SortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Tool != b.Tool {
			return a.Tool < b.Tool
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
}

// GroupByTool groups findings by Tool, preserving the relative order within
// each group.
func GroupByTool(findings []Finding) map[string][]Finding {
	groups := make(map[string][]Finding)
	for _, f := range findings {
		groups[f.Tool] = append(groups[f.Tool], f)
	}
	return groups
}

// PrintReport writes a human-readable report of findings, grouped by tool.
func PrintReport(w io.Writer, tools []string, findings []Finding) {
	groups := GroupByTool(findings)
	dangling, orphaned := 0, 0
	for _, f := range findings {
		switch f.Reason {
		case ReasonDangling:
			dangling++
		case ReasonOrphaned:
			orphaned++
		}
	}

	for _, tool := range tools {
		fmt.Fprintf(w, "== %s ==\n", tool)
		fs := groups[tool]
		if len(fs) == 0 {
			fmt.Fprintln(w, "(no findings)")
			fmt.Fprintln(w)
			continue
		}
		for _, f := range fs {
			fmt.Fprintf(w, "[%s] %s\n", f.Kind, f.Name)
			fmt.Fprintf(w, "  file:   %s\n", f.ConfigFile)
			fmt.Fprintf(w, "  reason: %s\n", f.Reason)
			fmt.Fprintf(w, "  detail: %s\n", f.Detail)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "%d tool(s) scanned, %d finding(s) (%d dangling, %d orphaned).",
		len(tools), len(findings), dangling, orphaned)
	if len(findings) > 0 {
		fmt.Fprint(w, " Run with -apply to remove.")
	}
	fmt.Fprintln(w)
}

// Command extclean scans Claude Code, Cursor, Codex, and Pi for hook/MCP
// server/plugin/extension registrations that are dangling (the referenced
// command/path doesn't exist) or orphaned (the owning tool isn't installed
// at all, so the whole config file is a stale leftover). It reports by
// default; -apply removes what it finds. See docs/extclean.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type scanner interface {
	Scan() ([]Finding, error)
}

func main() {
	home := flag.String("home", os.Getenv("HOME"), "home directory to scan (override for testing)")
	apply := flag.Bool("apply", false, "remove flagged entries instead of only reporting them")
	toolFlag := flag.String("tool", "", "scope to a single agent: claude, cursor, codex, or pi (default: all four)")
	jsonOut := flag.Bool("json", false, "emit findings as JSON instead of a human-readable report")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: extclean [-apply] [-tool claude|cursor|codex|pi] [-json]\n\n")
		fmt.Fprintf(os.Stderr, "Scans Claude Code, Cursor, Codex, and Pi for hook/MCP-server/plugin/extension\n")
		fmt.Fprintf(os.Stderr, "registrations that are dangling (missing command/path) or orphaned (the\n")
		fmt.Fprintf(os.Stderr, "owning tool isn't installed at all). Reports by default; -apply removes them.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	allTools := []string{"claude", "cursor", "codex", "pi"}
	tools := allTools
	if *toolFlag != "" {
		valid := false
		for _, t := range allTools {
			if t == *toolFlag {
				valid = true
			}
		}
		if !valid {
			fmt.Fprintf(os.Stderr, "[error] unknown -tool %q (want one of claude, cursor, codex, pi)\n", *toolFlag)
			os.Exit(2)
		}
		tools = []string{*toolFlag}
	}

	pc := osPathChecker{}
	pr := osPathResolver{}
	fr := osFileReader{}
	ic := newRealInstalledChecker(pr, pc)

	claude := NewClaudeScanner(fr, pc, pr, ic, *home)
	cursor := NewCursorScanner(fr, pc, pr, ic, *home)
	codex := NewCodexScanner(newRealTomlReader(pr), fr, pc, pr, ic, *home)
	pi := NewPiScanner(fr, pc, ic, *home)

	scanners := map[string]scanner{
		"claude": claude,
		"cursor": cursor,
		"codex":  codex,
		"pi":     pi,
	}

	var findings []Finding
	scanErrored := false
	for _, t := range tools {
		fs, err := scanners[t].Scan()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: %v\n", t, err)
			scanErrored = true
			continue
		}
		findings = append(findings, fs...)
	}
	SortFindings(findings)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			fmt.Fprintf(os.Stderr, "[error] encode: %v\n", err)
			os.Exit(1)
		}
	} else {
		PrintReport(os.Stdout, tools, findings)
	}

	applyFailed := false
	if *apply && len(findings) > 0 {
		applied, failed := 0, 0
		for _, f := range findings {
			var err error
			switch f.Tool {
			case "claude":
				err = claude.RemoveFinding(f)
			case "cursor":
				err = cursor.RemoveFinding(f)
			case "codex":
				err = codex.RemoveFinding(f)
			case "pi":
				err = pi.RemoveFinding(f)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "[error] apply %s/%s: %v\n", f.Tool, f.Name, err)
				failed++
				continue
			}
			fmt.Printf("[apply] %s: removed %s (%s)\n", f.Tool, f.Name, f.ConfigFile)
			applied++
		}
		fmt.Printf("%d applied, %d failed.\n", applied, failed)
		applyFailed = failed > 0
	}

	if scanErrored || applyFailed {
		os.Exit(1)
	}
}

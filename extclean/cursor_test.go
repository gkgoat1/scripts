package main

import (
	"os"
	"path/filepath"
	"testing"
)

func cursorPaths(home string) (extensionsJSON, extensionsDir, mcpJSON string) {
	dir := filepath.Join(home, ".cursor", "extensions")
	return filepath.Join(dir, "extensions.json"), dir, filepath.Join(home, ".cursor", "mcp.json")
}

func TestCursorScannerHealthyExtension(t *testing.T) {
	home := "/home/g"
	extJSON, extDir, _ := cursorPaths(home)
	fr := newFakeFileReader()
	fr.setFile(extJSON, `[{"identifier":{"id":"rust-lang.rust-analyzer"},"relativeLocation":"rust-lang.rust-analyzer-0.3.2963"}]`)
	pc := newFakePathChecker()
	pc.setExists(extJSON)
	pc.setExists(filepath.Join(extDir, "rust-lang.rust-analyzer-0.3.2963", "package.json"))

	s := NewCursorScanner(fr, pc, newFakePathResolver(), fakeInstalledChecker{cursor: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

func TestCursorScannerDanglingExtensionMissingPackageJSON(t *testing.T) {
	home := "/home/g"
	extJSON, extDir, _ := cursorPaths(home)
	fr := newFakeFileReader()
	fr.setFile(extJSON, `[{"identifier":{"id":"stale.ext"},"relativeLocation":"stale.ext-0.0.1"}]`)
	pc := newFakePathChecker()
	pc.setExists(extJSON)
	// The directory itself exists (stale leftover) but has no package.json.
	pc.setExists(filepath.Join(extDir, "stale.ext-0.0.1"))

	s := NewCursorScanner(fr, pc, newFakePathResolver(), fakeInstalledChecker{cursor: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindExtension {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestCursorScannerDanglingMCPServer(t *testing.T) {
	home := "/home/g"
	_, _, mcpJSON := cursorPaths(home)
	fr := newFakeFileReader()
	fr.setFile(mcpJSON, `{"mcpServers": {"bad": {"command": "/nope"}}}`)
	pc := newFakePathChecker()
	pc.setExists(mcpJSON)

	s := NewCursorScanner(fr, pc, newFakePathResolver(), fakeInstalledChecker{cursor: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindMCPServer {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestCursorScannerOrphanedWholeFile(t *testing.T) {
	home := "/home/g"
	extJSON, extDir, mcpJSON := cursorPaths(home)
	fr := newFakeFileReader()
	fr.setFile(extJSON, `[{"identifier":{"id":"rust-lang.rust-analyzer"},"relativeLocation":"rust-lang.rust-analyzer-0.3.2963"}]`)
	fr.setFile(mcpJSON, `{"mcpServers": {"lean-lsp": {"command": "uvx"}}}`)
	pc := newFakePathChecker()
	pc.setExists(extJSON)
	pc.setExists(mcpJSON)
	pc.setExists(filepath.Join(extDir, "rust-lang.rust-analyzer-0.3.2963", "package.json"))
	pr := newFakePathResolver()
	pr.setResolves("uvx", "/home/g/.local/bin/uvx")

	s := NewCursorScanner(fr, pc, pr, fakeInstalledChecker{cursor: false}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2 (extension + mcp, both orphaned): %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Reason != ReasonOrphaned {
			t.Errorf("finding %+v: want ReasonOrphaned", f)
		}
	}
}

func TestCursorScannerApplyRemovesDanglingExtension(t *testing.T) {
	dir := t.TempDir()
	extJSON, extDir, _ := cursorPaths(dir)
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `[{"identifier":{"id":"good.ext"},"relativeLocation":"good.ext-1.0"},{"identifier":{"id":"stale.ext"},"relativeLocation":"stale.ext-0.0.1"}]`
	if err := os.WriteFile(extJSON, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(extDir, "good.ext-1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "good.ext-1.0", "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	pc := newFakePathChecker()
	pc.setExists(extJSON)
	pc.setExists(filepath.Join(extDir, "good.ext-1.0", "package.json"))

	s := NewCursorScanner(osFileReader{}, pc, newFakePathResolver(), fakeInstalledChecker{cursor: true}, dir)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if err := s.RemoveFinding(findings[0]); err != nil {
		t.Fatalf("RemoveFinding: %v", err)
	}
	findings, err = s.Scan()
	if err != nil {
		t.Fatalf("re-Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings after apply = %+v", findings)
	}
}

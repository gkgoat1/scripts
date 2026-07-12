package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiScannerDanglingPackage(t *testing.T) {
	home := "/home/g"
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{"packages": ["npm:pi-blackhole"]}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	// no package.json present for pi-blackhole -> dangling

	s := NewPiScanner(fr, pc, fakeInstalledChecker{pi: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Reason != ReasonDangling || f.Name != "pi-blackhole" || f.Tool != "pi" {
		t.Errorf("finding = %+v", f)
	}
}

func TestPiScannerScopedPackageMissing(t *testing.T) {
	home := "/home/g"
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{"packages": ["npm:@gintasz/pi-neuralyzer"]}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)

	s := NewPiScanner(fr, pc, fakeInstalledChecker{pi: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Name != "@gintasz/pi-neuralyzer" {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestPiScannerHealthyPackage(t *testing.T) {
	home := "/home/g"
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{"packages": ["npm:pi-blackhole"]}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	pc.setExists(filepath.Join(home, ".pi", "agent", "npm", "node_modules", "pi-blackhole", "package.json"))

	s := NewPiScanner(fr, pc, fakeInstalledChecker{pi: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("got %d findings, want 0 (healthy): %+v", len(findings), findings)
	}
}

func TestPiScannerOrphanedWholeFile(t *testing.T) {
	home := "/home/g"
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{"packages": ["npm:pi-blackhole", "npm:pi-tool-repair"]}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	// even though the node_modules dirs DO exist, pi itself isn't installed,
	// so every entry should be flagged orphaned rather than dangling-checked.
	pc.setExists(filepath.Join(home, ".pi", "agent", "npm", "node_modules", "pi-blackhole", "package.json"))
	pc.setExists(filepath.Join(home, ".pi", "agent", "npm", "node_modules", "pi-tool-repair", "package.json"))

	s := NewPiScanner(fr, pc, fakeInstalledChecker{pi: false}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Reason != ReasonOrphaned {
			t.Errorf("finding %+v: want ReasonOrphaned", f)
		}
	}
}

func TestPiScannerNoSettingsFileNoFindings(t *testing.T) {
	home := "/home/g"
	fr := newFakeFileReader()
	pc := newFakePathChecker()

	s := NewPiScanner(fr, pc, fakeInstalledChecker{pi: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none when settings.json absent", findings)
	}
}

func TestPiScannerApplyRemovesDanglingPackage(t *testing.T) {
	dir := t.TempDir()
	home := dir
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"packages": ["npm:pi-blackhole", "npm:dangling-pkg"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	pc.setExists(filepath.Join(home, ".pi", "agent", "npm", "node_modules", "pi-blackhole", "package.json"))

	s := NewPiScanner(osFileReader{}, pc, fakeInstalledChecker{pi: true}, home)
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

	// Re-scan to confirm it's gone and the healthy package survived.
	findings, err = s.Scan()
	if err != nil {
		t.Fatalf("re-Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings after apply = %+v, want none", findings)
	}
}

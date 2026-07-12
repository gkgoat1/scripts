package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFileAt(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestRemoveTomlTableMiddle(t *testing.T) {
	content := "[marketplaces.a]\nsource = \"/x\"\n\n[mcp_servers.foo]\ncommand = \"/y\"\n\n[plugins.\"z@w\"]\nenabled = true\n"
	got, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err != nil {
		t.Fatalf("RemoveTomlTable: %v", err)
	}
	want := "[marketplaces.a]\nsource = \"/x\"\n\n[plugins.\"z@w\"]\nenabled = true\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveTomlTableAtStart(t *testing.T) {
	content := "[mcp_servers.foo]\ncommand = \"/y\"\n\n[marketplaces.a]\nsource = \"/x\"\n"
	got, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err != nil {
		t.Fatalf("RemoveTomlTable: %v", err)
	}
	want := "[marketplaces.a]\nsource = \"/x\"\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveTomlTableAtEndFollowedByEOF(t *testing.T) {
	content := "[marketplaces.a]\nsource = \"/x\"\n\n[mcp_servers.foo]\ncommand = \"/y\"\n"
	got, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err != nil {
		t.Fatalf("RemoveTomlTable: %v", err)
	}
	want := "[marketplaces.a]\nsource = \"/x\"\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveTomlTableFollowedByNestedSubtableThenSibling(t *testing.T) {
	content := "[mcp_servers.foo]\ncommand = \"/y\"\n\n[mcp_servers.foo.env]\nFOO = \"bar\"\n\n[marketplaces.a]\nsource = \"/x\"\n"
	got, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err != nil {
		t.Fatalf("RemoveTomlTable: %v", err)
	}
	// Documented limitation: the nested "[mcp_servers.foo.env]" subtable is
	// NOT removed along with its parent -- it also matches the "next
	// top-level header" boundary and ends the excision there.
	want := "[mcp_servers.foo.env]\nFOO = \"bar\"\n\n[marketplaces.a]\nsource = \"/x\"\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveTomlTableDuplicateHeaderErrors(t *testing.T) {
	content := "[mcp_servers.foo]\ncommand = \"/y\"\n\n[mcp_servers.foo]\ncommand = \"/z\"\n"
	_, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err == nil {
		t.Fatal("want error for duplicate header")
	}
	if !strings.Contains(err.Error(), "appears") {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveTomlTableMissingHeaderErrors(t *testing.T) {
	content := "[marketplaces.a]\nsource = \"/x\"\n"
	_, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err == nil {
		t.Fatal("want error for missing header")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveTomlTablePreservesContentOutsideRemovedSpanByteExact(t *testing.T) {
	before := "# top comment\n[marketplaces.a]\n# a comment about a\nsource = \"/x\"\n"
	after := "[plugins.\"z@w\"]\nenabled = true\n# trailing comment\n"
	content := before + "\n[mcp_servers.foo]\ncommand = \"/y\"\n\n" + after

	got, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err != nil {
		t.Fatalf("RemoveTomlTable: %v", err)
	}
	want := before + "\n" + after
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveTomlTableNoTrailingNewlinePreserved(t *testing.T) {
	content := "[mcp_servers.foo]\ncommand = \"/y\"\n\n[marketplaces.a]\nsource = \"/x\""
	got, err := RemoveTomlTable([]byte(content), "[mcp_servers.foo]")
	if err != nil {
		t.Fatalf("RemoveTomlTable: %v", err)
	}
	want := "[marketplaces.a]\nsource = \"/x\""
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func codexConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}

func TestCodexScannerDanglingMarketplace(t *testing.T) {
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{"marketplaces": {"stale-marketplace": {"source": "/nonexistent"}}}`)
	pc := newFakePathChecker()
	pc.setExists(path)

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindMarketplace {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestCodexScannerHealthyMarketplace(t *testing.T) {
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{"marketplaces": {"openai-bundled": {"source": "/home/g/.codex/.tmp/bundled-marketplaces/openai-bundled"}}}`)
	pc := newFakePathChecker()
	pc.setExists(path)
	pc.setExists("/home/g/.codex/.tmp/bundled-marketplaces/openai-bundled")

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

func TestCodexScannerDanglingPluginCacheDir(t *testing.T) {
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{"plugins": {"documents@openai-primary-runtime": {"enabled": true}}}`)
	pc := newFakePathChecker()
	pc.setExists(path)
	// cache dir intentionally absent

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindPlugin {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestCodexScannerHealthyPluginCacheDir(t *testing.T) {
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{"plugins": {"documents@openai-primary-runtime": {"enabled": true}}}`)
	pc := newFakePathChecker()
	pc.setExists(path)
	pc.setNonEmptyDir(filepath.Join(home, ".codex", "plugins", "cache", "openai-primary-runtime", "documents"))

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

func TestCodexScannerDanglingMCPServerRelativeCwd(t *testing.T) {
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{"mcp_servers": {"computer-use": {"command": "./Codex Computer Use.app/bin/SkyComputerUseClient", "cwd": ".", "enabled": false}}}`)
	pc := newFakePathChecker()
	pc.setExists(path)
	// deliberately do NOT mark the resolved path as existing -> dangling

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindMCPServer {
		t.Fatalf("findings = %+v", findings)
	}
	if !strings.Contains(findings[0].Detail, "currently disabled") {
		t.Errorf("detail = %q, want disabled-suffix note", findings[0].Detail)
	}
}

func TestCodexScannerHealthyMCPServerRelativeCwdResolvesAgainstPerServerDir(t *testing.T) {
	// Mirrors the real "computer-use" entry found on this machine: its
	// command only resolves from ~/.codex/computer-use/, not ~/.codex/
	// directly, despite cwd being ".".
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{"mcp_servers": {"computer-use": {"command": "./Codex Computer Use.app/bin/SkyComputerUseClient", "cwd": "."}}}`)
	pc := newFakePathChecker()
	pc.setExists(path)
	pc.setExists(filepath.Join(home, ".codex", "computer-use", "./Codex Computer Use.app/bin/SkyComputerUseClient"))

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none (path resolves against ~/.codex)", findings)
	}
}

func TestCodexScannerOrphanedWholeFile(t *testing.T) {
	home := "/home/g"
	path := codexConfigPath(home)
	tr := newFakeTomlReader()
	tr.setJSON(path, `{
		"marketplaces": {"openai-bundled": {"source": "/home/g/.codex/.tmp/bundled-marketplaces/openai-bundled"}},
		"mcp_servers": {"node_repl": {"command": "/bin/true"}}
	}`)
	pc := newFakePathChecker()
	pc.setExists(path)
	pc.setExists("/home/g/.codex/.tmp/bundled-marketplaces/openai-bundled")
	pc.setExists("/bin/true")

	s := NewCodexScanner(tr, newFakeFileReader(), pc, newFakePathResolver(), fakeInstalledChecker{codex: false}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2 (both orphaned despite resolving fine): %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Reason != ReasonOrphaned {
			t.Errorf("finding %+v: want ReasonOrphaned", f)
		}
	}
}

func TestCodexScannerNoConfigNoFindings(t *testing.T) {
	s := NewCodexScanner(newFakeTomlReader(), newFakeFileReader(), newFakePathChecker(), newFakePathResolver(), fakeInstalledChecker{codex: true}, "/home/g")
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none when config.toml absent", findings)
	}
}

func TestCodexScannerApplyRemovesDanglingMCPServer(t *testing.T) {
	dir := t.TempDir()
	path := codexConfigPath(dir)
	content := "[marketplaces.openai-bundled]\nsource = \"/x\"\n\n[mcp_servers.dangling]\ncommand = \"/nope\"\n"
	if err := writeTestFileAt(path, content); err != nil {
		t.Fatal(err)
	}
	loc := Locator{TOMLTable: &TOMLTableLocator{Header: "[mcp_servers.dangling]"}}
	f := Finding{Tool: "codex", Kind: KindMCPServer, Name: "dangling", ConfigFile: path, Reason: ReasonDangling, Locator: loc}

	s := NewCodexScanner(newFakeTomlReader(), osFileReader{}, newFakePathChecker(), newFakePathResolver(), fakeInstalledChecker{codex: true}, dir)
	if err := s.RemoveFinding(f); err != nil {
		t.Fatalf("RemoveFinding: %v", err)
	}

	out, err := osFileReader{}.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "[marketplaces.openai-bundled]\nsource = \"/x\"\n"
	if string(out) != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

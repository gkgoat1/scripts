package main

import (
	"os"
	"path/filepath"
	"testing"
)

func claudePaths(home string) (settings, appJSON, plugins, marketplaces string) {
	return filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")
}

func TestClaudeScannerDanglingHook(t *testing.T) {
	home := "/home/g"
	settingsPath, _, _, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{
		"hooks": {
			"UserPromptSubmit": [
				{"matcher": "", "hooks": [{"type": "command", "command": "DEVROULETTE_HOOK=1 /opt/homebrew/Cellar/node/26.3.0/bin/node /opt/homebrew/lib/node_modules/devroulette-cli/dist/cli/src/hook-runner.js start"}]}
			]
		}
	}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	pc.setExists("/opt/homebrew/lib/node_modules/devroulette-cli/dist/cli/src/hook-runner.js")
	pr := newFakePathResolver()

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Kind != KindHook || f.Reason != ReasonDangling {
		t.Errorf("finding = %+v", f)
	}
}

func TestClaudeScannerHealthyHook(t *testing.T) {
	home := "/home/g"
	settingsPath, _, _, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}
			]
		}
	}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	pr := newFakePathResolver()
	pr.setResolves("rtk", "/opt/homebrew/bin/rtk")

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("got %d findings, want 0: %+v", len(findings), findings)
	}
}

func TestClaudeScannerDanglingMCPServer(t *testing.T) {
	home := "/home/g"
	_, appJSON, _, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(appJSON, `{"mcpServers": {"bad": {"command": "/opt/nonexistent/bin/tool"}}}`)
	pc := newFakePathChecker()
	pc.setExists(appJSON)
	pr := newFakePathResolver()

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindMCPServer {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestClaudeScannerHealthyMCPServer(t *testing.T) {
	home := "/home/g"
	_, appJSON, _, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(appJSON, `{"mcpServers": {"lean-lsp": {"command": "uvx"}}}`)
	pc := newFakePathChecker()
	pc.setExists(appJSON)
	pr := newFakePathResolver()
	pr.setResolves("uvx", "/home/g/.local/bin/uvx")

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestClaudeScannerPerProjectMCPServer(t *testing.T) {
	home := "/home/g"
	_, appJSON, _, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(appJSON, `{"projects": {"/Users/g/proj": {"mcpServers": {"bad": {"command": "/nope"}}}}}`)
	pc := newFakePathChecker()
	pc.setExists(appJSON)
	pr := newFakePathResolver()

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestClaudeScannerDanglingPlugin(t *testing.T) {
	home := "/home/g"
	_, _, pluginsPath, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(pluginsPath, `{"plugins": {"rust-analyzer-lsp@claude-plugins-official": [{"installPath": "/home/g/.claude/plugins/cache/x/y/1.0.0"}]}}`)
	pc := newFakePathChecker()
	pc.setExists(pluginsPath)
	pr := newFakePathResolver()

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindPlugin {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestClaudeScannerDanglingMarketplace(t *testing.T) {
	home := "/home/g"
	_, _, _, marketplacesPath := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(marketplacesPath, `{"claude-plugins-official": {"installLocation": "/home/g/.claude/plugins/marketplaces/claude-plugins-official"}}`)
	pc := newFakePathChecker()
	pc.setExists(marketplacesPath)
	pr := newFakePathResolver()

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: true}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Kind != KindMarketplace {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestClaudeScannerOrphanedWholeFile(t *testing.T) {
	home := "/home/g"
	settingsPath, appJSON, _, _ := claudePaths(home)
	fr := newFakeFileReader()
	fr.setFile(settingsPath, `{"hooks": {"PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"rtk hook claude"}]}]}}`)
	fr.setFile(appJSON, `{"mcpServers": {"lean-lsp": {"command": "uvx"}}}`)
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	pc.setExists(appJSON)
	pr := newFakePathResolver()
	pr.setResolves("rtk", "/opt/homebrew/bin/rtk")
	pr.setResolves("uvx", "/home/g/.local/bin/uvx")

	s := NewClaudeScanner(fr, pc, pr, fakeInstalledChecker{claude: false}, home)
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2 (hook + mcp, both orphaned): %+v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Reason != ReasonOrphaned {
			t.Errorf("finding %+v: want ReasonOrphaned even though commands resolve fine", f)
		}
	}
}

func TestClaudeScannerNoFilesNoFindings(t *testing.T) {
	s := NewClaudeScanner(newFakeFileReader(), newFakePathChecker(), newFakePathResolver(), fakeInstalledChecker{claude: true}, "/home/g")
	findings, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none when no config files exist", findings)
	}
}

func TestClaudeScannerApplyRemovesHookAndKeepsSiblings(t *testing.T) {
	dir := t.TempDir()
	settingsPath, _, _, _ := claudePaths(dir)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [
					{"type": "command", "command": "rtk hook claude"},
					{"type": "command", "command": "/opt/nonexistent/bin/dangling"}
				]}
			]
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pc := newFakePathChecker()
	pc.setExists(settingsPath)
	pr := newFakePathResolver()
	pr.setResolves("rtk", "/opt/homebrew/bin/rtk")

	s := NewClaudeScanner(osFileReader{}, pc, pr, fakeInstalledChecker{claude: true}, dir)
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
		t.Fatalf("findings after apply = %+v, want none", findings)
	}
}

func TestClaudeScannerApplyRemovesMCPServer(t *testing.T) {
	dir := t.TempDir()
	_, appJSON, _, _ := claudePaths(dir)
	if err := os.MkdirAll(filepath.Dir(appJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"mcpServers": {"good": {"command": "uvx"}, "bad": {"command": "/nope"}}}`
	if err := os.WriteFile(appJSON, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pc := newFakePathChecker()
	pc.setExists(appJSON)
	pr := newFakePathResolver()
	pr.setResolves("uvx", "/home/g/.local/bin/uvx")

	s := NewClaudeScanner(osFileReader{}, pc, pr, fakeInstalledChecker{claude: true}, dir)
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

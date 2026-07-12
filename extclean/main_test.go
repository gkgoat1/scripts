package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func buildExtclean(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "extclean")
	testutil.BuildPackage(t, ".", path)
	return path
}

const danglingHookConfig = `{
	"hooks": {
		"PreToolUse": [
			{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}
		],
		"UserPromptSubmit": [
			{"matcher": "", "hooks": [{"type": "command", "command": "/nonexistent-extclean-test/does-not-exist"}]}
		]
	}
}`

func TestMainDefaultRunReportsAndChangesNothing(t *testing.T) {
	bin := buildExtclean(t)
	home := t.TempDir()
	testutil.WriteTree(t, home, map[string]string{
		".claude/settings.json": danglingHookConfig,
	})
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	res := testutil.RunBinary(bin, "-home", home)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "does-not-exist") {
		t.Errorf("stdout = %q, want it to mention the dangling hook", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "-apply to remove") {
		t.Errorf("stdout = %q, want the -apply hint", res.Stdout)
	}

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("default run modified the config file; want report-only, no changes")
	}
}

func TestMainApplyRemovesFindings(t *testing.T) {
	bin := buildExtclean(t)
	home := t.TempDir()
	testutil.WriteTree(t, home, map[string]string{
		".claude/settings.json": danglingHookConfig,
	})

	res := testutil.RunBinary(bin, "-apply", "-home", home)
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "1 applied, 0 failed.") {
		t.Errorf("stdout = %q, want an apply summary", res.Stdout)
	}

	// Re-scan (no -apply) to confirm the dangling hook is gone.
	res = testutil.RunBinary(bin, "-home", home, "-tool", "claude")
	if res.ExitCode != 0 {
		t.Fatalf("re-scan exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stdout, "does-not-exist") {
		t.Errorf("stdout = %q, want the dangling hook gone after -apply", res.Stdout)
	}
}

func TestMainToolScoping(t *testing.T) {
	bin := buildExtclean(t)
	home := t.TempDir()
	testutil.WriteTree(t, home, map[string]string{
		".claude/settings.json":     danglingHookConfig,
		".pi/agent/settings.json": `{"packages": ["npm:extclean-test-missing-pkg"]}`,
	})

	res := testutil.RunBinary(bin, "-home", home, "-tool", "pi")
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "extclean-test-missing-pkg") {
		t.Errorf("stdout = %q, want the pi finding", res.Stdout)
	}
	if strings.Contains(res.Stdout, "does-not-exist") {
		t.Errorf("stdout = %q, want claude's finding excluded by -tool pi scoping", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "== pi ==") || strings.Contains(res.Stdout, "== claude ==") {
		t.Errorf("stdout = %q, want only the pi section", res.Stdout)
	}
}

func TestMainBadToolValueExitsTwo(t *testing.T) {
	bin := buildExtclean(t)
	home := t.TempDir()

	res := testutil.RunBinary(bin, "-home", home, "-tool", "bogus")
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", res.ExitCode, res.Stderr)
	}
}

func TestMainCorruptJSONReportedAsScanErrorOthersStillRun(t *testing.T) {
	bin := buildExtclean(t)
	home := t.TempDir()
	testutil.WriteTree(t, home, map[string]string{
		".claude/settings.json":     "{ not valid json",
		".pi/agent/settings.json": `{"packages": ["npm:extclean-test-missing-pkg"]}`,
	})

	res := testutil.RunBinary(bin, "-home", home)
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1; stdout = %s\nstderr = %s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "claude") {
		t.Errorf("stderr = %q, want it to name the tool whose scan failed", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "extclean-test-missing-pkg") {
		t.Errorf("stdout = %q, want pi's finding still reported despite claude's scan error", res.Stdout)
	}
}

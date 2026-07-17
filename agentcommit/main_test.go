package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func buildAgentcommit(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agentcommit")
	testutil.BuildPackage(t, ".", path)
	return path
}

func TestCommitEndToEndPrintsHexRootAndWritesSidecars(t *testing.T) {
	bin := buildAgentcommit(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "interpose"), 0o755); err != nil {
		t.Fatal(err)
	}
	pulseCfg := filepath.Join(home, "jobs")
	if err := os.WriteFile(pulseCfg, []byte("job: a\ninterval: 1m\ncommand: echo a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "commit", "-pulse-config", pulseCfg)
	cmd.Env = testutil.EnvWith("HOME", home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	root := strings.TrimSpace(string(out))
	if len(root) != 64 { // 32 bytes hex-encoded
		t.Errorf("stdout = %q, want a 64-char hex root", root)
	}
	if _, err := os.Stat(pulseCfg + ".proof"); err != nil {
		t.Errorf("pulse proof sidecar not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "interpose", "config.proof")); err != nil {
		t.Errorf("interpose proof sidecar not written: %v", err)
	}
}

func TestAnchorSubcommandValidRoot(t *testing.T) {
	bin := buildAgentcommit(t)
	res := testutil.RunBinary(bin, "anchor", "-root", strings.Repeat("ab", 32))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "[anchor]") {
		t.Errorf("stdout = %q, want an [anchor] line", res.Stdout)
	}
}

func TestAnchorSubcommandMissingRootExitsUsageError(t *testing.T) {
	bin := buildAgentcommit(t)
	res := testutil.RunBinary(bin, "anchor")
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", res.ExitCode, res.Stderr)
	}
}

func TestNoSubcommandExitsUsageError(t *testing.T) {
	bin := buildAgentcommit(t)
	res := testutil.RunBinary(bin)
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", res.ExitCode, res.Stderr)
	}
}

func TestUnknownSubcommandExitsUsageError(t *testing.T) {
	bin := buildAgentcommit(t)
	res := testutil.RunBinary(bin, "bogus")
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", res.ExitCode, res.Stderr)
	}
}

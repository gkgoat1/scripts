package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestInterposeGitDelegates(t *testing.T) {
	root := t.TempDir()
	interposerDir := filepath.Join(root, "interposers")
	stubDir := filepath.Join(root, "stubs")
	logFile := filepath.Join(root, "log.txt")
	if err := os.MkdirAll(interposerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	interposeBin := filepath.Join(interposerDir, "git")
	testutil.BuildPackage(t, "../interpose", interposeBin)

	stub := filepath.Join(stubDir, "git")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", interposerDir+string(os.PathListSeparator)+stubDir)
	t.Setenv("INTERPOSE_SELF", interposeBin)
	res := testutil.RunBinary(interposeBin, "status", "-s")
	if res.ExitCode != 0 {
		t.Fatalf("exit %d stderr=%q", res.ExitCode, res.Stderr)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status") {
		t.Errorf("log = %q", data)
	}
}

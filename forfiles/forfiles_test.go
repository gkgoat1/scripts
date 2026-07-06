package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestForfilesSubstitutesAndRuns(t *testing.T) {
	bin := buildForfiles(t)
	input := strings.NewReader("a\nb\n")
	cmd := exec.Command(bin, "^", "echo", "^")
	cmd.Stdin = input
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "a") || !strings.Contains(string(out), "b") {
		t.Errorf("output = %q", out)
	}
}

func TestForfilesEmptyStdin(t *testing.T) {
	bin := buildForfiles(t)
	cmd := exec.Command(bin, "^", "echo", "^")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("output = %q want empty", out)
	}
}

func TestForfilesCommandFailure(t *testing.T) {
	bin := buildForfiles(t)
	cmd := exec.Command(bin, "^", "sh", "-c", "exit 1")
	cmd.Stdin = strings.NewReader("x\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if !strings.Contains(stderr.String(), "command execution error") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func buildForfiles(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forfiles")
	testutil.BuildPackage(t, ".", path)
	return path
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestCleanAllRemovesArtifacts(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "proj", "target", "debug")
	nm := filepath.Join(root, "app", "node_modules", "pkg")
	keep := filepath.Join(root, "app", "src", "main.rs")
	testutil.MkdirAll(t, target, nm, filepath.Dir(keep))
	testutil.WriteFile(t, keep, "fn main(){}")

	if err := cleanAll(root); err != nil {
		t.Fatal(err)
	}
	if testutil.PathExists(target) {
		t.Error("target still exists")
	}
	if testutil.PathExists(nm) {
		t.Error("node_modules still exists")
	}
	if !testutil.PathExists(keep) {
		t.Error("source removed")
	}
}

func TestCleanAllSkipsUnrelated(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "build")
	testutil.MkdirAll(t, other)
	if err := cleanAll(root); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(other) {
		t.Error("build removed")
	}
}

func TestCleanRepoRoots(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "alpha")
	repo := filepath.Join(proj, "svc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteTree(t, root, map[string]string{
		"alpha/.prtag": "alpha:\n---\n[metadata]\n",
	})
	testutil.InitRepo(t, repo)
	target := filepath.Join(repo, "target", "out")
	nested := filepath.Join(proj, "target", "stray")
	testutil.MkdirAll(t, target, nested)

	if err := cleanRepoRoots(root); err != nil {
		t.Fatal(err)
	}
	if testutil.PathExists(target) {
		t.Error("repo target still exists")
	}
	if !testutil.PathExists(nested) {
		t.Error("non-repo target removed")
	}
}

func TestRemovePathsListsEach(t *testing.T) {
	root := t.TempDir()
	p1 := filepath.Join(root, "a")
	p2 := filepath.Join(root, "b")
	testutil.MkdirAll(t, p1, p2)
	r := captureStdout(t, func() {
		if err := removePaths([]string{p1, p2}); err != nil {
			t.Fatal(err)
		}
	})
	if !containsLine(r, p1) || !containsLine(r, p2) {
		t.Errorf("output = %q", r)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func containsLine(out, want string) bool {
	for _, line := range splitLines(out) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

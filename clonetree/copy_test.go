package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestIsUnderRepo(t *testing.T) {
	repos := []string{"/root/proj/app", "/root/other"}

	cases := []struct {
		path string
		want bool
	}{
		{"/root/proj/app", false},
		{"/root/proj/app/src", true},
		{"/root/proj/config.yaml", false},
		{"/root/other/pkg", true},
		{"/root/unrelated", false},
	}

	for _, tc := range cases {
		got := isUnderRepo(tc.path, repos)
		if got != tc.want {
			t.Errorf("isUnderRepo(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsRepoRoot(t *testing.T) {
	repos := []string{"/root/app"}
	if !isRepoRoot("/root/app", repos) {
		t.Fatal("expected repo root")
	}
	if isRepoRoot("/root/app/src", repos) {
		t.Fatal("subdir is not repo root")
	}
}

func TestCopyNonRepoFiles(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	mustWrite(t, filepath.Join(src, ".prtag"), "p:\n")
	mustWrite(t, filepath.Join(src, "notes.txt"), "hello\n")
	mustMkdir(t, filepath.Join(src, "shared"))
	mustWrite(t, filepath.Join(src, "shared", "config.yaml"), "cfg\n")

	repo := filepath.Join(src, "app")
	mustMkdir(t, repo)
	mustWrite(t, filepath.Join(repo, "inside.txt"), "secret\n")
	testutil.InitRepo(t, repo)

	repos := []string{repo}
	if err := copyNonRepoFiles(src, dest, repos, false, false, false); err != nil {
		t.Fatal(err)
	}

	assertFile(t, filepath.Join(dest, "notes.txt"), "hello\n")
	assertFile(t, filepath.Join(dest, "shared", "config.yaml"), "cfg\n")
	assertNoFile(t, filepath.Join(dest, ".prtag"))
	assertNoFile(t, filepath.Join(dest, "app", "inside.txt"))
}

func TestCopySkipsExistingWithoutForce(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()

	mustWrite(t, filepath.Join(src, "notes.txt"), "new\n")
	mustWrite(t, filepath.Join(dest, "notes.txt"), "old\n")

	if err := copyNonRepoFiles(src, dest, nil, false, false, false); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dest, "notes.txt"), "old\n")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q, want %q", path, string(b), want)
	}
}

func assertNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected missing file %s", path)
	}
}

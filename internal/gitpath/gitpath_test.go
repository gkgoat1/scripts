package gitpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLocalRemote(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := ResolveLocalRemote(dir, repo)
	if !ok {
		t.Fatal("expected local remote")
	}
	want, _ := filepath.EvalSymlinks(repo)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	if _, ok := ResolveLocalRemote(dir, "https://github.com/a/b"); ok {
		t.Fatal("expected network remote to fail")
	}
}

func TestIsGitRepo(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Fatal("empty dir is not a repo")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsGitRepo(dir) {
		t.Fatal("dir with .git should be a repo")
	}
}

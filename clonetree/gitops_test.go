package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/internal/gitpath"
	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestMaterializeCloneUnit(t *testing.T) {
	src := t.TempDir()
	destRoot := t.TempDir()
	dest := filepath.Join(destRoot, "copy")
	testutil.InitRepo(t, src)

	state, err := readRepoState(src)
	if err != nil {
		t.Fatal(err)
	}

	g := gitRunner{}
	if err := materializeClone(src, dest, state, false, g); err != nil {
		t.Fatal(err)
	}
	if !gitpath.IsGitRepo(dest) {
		t.Fatal("dest should be a git repo")
	}
	if testutil.Head(t, src) != testutil.Head(t, dest) {
		t.Fatal("HEAD mismatch")
	}
}

func TestMaterializeWorktreeUnit(t *testing.T) {
	src := t.TempDir()
	destRoot := t.TempDir()
	dest := filepath.Join(destRoot, "wt")
	testutil.InitRepo(t, src)

	state, err := readRepoState(src)
	if err != nil {
		t.Fatal(err)
	}

	g := gitRunner{}
	if err := materializeWorktree(src, dest, state, false, g); err != nil {
		t.Fatal(err)
	}
	if !isWorktreeOf(src, dest) {
		t.Fatal("dest should be a worktree of src")
	}
}

func TestUpdateExistingClone(t *testing.T) {
	src := t.TempDir()
	destRoot := t.TempDir()
	testutil.InitRepo(t, src)

	dest := filepath.Join(destRoot, "copy")
	state, err := readRepoState(src)
	if err != nil {
		t.Fatal(err)
	}
	g := gitRunner{}
	if err := materializeClone(src, dest, state, false, g); err != nil {
		t.Fatal(err)
	}

	testutil.WriteFile(t, filepath.Join(src, "f.txt"), "updated\n")
	testutil.Run(t, src, "git", "add", "-A")
	testutil.Run(t, src, "git", "commit", "-q", "-m", "update")

	state, err = readRepoState(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializeClone(src, dest, state, false, g); err != nil {
		t.Fatal(err)
	}
	if testutil.Head(t, src) != testutil.Head(t, dest) {
		t.Fatal("dest should match updated source HEAD")
	}
}

func TestMaterializeCloneForceRemovesNonRepoDir(t *testing.T) {
	src := t.TempDir()
	destRoot := t.TempDir()
	testutil.InitRepo(t, src)

	blocker := filepath.Join(destRoot, "copy")
	if err := os.MkdirAll(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := readRepoState(src)
	if err != nil {
		t.Fatal(err)
	}
	g := gitRunner{}
	if err := materializeClone(src, blocker, state, true, g); err != nil {
		t.Fatal(err)
	}
	if !gitpath.IsGitRepo(blocker) {
		t.Fatal("expected git repo after -force re-clone")
	}
}

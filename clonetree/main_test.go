package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/gitpath"
	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestMaterializeClone(t *testing.T) {
	srcRoot := t.TempDir()
	destRoot := t.TempDir()

	setupProject(t, srcRoot)

	bin := filepath.Join(t.TempDir(), "clonetree")
	testutil.BuildPackage(t, ".", bin)

	res := testutil.RunBinary(bin, "-method", "clone", "-from", "prtag", srcRoot, destRoot)
	if res.ExitCode != 0 {
		t.Fatalf("clonetree failed: %v\nstderr: %s", res.ExitCode, res.Stderr)
	}

	destRepo := filepath.Join(destRoot, "app")
	if !gitpath.IsGitRepo(destRepo) {
		t.Fatal("dest app is not a git repo")
	}

	srcHead := testutil.Head(t, filepath.Join(srcRoot, "app"))
	destHead := testutil.Head(t, destRepo)
	if srcHead != destHead {
		t.Fatalf("HEAD mismatch: src=%s dest=%s", srcHead, destHead)
	}

	origin, err := captureGit(destRepo, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := gitpath.ResolveLocalRemote(destRepo, origin)
	if !ok {
		t.Fatalf("origin %q is not a local remote", origin)
	}
	srcEval, _ := gitpath.EvalRepoPath(filepath.Join(srcRoot, "app"))
	if resolved != srcEval {
		t.Fatalf("origin resolves to %q, want %q", resolved, srcEval)
	}

	assertNoFile(t, filepath.Join(destRoot, ".prtag"))
	assertFile(t, filepath.Join(destRoot, "notes.txt"), "notes\n")
}

func TestMaterializeWorktree(t *testing.T) {
	srcRoot := t.TempDir()
	destRoot := t.TempDir()

	setupProject(t, srcRoot)

	bin := filepath.Join(t.TempDir(), "clonetree")
	testutil.BuildPackage(t, ".", bin)

	res := testutil.RunBinary(bin, "-method", "worktree", "-from", "prtag", srcRoot, destRoot)
	if res.ExitCode != 0 {
		t.Fatalf("clonetree failed: %v\nstderr: %s", res.ExitCode, res.Stderr)
	}

	destRepo := filepath.Join(destRoot, "app")
	if !isWorktreeOf(filepath.Join(srcRoot, "app"), destRepo) {
		out, _ := exec.Command("git", "-C", filepath.Join(srcRoot, "app"), "worktree", "list").CombinedOutput()
		t.Fatalf("dest is not a worktree of source\n%s", out)
	}
}

func TestDryRun(t *testing.T) {
	srcRoot := t.TempDir()
	destRoot := t.TempDir()
	setupProject(t, srcRoot)

	bin := filepath.Join(t.TempDir(), "clonetree")
	testutil.BuildPackage(t, ".", bin)

	res := testutil.RunBinary(bin, "-n", "-method", "clone", "-from", "prtag", srcRoot, destRoot)
	if res.ExitCode != 0 {
		t.Fatalf("dry run failed: %v\nstderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "[copy]") || !strings.Contains(res.Stdout, "[git]") {
		t.Fatalf("expected dry-run output, got:\n%s", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(destRoot, "notes.txt")); err == nil {
		t.Fatal("dry run should not copy files")
	}
}

func TestRerunSkipsExistingNonRepoFiles(t *testing.T) {
	srcRoot := t.TempDir()
	destRoot := t.TempDir()
	setupProject(t, srcRoot)

	bin := filepath.Join(t.TempDir(), "clonetree")
	testutil.BuildPackage(t, ".", bin)

	args := []string{"-method", "clone", "-from", "prtag", srcRoot, destRoot}
	if res := testutil.RunBinary(bin, args...); res.ExitCode != 0 {
		t.Fatalf("first run failed: %s", res.Stderr)
	}

	mustWrite(t, filepath.Join(destRoot, "notes.txt"), "preserved\n")
	if res := testutil.RunBinary(bin, args...); res.ExitCode != 0 {
		t.Fatalf("second run failed: %s", res.Stderr)
	}
	assertFile(t, filepath.Join(destRoot, "notes.txt"), "preserved\n")
}

func setupProject(t *testing.T, root string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, ".prtag"), "proj:\n---\n[metadata]\n")
	mustWrite(t, filepath.Join(root, "notes.txt"), "notes\n")
	testutil.InitRepo(t, filepath.Join(root, "app"))
}

func TestDiscoverAny(t *testing.T) {
	root := t.TempDir()
	testutil.InitRepo(t, filepath.Join(root, "a"))
	testutil.InitRepo(t, filepath.Join(root, "b"))

	repos, err := discoverAny([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2: %v", len(repos), repos)
	}
}

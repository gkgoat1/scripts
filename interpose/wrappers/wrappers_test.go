package wrappers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/wrappers"
)

func TestGitDestructive(t *testing.T) {
	cases := map[string]bool{
		"reset --hard":           true,
		"status":                 false,
		"push --force":           true,
		"checkout -f main":       true,
		"branch -D old":          true,
		"clean -fd":              true,
		"revert HEAD~1":          true,
		"revert HEAD -- file.go": true,
	}
	for cmd, want := range cases {
		args := strings.Fields(cmd)
		if got := wrappers.GitDestructive(args); got != want {
			t.Errorf("%q: got %v want %v", cmd, got, want)
		}
	}
}

func TestGitRestoreTrigger(t *testing.T) {
	cases := map[string]bool{
		"add .":              true,
		"commit -m x":        true,
		"merge main":         true,
		"rebase main":        true,
		"cherry-pick abc123": true,
		"pull":               true,
		"status":             false,
		"push":               false,
		"reset --hard":       false,
	}
	for cmd, want := range cases {
		args := strings.Fields(cmd)
		if got := wrappers.GitRestoreTrigger(args); got != want {
			t.Errorf("%q: got %v want %v", cmd, got, want)
		}
	}
}

func TestGitSnapshotOnReset(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	g := wrappers.Git{}
	ctx := core.NewContext("git", []string{"-C", repo, "reset", "--hard"}, realGit)
	if err := g.Before(ctx); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("git", "-C", repo, "branch", "--list", "interpose/snapshot/*").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Fatal("no snapshot branch created")
	}
}

func TestGitStatusNoSnapshot(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	g := wrappers.Git{}
	ctx := core.NewContext("git", []string{"-C", repo, "status"}, realGit)
	if err := g.Before(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", repo, "branch", "--list", "interpose/snapshot/*").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("unexpected snapshot: %s", out)
	}
}

func TestGitRestoreConflictBeforeAdd(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	testutil.WriteFile(t, filepath.Join(repo, "f.txt"), "clean\n")
	testutil.Run(t, repo, "git", "add", "f.txt")
	testutil.Run(t, repo, "git", "commit", "-m", "clean")
	head := testutil.Head(t, repo)
	testutil.Run(t, repo, "git", "branch", "interpose/snapshot/20260707-000000_main_"+head[:7], "HEAD")

	testutil.WriteFile(t, filepath.Join(repo, "f.txt"), "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> other\n")

	g := wrappers.Git{}
	ctx := core.NewContext("git", []string{"-C", repo, "add", "."}, realGit)
	if err := g.Before(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "clean\n" {
		t.Fatalf("content = %q, want clean", got)
	}
}

func TestGitRestoreConflictBeforeCommit(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	testutil.WriteFile(t, filepath.Join(repo, "f.txt"), "clean\n")
	testutil.Run(t, repo, "git", "add", "f.txt")
	testutil.Run(t, repo, "git", "commit", "-m", "clean")
	head := testutil.Head(t, repo)
	testutil.Run(t, repo, "git", "branch", "interpose/snapshot/20260707-000000_main_"+head[:7], "HEAD")

	testutil.WriteFile(t, filepath.Join(repo, "f.txt"), "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> other\n")

	g := wrappers.Git{}
	ctx := core.NewContext("git", []string{"-C", repo, "commit", "-m", "x"}, realGit)
	if err := g.Before(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(repo, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "clean\n" {
		t.Fatalf("content = %q, want clean", got)
	}
}

func TestTransformFindInjectsPrune(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	out, err := wrappers.TransformFind([]string{home, "-name", "foo"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out, " ")
	if !strings.Contains(joined, "-prune") {
		t.Errorf("args = %v", out)
	}
}

func TestTransformFindNoInterpose(t *testing.T) {
	f := wrappers.Find{}
	ctx := core.NewContext("find", nil, "")
	args := []string{"--no-interpose", "/tmp", "-name", "x"}
	out, err := f.Transform(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(args)-1 {
		t.Errorf("out = %v", out)
	}
}

func TestTransformGrepSkipsProtected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(home, "Library")
	safe := t.TempDir()
	out, err := wrappers.TransformGrep([]string{"-r", "pattern", lib, safe})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out, " ")
	if strings.Contains(joined, lib) {
		t.Errorf("protected path not stripped: %v", out)
	}
	if !strings.Contains(joined, filepath.Base(safe)) {
		t.Errorf("safe path removed: %v", out)
	}
}

func TestTransformGrepAllProtectedErrors(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrappers.TransformGrep([]string{"pattern", filepath.Join(home, "Library")})
	if err == nil {
		t.Fatal("expected error")
	}
}

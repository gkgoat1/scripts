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
		"reset --hard":             true,
		"status":                   false,
		"push --force":             true,
		"checkout -f main":         true,
		"branch -D old":            true,
		"clean -fd":                true,
		"revert HEAD~1":            true,
		"revert HEAD -- file.go":   true,
	}
	for cmd, want := range cases {
		args := strings.Fields(cmd)
		if got := wrappers.GitDestructive(args); got != want {
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

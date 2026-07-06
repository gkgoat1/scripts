package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/interpose/core"
)

func TestResolveRealBinary(t *testing.T) {
	root := t.TempDir()
	interposerDir := filepath.Join(root, "interposers")
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(interposerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}

	interposer := filepath.Join(interposerDir, "git")
	realGit := filepath.Join(realDir, "git")
	for _, p := range []string{interposer, realGit} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", interposerDir+string(os.PathListSeparator)+realDir)
	t.Setenv("INTERPOSE_SELF", interposer)
	got, err := core.ResolveRealBinary("git")
	if err != nil {
		t.Fatal(err)
	}
	realAbs, _ := filepath.EvalSymlinks(realGit)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if gotAbs != realAbs {
		t.Errorf("got %q want %q", gotAbs, realAbs)
	}
}

func TestResolveRealBinaryNotFound(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "only")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "git")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("INTERPOSE_SELF", bin)
	_, err := core.ResolveRealBinary("git")
	if err != core.ErrRealBinaryNotFound {
		t.Errorf("err = %v", err)
	}
}

func TestStripNoInterpose(t *testing.T) {
	out, found := core.StripNoInterpose([]string{"status", "--no-interpose", "-s"})
	if !found || len(out) != 2 {
		t.Errorf("out=%v found=%v", out, found)
	}
}

func TestSubcommand(t *testing.T) {
	if got := core.Subcommand([]string{"-C", "/r", "status"}); got != "status" {
		t.Errorf("got %q", got)
	}
}

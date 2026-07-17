package tcc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/interpose/config"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
)

func TestIsProtected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(home, "Library", "Preferences")
	if !tcc.IsProtected(lib) {
		t.Errorf("%q should be protected", lib)
	}
	tmp := t.TempDir()
	if tcc.IsProtected(tmp) {
		t.Errorf("%q should not be protected", tmp)
	}
}

func TestIsProtectedDirName(t *testing.T) {
	if !tcc.IsProtectedDirName("Library") || !tcc.IsProtectedDirName("Documents") {
		t.Error("expected TCC dir names")
	}
	if tcc.IsProtectedDirName("src") {
		t.Error("src should not be protected")
	}
}

func TestWouldTraverseProtected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !tcc.WouldTraverseProtected(home) {
		t.Error("home should traverse protected")
	}
	if tcc.WouldTraverseProtected(t.TempDir()) {
		t.Error("temp should not traverse protected")
	}
}

func TestDefaultProtectedRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	roots, err := tcc.DefaultProtectedRoots()
	if err != nil {
		t.Fatalf("DefaultProtectedRoots: %v", err)
	}
	want := filepath.Join(home, "Documents")
	found := false
	for _, r := range roots {
		if r == want {
			found = true
		}
	}
	if !found {
		t.Errorf("roots = %v, want it to contain %q", roots, want)
	}
	// Config-independent: setting HOME to a scratch dir with no interpose
	// config at all must not error and must still return the fixed set.
	t.Setenv("HOME", t.TempDir())
	roots2, err := tcc.DefaultProtectedRoots()
	if err != nil {
		t.Fatalf("DefaultProtectedRoots: %v", err)
	}
	if len(roots2) != len(roots) {
		t.Errorf("DefaultProtectedRoots should be independent of $HOME's content, got %v vs %v", roots2, roots)
	}
}

func TestMatchesRoots(t *testing.T) {
	roots := []string{"/Users/g/Documents", "/Users/g/Desktop"}
	if !tcc.MatchesRoots("/Users/g/Documents", roots) {
		t.Error("exact root match should match")
	}
	if !tcc.MatchesRoots("/Users/g/Documents/secret.txt", roots) {
		t.Error("path under a root should match")
	}
	if tcc.MatchesRoots("/Users/g/Downloads", roots) {
		t.Error("unrelated path should not match")
	}
	if tcc.MatchesRoots("/Users/g/Documents-other", roots) {
		t.Error("a sibling with the root as a string prefix (no separator) must not match")
	}
}

func TestExtraProtectedFromConfig(t *testing.T) {
	config.Reset()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".config", "interpose")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "secret")
	if err := os.WriteFile(filepath.Join(cfgDir, "config"), []byte("extra-protected-path: "+extra+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	config.Reset()
	if !tcc.IsProtected(extra) {
		t.Error("extra path should be protected")
	}
	config.Reset()
}

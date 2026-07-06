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

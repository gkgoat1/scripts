package config

import (
	"os"
	"path/filepath"
	"testing"
)

const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestLayoutUsesLogicalHome(t *testing.T) {
	home := t.TempDir()
	l, err := NewLayout(home)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if l.ConfigPath != filepath.Join(canonical, "Library", "Application Support", "sandbox", "config.json") {
		t.Fatal(l.ConfigPath)
	}
	if l.TransientRoot != filepath.Join(canonical, "tmp", "sandbox") {
		t.Fatal(l.TransientRoot)
	}
	if l.AnchorPath != filepath.Join(canonical, "Library", "LaunchAgents", "com.gkgoat.scripts.agentcommit-anchor.plist") {
		t.Fatal(l.AnchorPath)
	}
}

func TestConfigStrictAndAllowsOnlyListedTransition(t *testing.T) {
	c := Config{Version: Version, EnvironmentAllow: map[string][]string{"TOKEN": {digestB}}, HashUpdates: []HashUpdate{{From: digestA, Extensions: []string{".py"}, AllowResult: []string{digestB}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.AllowsUpdate(digestA, ".py", digestB) || c.AllowsUpdate(digestA, ".py", digestA) {
		t.Fatal("unexpected transition authorization")
	}
	if !c.EnvAllowed("TOKEN", digestB) || c.EnvAllowed("TOKEN", digestA) || !c.EnvAllowed("UNCONFIGURED", digestA) {
		t.Fatal("unexpected env authorization")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestAutoInterposeIsStrictlyDarwinAndRegistered(t *testing.T) {
	c := Config{Version: Version, AutoInterpose: AutoInterpose{
		Enabled: true, Platform: "darwin", Commands: []string{"git", "kill"},
		Policy: AutoInterposePolicy{CommandAllowlist: map[string][][]string{"kill": {{"-0", "{pid}"}}}},
	}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.AutoInterpose.Platform = "linux"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted Linux autoInterpose")
	}
	c.AutoInterpose.Platform = "darwin"
	c.AutoInterpose.Commands = []string{"git", "git"}
	if err := c.Validate(); err == nil {
		t.Fatal("accepted duplicate command")
	}
	c.AutoInterpose.Commands = []string{"not-a-wrapper"}
	if err := c.Validate(); err == nil {
		t.Fatal("accepted unknown command")
	}
}

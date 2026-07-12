package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestRealTomlReaderIntegration exercises the actual python3/tomllib
// subprocess path (not a fake), so it depends on the test environment
// having a python3 with tomllib available. It skips cleanly rather than
// failing when none is found, keeping the default `go test ./...` hermetic
// while still covering the real bridge when run somewhere that has one.
func TestRealTomlReaderIntegration(t *testing.T) {
	r := newRealTomlReader(osPathResolver{})
	if _, err := r.findInterpreter(); err != nil {
		t.Skipf("no python3 with tomllib available in this environment: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := "[marketplaces.demo]\nsource = \"/tmp/demo\"\n\n[mcp_servers.node_repl]\ncommand = \"/bin/true\"\nenabled = true\n"
	if err := writeTestFileAt(path, content); err != nil {
		t.Fatal(err)
	}

	out, err := r.ReadTomlAsJSON(path)
	if err != nil {
		t.Fatalf("ReadTomlAsJSON: %v", err)
	}

	var cfg codexConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, out)
	}
	if cfg.Marketplaces["demo"].Source != "/tmp/demo" {
		t.Errorf("marketplaces.demo.source = %q", cfg.Marketplaces["demo"].Source)
	}
	if cfg.MCPServers["node_repl"].Command != "/bin/true" {
		t.Errorf("mcp_servers.node_repl.command = %q", cfg.MCPServers["node_repl"].Command)
	}
}

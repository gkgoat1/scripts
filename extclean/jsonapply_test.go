package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyJSONRemovalMapKey(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "mcp.json", `{"mcpServers": {"good": {"command": "uvx"}, "bad": {"command": "/dangling"}}}`)

	err := ApplyJSONRemoval(osFileReader{}, path, func(root any) (any, bool, error) {
		removed, err := removeMapKey(root, []string{"mcpServers"}, "bad")
		return root, removed, err
	})
	if err != nil {
		t.Fatalf("ApplyJSONRemoval: %v", err)
	}

	out, _ := os.ReadFile(path)
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	servers := decoded["mcpServers"].(map[string]any)
	if _, ok := servers["bad"]; ok {
		t.Error("bad still present")
	}
	if _, ok := servers["good"]; !ok {
		t.Error("good was removed, want it kept")
	}
}

func TestApplyJSONRemovalArrayElementAtRoot(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "extensions.json", `[{"identifier":{"id":"good.ext"}},{"identifier":{"id":"bad.ext"}}]`)

	err := ApplyJSONRemoval(osFileReader{}, path, func(root any) (any, bool, error) {
		return removeArrayElement(root, nil, func(el any) bool {
			m := el.(map[string]any)
			ident := m["identifier"].(map[string]any)
			return ident["id"] == "bad.ext"
		})
	})
	if err != nil {
		t.Fatalf("ApplyJSONRemoval: %v", err)
	}

	out, _ := os.ReadFile(path)
	var decoded []any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("got %d elements, want 1: %v", len(decoded), decoded)
	}
	id := decoded[0].(map[string]any)["identifier"].(map[string]any)["id"]
	if id != "good.ext" {
		t.Errorf("remaining element = %v, want good.ext", id)
	}
}

func TestApplyJSONRemovalNestedArrayElement(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "settings.json", `{"packages": ["npm:good-pkg", "npm:bad-pkg"]}`)

	err := ApplyJSONRemoval(osFileReader{}, path, func(root any) (any, bool, error) {
		return removeArrayElement(root, []string{"packages"}, func(el any) bool {
			return el == "npm:bad-pkg"
		})
	})
	if err != nil {
		t.Fatalf("ApplyJSONRemoval: %v", err)
	}

	out, _ := os.ReadFile(path)
	var decoded map[string]any
	json.Unmarshal(out, &decoded)
	pkgs := decoded["packages"].([]any)
	if len(pkgs) != 1 || pkgs[0] != "npm:good-pkg" {
		t.Errorf("packages = %v", pkgs)
	}
}

func TestApplyJSONRemovalNotFoundErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "mcp.json", `{"mcpServers": {"good": {"command": "uvx"}}}`)

	err := ApplyJSONRemoval(osFileReader{}, path, func(root any) (any, bool, error) {
		removed, err := removeMapKey(root, []string{"mcpServers"}, "already-gone")
		return root, removed, err
	})
	if err == nil {
		t.Error("want error when the entry to remove is not found")
	}

	// File must be untouched.
	out, _ := os.ReadFile(path)
	if string(out) != `{"mcpServers": {"good": {"command": "uvx"}}}` {
		t.Errorf("file was modified despite not-found error: %s", out)
	}
}

func TestApplyJSONRemovalNoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "mcp.json", `{"mcpServers": {"bad": {"command": "/dangling"}}}`)

	err := ApplyJSONRemoval(osFileReader{}, path, func(root any) (any, bool, error) {
		removed, err := removeMapKey(root, []string{"mcpServers"}, "bad")
		return root, removed, err
	})
	if err != nil {
		t.Fatalf("ApplyJSONRemoval: %v", err)
	}
	if _, err := os.Stat(path + ".extclean.tmp"); !os.IsNotExist(err) {
		t.Error("temp file was not cleaned up (should have been renamed over the target)")
	}
}

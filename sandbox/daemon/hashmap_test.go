package main

import (
	"os"
	"path/filepath"
	"testing"

	sandboxconfig "github.com/gkgoat1/scripts/sandbox/config"
	"github.com/gkgoat1/scripts/sandbox/hashmap"
)

func digest(s string) string {
	m := hashmap.Map{Version: hashmap.Version, Files: map[string]string{"/main": s}}
	d, err := m.Digest()
	if err != nil {
		panic(err)
	}
	return d
}

func TestUpdateHashRetainsOriginalAndRequiresResultMap(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main")
	code := filepath.Join(dir, "code.py")
	if err := os.WriteFile(main, []byte("main"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(code, []byte("code"), 0600); err != nil {
		t.Fatal(err)
	}
	m := hashmap.Map{Version: hashmap.Version, Files: map[string]string{}}
	m, err := m.AddPath(main)
	if err != nil {
		t.Fatal(err)
	}
	from, _ := m.Digest()
	candidate, err := m.AddPath(code)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := candidate.Digest()
	s := &server{procs: map[int]process{7: {pid: 7, hash: from, files: m}}, policyActive: true,
		policy: sandboxconfig.Config{Version: sandboxconfig.Version, HashUpdates: []sandboxconfig.HashUpdate{{From: from, Extensions: []string{".py"}, AllowResult: []string{want}}}}}
	if !s.updateHash(7, code) {
		t.Fatal("authorized cumulative update denied")
	}
	got := s.procs[7]
	if got.hash != want || len(got.files.Files) != 2 {
		t.Fatalf("process = %#v", got)
	}
}

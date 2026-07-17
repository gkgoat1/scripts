package hashmap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMapDigestIsOrderIndependent(t *testing.T) {
	a := Map{Version: Version, Files: map[string]string{"/b": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "/a": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	b := Map{Version: Version, Files: map[string]string{"/a": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/b": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	da, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatalf("digest differs by input order: %s != %s", da, db)
	}
	got, err := a.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"files":{"/a":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","/b":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"version":1}` {
		t.Fatalf("canonical JSON = %s", got)
	}
}

func TestAddPathUsesAbsoluteResolvedPathAndFullFileHash(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	m, err := (Map{Version: Version, Files: map[string]string{}}).AddPath(link)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalPath(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[canonical]; !ok {
		t.Fatalf("map keys = %#v, want resolved %q", m.Files, canonical)
	}
	if m.Files[canonical] != "d1b2a59fbea7e20077af9f91b27e95e865061b270be03ff539ab3b73587882e8" {
		t.Fatalf("hash = %q", m.Files[canonical])
	}
	var decoded Map
	b, _ := m.CanonicalJSON()
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if !m.Equal(decoded) {
		t.Fatal("canonical map did not round trip")
	}
}

package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanErrorUnwrap(t *testing.T) {
	inner := errors.New("boom")
	se := ScanError{Errors: []error{inner}}
	if !errors.Is(se, inner) {
		t.Error("Unwrap failed")
	}
}

func TestProjectErrorFormat(t *testing.T) {
	pe := ProjectError{ProjectPath: "/p", Err: errors.New("x")}
	if !strings.Contains(pe.Error(), "/p") {
		t.Errorf("error = %q", pe.Error())
	}
}

func TestScanNonDirectoryRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewOSScanner(f).Scan(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("err = %v", err)
	}
}

func TestScanContextCancel(t *testing.T) {
	fsys := newFakeFS(map[string]string{"/root/.prtag": "p:\n"}, []string{"/root"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewScanner("/root", fsys).Scan(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestScanSkipsSymlinkDirs(t *testing.T) {
	s := NewScanner("/root", &symlinkFake{newFakeFS(
		map[string]string{"/root/.prtag": "p:\n"},
		[]string{"/root"},
	)})
	got, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 {
		t.Errorf("projects = %d want 1 (symlink skipped)", len(got.Projects))
	}
}

type symlinkFake struct {
	*fakeFS
}

func (s *symlinkFake) ReadDir(path string) ([]fs.DirEntry, error) {
	entries, err := s.fakeFS.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if path != "/root" {
		return entries, err
	}
	return append(entries, fakeEntry{name: "link", dir: true, symlink: true}), nil
}

func TestNewOSScannerSmoke(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	repo := filepath.Join(proj, "svc")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".prtag"), []byte("proj:\n---\n[metadata]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := NewOSScanner(root).Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Projects) != 1 || len(snap.Projects[0].Repos) != 1 {
		t.Fatalf("snap = %+v", snap)
	}
}

type fakeEntry struct {
	name    string
	dir     bool
	symlink bool
}

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return e.dir }
func (e fakeEntry) Type() fs.FileMode          { if e.symlink { return fs.ModeSymlink } ; if e.dir { return fs.ModeDir }; return 0 }
func (e fakeEntry) Info() (fs.FileInfo, error) { return nil, errors.New("not implemented") }

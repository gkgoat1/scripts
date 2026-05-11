package workspace

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestScan_FindsProjectAndRepos(t *testing.T) {
	fsys := newFakeFS(
		map[string]string{
			"/root/a/.prtag":          "alpha:\nhello\n",
			"/root/a/repo1/readme.md": "x",
			"/root/a/repo3/.gitfile":  "",
		},
		[]string{
			"/root",
			"/root/a",
			"/root/a/repo1",
			"/root/a/repo1/.git",
			"/root/a/repo2",
			"/root/a/repo2/.git",
			"/root/a/repo3",
		},
	)

	s := NewScanner("/root", fsys)
	got, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if len(got.Projects) != 1 {
		t.Fatalf("projects len = %d, want 1", len(got.Projects))
	}

	p := got.Projects[0]
	if p.Path != "/root/a" {
		t.Fatalf("project path = %q, want /root/a", p.Path)
	}
	if p.Tag.Name != "alpha" {
		t.Fatalf("tag name = %q, want alpha", p.Tag.Name)
	}
	if len(p.Repos) != 2 {
		t.Fatalf("repos len = %d, want 2", len(p.Repos))
	}
	if p.Repos[0].Path != "/root/a/repo1" || p.Repos[1].Path != "/root/a/repo2" {
		t.Fatalf("repos = %#v, want repo1/repo2", p.Repos)
	}
}

func TestScan_FindsNestedProjects(t *testing.T) {
	fsys := newFakeFS(
		map[string]string{
			"/root/top/.prtag":            "top:\n",
			"/root/top/subproject/.prtag": "sub:\n",
		},
		[]string{
			"/root", "/root/top", "/root/top/subproject",
		},
	)

	s := NewScanner("/root", fsys)
	got, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("projects len = %d, want 2", len(got.Projects))
	}
	if got.Projects[0].Path != "/root/top" || got.Projects[1].Path != "/root/top/subproject" {
		t.Fatalf("project order/paths = %#v", got.Projects)
	}
}

func TestScan_SortingDeterministic(t *testing.T) {
	fsys := newFakeFS(
		map[string]string{
			"/root/z/.prtag": "z:\n",
			"/root/a/.prtag": "a:\n",
		},
		[]string{
			"/root", "/root/z", "/root/a", "/root/z/r1", "/root/z/r1/.git", "/root/z/r0", "/root/z/r0/.git",
		},
	)

	s := NewScanner("/root", fsys)
	got, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("projects len = %d, want 2", len(got.Projects))
	}
	if got.Projects[0].Path != "/root/a" || got.Projects[1].Path != "/root/z" {
		t.Fatalf("unexpected project ordering: %#v", got.Projects)
	}
	repos := got.Projects[1].Repos
	if len(repos) != 2 || repos[0].Path != "/root/z/r0" || repos[1].Path != "/root/z/r1" {
		t.Fatalf("unexpected repo ordering: %#v", repos)
	}
}

func TestRescan_DiffAddedRemovedChanged(t *testing.T) {
	fs1 := newFakeFS(
		map[string]string{
			"/root/a/.prtag": "a:\none\n",
			"/root/b/.prtag": "b:\n",
		},
		[]string{"/root", "/root/a", "/root/b", "/root/a/repo", "/root/a/repo/.git"},
	)
	s1 := NewScanner("/root", fs1)
	prev, err := s1.Scan(context.Background())
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	fs2 := newFakeFS(
		map[string]string{
			"/root/a/.prtag": "a:\nchanged\n",
			"/root/c/.prtag": "c:\n",
		},
		[]string{"/root", "/root/a", "/root/c", "/root/a/repo2", "/root/a/repo2/.git"},
	)
	s2 := NewScanner("/root", fs2)
	next, diff, err := s2.Rescan(context.Background(), prev)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(next.Projects) != 2 {
		t.Fatalf("next projects len = %d, want 2", len(next.Projects))
	}
	if len(diff.AddedProjects) != 1 || diff.AddedProjects[0].Path != "/root/c" {
		t.Fatalf("added = %#v", diff.AddedProjects)
	}
	if len(diff.RemovedProjectPaths) != 1 || diff.RemovedProjectPaths[0] != "/root/b" {
		t.Fatalf("removed = %#v", diff.RemovedProjectPaths)
	}
	if len(diff.ChangedProjects) != 1 || diff.ChangedProjects[0].Path != "/root/a" {
		t.Fatalf("changed = %#v", diff.ChangedProjects)
	}
}

func TestScan_ProjectParseErrorIncludesPath(t *testing.T) {
	fsys := newFakeFS(
		map[string]string{
			"/root/good/.prtag": "good:\n",
			"/root/bad/.prtag":  "bad\n",
		},
		[]string{
			"/root", "/root/good", "/root/bad",
		},
	)

	s := NewScanner("/root", fsys)
	snap, err := s.Scan(context.Background())
	if err == nil {
		t.Fatalf("Scan: expected error")
	}
	if len(snap.Projects) != 1 || snap.Projects[0].Path != "/root/good" {
		t.Fatalf("expected partial snapshot with good project, got %#v", snap.Projects)
	}

	var scanErr ScanError
	if !errors.As(err, &scanErr) {
		t.Fatalf("error type = %T, want ScanError", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "/root/bad") {
		t.Fatalf("error message missing project path: %q", msg)
	}
}

func TestScan_RootErrorsFast(t *testing.T) {
	fsys := newFakeFS(nil, []string{"/root"})
	s := NewScanner("/missing", fsys)
	_, err := s.Scan(context.Background())
	if err == nil {
		t.Fatalf("expected error for missing root")
	}
}

type fakeFS struct {
	files map[string][]byte
	dirs  map[string]struct{}
}

func newFakeFS(files map[string]string, dirs []string) *fakeFS {
	f := &fakeFS{
		files: map[string][]byte{},
		dirs:  map[string]struct{}{},
	}

	for _, d := range dirs {
		f.addDir(d)
	}
	for p, v := range files {
		clean := cleanPath(p)
		f.files[clean] = []byte(v)
		f.addDir(path.Dir(clean))
	}
	return f
}

func (f *fakeFS) ReadFile(p string) ([]byte, error) {
	clean := cleanPath(p)
	b, ok := f.files[clean]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (f *fakeFS) ReadDir(p string) ([]fs.DirEntry, error) {
	clean := cleanPath(p)
	if _, ok := f.dirs[clean]; !ok {
		return nil, fs.ErrNotExist
	}

	children := map[string]fakeDirEntry{}
	prefix := clean
	if prefix != "/" {
		prefix += "/"
	}

	for d := range f.dirs {
		if d == clean || !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := strings.TrimPrefix(d, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		children[rest] = fakeDirEntry{name: rest, isDir: true}
	}
	for filePath := range f.files {
		if !strings.HasPrefix(filePath, prefix) {
			continue
		}
		rest := strings.TrimPrefix(filePath, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		children[rest] = fakeDirEntry{name: rest, isDir: false}
	}

	entries := make([]fs.DirEntry, 0, len(children))
	for _, entry := range children {
		entryCopy := entry
		entries = append(entries, entryCopy)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (f *fakeFS) Stat(p string) (fs.FileInfo, error) {
	clean := cleanPath(p)
	if _, ok := f.dirs[clean]; ok {
		return fakeFileInfo{name: path.Base(clean), isDir: true}, nil
	}
	if _, ok := f.files[clean]; ok {
		return fakeFileInfo{name: path.Base(clean), isDir: false}, nil
	}
	return nil, fs.ErrNotExist
}

func (f *fakeFS) Join(elem ...string) string {
	return cleanPath(path.Join(elem...))
}

func (f *fakeFS) addDir(p string) {
	clean := cleanPath(p)
	if clean == "." {
		clean = "/"
	}
	parts := strings.Split(clean, "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = current + "/" + part
		f.dirs[current] = struct{}{}
	}
	f.dirs["/"] = struct{}{}
}

type fakeDirEntry struct {
	name  string
	isDir bool
	mode  fs.FileMode
}

func (d fakeDirEntry) Name() string               { return d.name }
func (d fakeDirEntry) IsDir() bool                { return d.isDir }
func (d fakeDirEntry) Type() fs.FileMode          { return d.mode }
func (d fakeDirEntry) Info() (fs.FileInfo, error) { return fakeFileInfo{name: d.name, isDir: d.isDir, mode: d.mode}, nil }

type fakeFileInfo struct {
	name  string
	isDir bool
	mode  fs.FileMode
}

func (i fakeFileInfo) Name() string       { return i.name }
func (i fakeFileInfo) Size() int64        { return 0 }
func (i fakeFileInfo) Mode() fs.FileMode  { return i.mode | boolToModeDir(i.isDir) }
func (i fakeFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i fakeFileInfo) IsDir() bool        { return i.isDir }
func (i fakeFileInfo) Sys() interface{}   { return nil }

func boolToModeDir(isDir bool) fs.FileMode {
	if isDir {
		return fs.ModeDir
	}
	return 0
}

func cleanPath(p string) string {
	if p == "" {
		return "."
	}
	clean := path.Clean(p)
	if clean == "." {
		return "."
	}
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	return clean
}

var _ FS = (*fakeFS)(nil)


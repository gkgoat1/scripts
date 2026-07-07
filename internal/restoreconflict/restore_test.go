package restoreconflict

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

const markerContent = "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> other\n"

func TestHasConflictMarkers(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"markers", markerContent, true},
		{"clean", "hello\nworld\n", false},
		{"inline", "not a <<<<<<< marker\n", false},
		{"ends with gt", ">>>>>>>no space\n", false},
		{"start only", "<<<<<<< HEAD\n", true},
		{"end only", ">>>>>>> branch\n", true},
		{"equals line only", "=======\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasConflictMarkers([]byte(c.data)); got != c.want {
				t.Errorf("hasConflictMarkers(%q) = %v, want %v", c.data, got, c.want)
			}
		})
	}
}

func TestRestoreCleanSnapshot(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	commitFile(t, repo, "f.txt", "clean\n")
	snapshot(t, repo, testutil.Head(t, repo)[:7])

	mustWrite(t, filepath.Join(repo, "f.txt"), markerContent)

	if err := Restore(repo, Options{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := readFile(t, repo, "f.txt")
	if got != "clean\n" {
		t.Fatalf("content = %q, want clean", got)
	}
}

func TestRestorePicksMarkerFreeSnapshotEvenIfNewerHasMarkers(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)

	commitFile(t, repo, "f.txt", "clean\n")
	cleanHead := testutil.Head(t, repo)
	snapshotName(t, repo, "interpose/snapshot/20260707-000000_main_", cleanHead[:7])

	commitFile(t, repo, "f.txt", markerContent)
	markerHead := testutil.Head(t, repo)
	snapshotName(t, repo, "interpose/snapshot/20260707-000001_main_", markerHead[:7])

	// Working tree matches the marker commit.
	if err := Restore(repo, Options{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := readFile(t, repo, "f.txt")
	if got != "clean\n" {
		t.Fatalf("content = %q, want clean", got)
	}
}

func TestRestoreMultipleFilesParallel(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	testutil.WriteFile(t, filepath.Join(repo, "a.txt"), "a clean\n")
	testutil.WriteFile(t, filepath.Join(repo, "b.txt"), "b clean\n")
	testutil.Run(t, repo, "git", "add", "-A")
	testutil.Run(t, repo, "git", "commit", "-q", "-m", "clean")
	snapshot(t, repo, testutil.Head(t, repo)[:7])

	testutil.WriteFile(t, filepath.Join(repo, "a.txt"), markerContent)
	testutil.WriteFile(t, filepath.Join(repo, "b.txt"), markerContent)

	if err := Restore(repo, Options{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if readFile(t, repo, "a.txt") != "a clean\n" {
		t.Errorf("a.txt not restored")
	}
	if readFile(t, repo, "b.txt") != "b clean\n" {
		t.Errorf("b.txt not restored")
	}
}

func TestRestoreSkipsUntracked(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	commitFile(t, repo, "tracked.txt", "clean\n")
	snapshot(t, repo, testutil.Head(t, repo)[:7])

	untracked := filepath.Join(repo, "new.txt")
	mustWrite(t, untracked, markerContent)

	if err := Restore(repo, Options{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := readFile(t, repo, "new.txt"); got != markerContent {
		t.Fatalf("untracked file changed: %q", got)
	}
}

func TestRestoreNoSnapshots(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	commitFile(t, repo, "f.txt", "clean\n")
	mustWrite(t, filepath.Join(repo, "f.txt"), markerContent)

	if err := Restore(repo, Options{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := readFile(t, repo, "f.txt"); got != markerContent {
		t.Fatalf("content changed without snapshots: %q", got)
	}
}

func TestRestoreDryRun(t *testing.T) {
	repo := t.TempDir()
	testutil.InitRepo(t, repo)
	commitFile(t, repo, "f.txt", "clean\n")
	snapshot(t, repo, testutil.Head(t, repo)[:7])
	mustWrite(t, filepath.Join(repo, "f.txt"), markerContent)

	if err := Restore(repo, Options{DryRun: true}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := readFile(t, repo, "f.txt"); got != markerContent {
		t.Fatalf("dry run modified file: %q", got)
	}
}

func commitFile(t *testing.T, repo, rel, content string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(repo, rel), content)
	testutil.Run(t, repo, "git", "add", "-A")
	testutil.Run(t, repo, "git", "commit", "-q", "-m", "commit")
}

func snapshot(t *testing.T, repo, short string) {
	t.Helper()
	snapshotName(t, repo, "interpose/snapshot/20260707-000000_main_", short)
}

func snapshotName(t *testing.T, repo, prefix, short string) {
	t.Helper()
	testutil.Run(t, repo, "git", "branch", prefix+short, "HEAD")
}

func readFile(t *testing.T, repo, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

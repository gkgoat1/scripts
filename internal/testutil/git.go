package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// InitRepo inits a git repo at dir and creates an initial commit.
func InitRepo(t *testing.T, dir string) {
	t.Helper()
	Run(t, "", "git", "init", "-q", dir)
	Run(t, dir, "git", "config", "user.email", "t@t")
	Run(t, dir, "git", "config", "user.name", "t")
	WriteFile(t, filepath.Join(dir, "f.txt"), "init\n")
	Run(t, dir, "git", "add", "-A")
	Run(t, dir, "git", "commit", "-q", "-m", "init")
}

// Head returns the current HEAD sha in repo.
func Head(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in %s: %v", repo, err)
	}
	return strings.TrimSpace(string(out))
}

// Run executes a command, failing the test on error.
func Run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s %v in %s: %v\n%s", name, args, dir, err, out)
	}
}

// WriteFile writes content to path.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// BuildChain creates work --origin--> mirror --origin--> upstream.
func BuildChain(t *testing.T) (work, mirror, upstream string) {
	t.Helper()
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	mirror = filepath.Join(root, "mirror")
	work = filepath.Join(root, "work")

	InitRepo(t, upstream)
	Run(t, root, "git", "clone", "-q", upstream, "mirror")
	Run(t, mirror, "git", "config", "user.email", "t@t")
	Run(t, mirror, "git", "config", "user.name", "t")
	Run(t, root, "git", "clone", "-q", mirror, "work")
	Run(t, work, "git", "config", "user.email", "t@t")
	Run(t, work, "git", "config", "user.name", "t")
	return work, mirror, upstream
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func TestResolveLocalRemote(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	testutil.InitRepo(t, repo)

	cases := []struct {
		name   string
		url    string
		wantOK bool
	}{
		{"absolute", repo, true},
		{"relative", "repo", true},
		{"file scheme", "file://" + repo, true},
		{"tilde", "~/nonexistent-not-a-repo-" + filepath.Base(root), false},
		{"http", "https://github.com/o/r.git", false},
		{"scp", "git@github.com:o/r.git", false},
		{"missing", filepath.Join(root, "missing"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := resolveLocalRemote(root, c.url)
			if ok != c.wantOK {
				t.Errorf("resolveLocalRemote(%q) ok=%v want %v", c.url, ok, c.wantOK)
			}
		})
	}
}

func TestDedupeRepos(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	testutil.InitRepo(t, a)
	testutil.InitRepo(t, b)

	got := dedupeRepos([]string{b, a, b})
	if len(got) != 2 {
		t.Fatalf("len = %d want 2", len(got))
	}
	ga, _ := filepath.EvalSymlinks(got[0])
	gb, _ := filepath.EvalSymlinks(got[1])
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	if ga != ea || gb != eb {
		t.Errorf("order = %v want [%s %s]", got, a, b)
	}
}

func TestDiscoverAnySkipsDotDirs(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "visible")
	testutil.InitRepo(t, repo)
	hidden := filepath.Join(root, ".hidden")
	testutil.MkdirAll(t, hidden)
	testutil.InitRepo(t, filepath.Join(hidden, "inner"))

	repos, err := discoverAny([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0] != repo {
		t.Errorf("repos = %v want [%s]", repos, repo)
	}
}

func TestDiscoverAnySkipsTCCDirs(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Library", "Documents", "Desktop"} {
		dir := filepath.Join(root, name)
		testutil.MkdirAll(t, dir)
		testutil.InitRepo(t, filepath.Join(dir, "proj"))
	}

	repos, err := discoverAny([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Errorf("repos = %v want none", repos)
	}
}

func TestHasGitDirAndIsGitRepo(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	testutil.InitRepo(t, repo)
	if !hasGitDir(repo) {
		t.Error("hasGitDir false")
	}
	if !isGitRepo(repo) {
		t.Error("isGitRepo false")
	}
	if hasGitDir(filepath.Join(root, "nope")) {
		t.Error("hasGitDir true for missing")
	}
}

func TestGithubRepoSlugAndMatchPRBranch(t *testing.T) {
	// covered in pr_test.go; keep package cohesion
	if _, ok := githubRepoSlug("git@github.com:a/b.git"); !ok {
		t.Error("expected ok")
	}
	if n, ok := matchPRBranch("gitall-pr/main-3", "main"); !ok || n != 3 {
		t.Errorf("match = %d %v", n, ok)
	}
}

func TestCLIInvalidAction(t *testing.T) {
	res := testutil.RunBinary(os.Getenv("GITALL_BIN"), "fetch")
	if res.ExitCode != 2 {
		t.Errorf("exit = %d want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "push or pull") {
		t.Errorf("stderr = %q", res.Stderr)
	}
}

func TestCLIInvalidFrom(t *testing.T) {
	res := testutil.RunBinary(os.Getenv("GITALL_BIN"), "-from", "nope", "push", t.TempDir())
	if res.ExitCode != 2 {
		t.Errorf("exit = %d want 2", res.ExitCode)
	}
}

func TestPushAllowMergeAbortsOnConflict(t *testing.T) {
	work, mirror, _ := testutil.BuildChain(t)
	divergentConflict(t, work, mirror)
	workHead := readHead(t, work)

	out, _ := execCombined([]string{"-allow-merge", "push", work})
	if !strings.Contains(out, "[error]") {
		t.Log("output:", out)
	}
	if got := readHead(t, work); got != workHead {
		t.Errorf("HEAD changed: %s -> %s", workHead, got)
	}
	status, err := exec.Command("git", "-C", work, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(status), "<<<<<<<") {
		t.Errorf("merge conflict markers left in tree: %s", status)
	}
}

func execCombined(args []string) (string, int) {
	bin := os.Getenv("GITALL_BIN")
	if bin == "" {
		bin = "/tmp/gitall"
	}
	res := testutil.RunBinary(bin, args...)
	return res.Stdout + res.Stderr, res.ExitCode
}

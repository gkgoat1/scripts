package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeLocalRepo inits a git repo at dir and creates an initial commit.
func makeLocalRepo(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, "", "git", "init", "-q", dir)
	mustRun(t, dir, "git", "config", "user.email", "t@t")
	mustRun(t, dir, "git", "config", "user.name", "t")
	mustWrite(t, filepath.Join(dir, "f.txt"), "init\n")
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "init")
}

// chain: work --origin--> mirror --origin--> upstream
func buildChain(t *testing.T) (work, mirror, upstream string) {
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	mirror = filepath.Join(root, "mirror")
	work = filepath.Join(root, "work")

	makeLocalRepo(t, upstream)
	// mirror clones upstream and is left as a working repo.
	mustRun(t, root, "git", "clone", "-q", upstream, "mirror")
	mustRun(t, mirror, "git", "config", "user.email", "t@t")
	mustRun(t, mirror, "git", "config", "user.name", "t")
	// work clones mirror, then points origin at mirror (local path).
	mustRun(t, root, "git", "clone", "-q", mirror, "work")
	mustRun(t, work, "git", "config", "user.email", "t@t")
	mustRun(t, work, "git", "config", "user.name", "t")
	return work, mirror, upstream
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s %v in %s: %v\n%s", name, args, dir, err, out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readHead(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in %s: %v", repo, err)
	}
	return strings.TrimSpace(string(out))
}

func TestPushPullRecursion(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	// New commit in work: should propagate to mirror then upstream.
	mustWrite(t, filepath.Join(work, "f.txt"), "v2\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "v2")
	workHead := readHead(t, work)

	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	if got := readHead(t, mirror); got != workHead {
		t.Errorf("mirror HEAD after push = %s, want %s", got, workHead)
	}
	if got := readHead(t, upstream); got != workHead {
		t.Errorf("upstream HEAD after push = %s, want %s", got, workHead)
	}

	// New commit in upstream: pull should flow back to mirror then work.
	mustWrite(t, filepath.Join(upstream, "f.txt"), "v3\n")
	mustRun(t, upstream, "git", "commit", "-q", "-am", "v3")
	upHead := readHead(t, upstream)

	if code := run([]string{"pull", work}); code != 0 {
		t.Fatalf("gitall pull exit %d", code)
	}
	if got := readHead(t, mirror); got != upHead {
		t.Errorf("mirror HEAD after pull = %s, want %s", got, upHead)
	}
	if got := readHead(t, work); got != upHead {
		t.Errorf("work HEAD after pull = %s, want %s", got, upHead)
	}
}

func TestSkipUnclean(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	// Dirty work: nothing should propagate.
	mustWrite(t, filepath.Join(work, "f.txt"), "dirty\n")
	workHead := readHead(t, work)

	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	if got := readHead(t, mirror); got != workHead {
		t.Errorf("mirror changed despite dirty work: %s", got)
	}
	if got := readHead(t, upstream); got != workHead {
		t.Errorf("upstream changed despite dirty work: %s", got)
	}
}

func TestPrtagDiscovery(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	// Mark the temp root as a project via .prtag so discovery finds `work`.
	root := filepath.Dir(work)
	prtagPath := filepath.Join(root, ".prtag")
	mustWrite(t, prtagPath, "testproject:\n---\n[metadata]\n")

	// New commit; push via prtag discovery should find work under the marker
	// and propagate to mirror and upstream.
	mustWrite(t, filepath.Join(work, "f.txt"), "vp\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "vp")
	workHead := readHead(t, work)

	if code := run([]string{"-from", "prtag", "push", root}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	if got := readHead(t, mirror); got != workHead {
		t.Errorf("mirror HEAD after prtag push = %s, want %s", got, workHead)
	}
	if got := readHead(t, upstream); got != workHead {
		t.Errorf("upstream HEAD after prtag push = %s, want %s", got, workHead)
	}
}

func TestDryRun(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	mustWrite(t, filepath.Join(work, "f.txt"), "dry\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "dry")
	workHead := readHead(t, work)

	if code := run([]string{"-n", "push", work}); code != 0 {
		t.Fatalf("gitall -n push exit %d", code)
	}
	// Dry run: nothing should actually move.
	if got := readHead(t, mirror); got == workHead {
		t.Errorf("mirror advanced during dry run")
	}
	if got := readHead(t, upstream); got == workHead {
		t.Errorf("upstream advanced during dry run")
	}
}

// run invokes main with the given args and returns the exit code.
func run(args []string) int {
	// main() calls os.Exit; run it in a goroutine by calling an extracted
	// runner. We call main directly and capture panics only for safety.
	oldArgs := os.Args
	os.Args = append([]string{"gitall"}, args...)
	defer func() { os.Args = oldArgs }()
	// Since main calls os.Exit on failure, tests using this helper expect
	// success paths. To avoid killing the test process, we exec the built
	// binary instead.
	return runBinary(args)
}

// runBinary builds and runs the gitall binary as a subprocess so os.Exit in
// main does not terminate the test process.
func runBinary(args []string) int {
	bin := os.Getenv("GITALL_BIN")
	if bin == "" {
		bin = "/tmp/gitall"
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

func TestMain(m *testing.M) {
	// Build the binary once for the test run.
	if err := exec.Command("go", "build", "-o", "/tmp/gitall", "./").Run(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/internal/testutil"
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

func readBranchHead(t *testing.T, repo, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", branch).Output()
	if err != nil {
		t.Fatalf("rev-parse %s in %s: %v", branch, repo, err)
	}
	return strings.TrimSpace(string(out))
}

func currentBranchName(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("current branch in %s: %v", repo, err)
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

func TestPushPullAllBranches(t *testing.T) {
	work, mirror, upstream := buildChain(t)
	base := currentBranchName(t, work)

	// Create a feature branch locally while leaving the base branch checked
	// out. A normal gitall push must propagate both refs through the complete
	// local-remote chain without merging the feature commit into base.
	mustRun(t, work, "git", "checkout", "-q", "-b", "feature")
	mustWrite(t, filepath.Join(work, "feature.txt"), "work feature\n")
	mustRun(t, work, "git", "add", "-A")
	mustRun(t, work, "git", "commit", "-q", "-m", "work feature")
	featureHead := readHead(t, work)
	mustRun(t, work, "git", "checkout", "-q", base)

	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("gitall multi-branch push exit %d", code)
	}
	for _, repo := range []string{mirror, upstream} {
		if got := readBranchHead(t, repo, "feature"); got != featureHead {
			t.Errorf("%s feature = %s, want %s", repo, got, featureHead)
		}
	}
	if got := currentBranchName(t, work); got != base {
		t.Errorf("work left on %s, want original branch %s", got, base)
	}

	// Advance the remote feature branch, then pull from the base checkout.
	// Both downstream repositories must receive that feature-only commit, and
	// the base branch must remain the checked-out branch in work.
	mustRun(t, upstream, "git", "checkout", "-q", "feature")
	mustWrite(t, filepath.Join(upstream, "feature.txt"), "upstream feature\n")
	mustRun(t, upstream, "git", "commit", "-q", "-am", "upstream feature")
	upstreamFeatureHead := readHead(t, upstream)
	mustRun(t, upstream, "git", "checkout", "-q", base)

	if code := run([]string{"pull", work}); code != 0 {
		t.Fatalf("gitall multi-branch pull exit %d", code)
	}
	for _, repo := range []string{mirror, work} {
		if got := readBranchHead(t, repo, "feature"); got != upstreamFeatureHead {
			t.Errorf("%s feature after pull = %s, want %s", repo, got, upstreamFeatureHead)
		}
	}
	if got := currentBranchName(t, work); got != base {
		t.Errorf("work left on %s after pull, want original branch %s", got, base)
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

func TestCommitMessage(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	// Leave uncommitted changes and push with a commit message.
	mustWrite(t, filepath.Join(work, "f.txt"), "committed-by-gitall\n")
	workHeadBefore := readHead(t, work)

	if code := run([]string{"-m", "wip", "push", work}); code != 0 {
		t.Fatalf("gitall -m push exit %d", code)
	}

	workHeadAfter := readHead(t, work)
	if workHeadAfter == workHeadBefore {
		t.Errorf("work HEAD did not advance after commit")
	}
	if got := readHead(t, mirror); got != workHeadAfter {
		t.Errorf("mirror HEAD = %s, want %s", got, workHeadAfter)
	}
	if got := readHead(t, upstream); got != workHeadAfter {
		t.Errorf("upstream HEAD = %s, want %s", got, workHeadAfter)
	}

	// Verify the commit message was used.
	out, err := exec.Command("git", "-C", work, "log", "-1", "--pretty=%B").Output()
	if err != nil {
		t.Fatalf("git log in work: %v", err)
	}
	if !strings.Contains(string(out), "wip") {
		t.Errorf("commit message did not contain 'wip': %s", out)
	}
}

func TestPushRestoresConflictedFiles(t *testing.T) {
	work, mirror, upstream := testutil.BuildChain(t)

	testutil.WriteFile(t, filepath.Join(work, "f.txt"), "clean\n")
	testutil.Run(t, work, "git", "commit", "-q", "-am", "clean")
	cleanHead := readHead(t, work)
	testutil.Run(t, work, "git", "branch", "interpose/snapshot/20260707-000000_main_"+cleanHead[:7], "HEAD")

	testutil.WriteFile(t, filepath.Join(work, "f.txt"), "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> other\n")
	testutil.Run(t, work, "git", "commit", "-q", "-am", "markers")

	if code := run([]string{"-m", "restored", "push", work}); code != 0 {
		out, _ := execCombined([]string{"-m", "restored", "push", work})
		t.Fatalf("gitall exit %d; output:\n%s", code, out)
	}

	for _, r := range []string{work, mirror, upstream} {
		data, err := os.ReadFile(filepath.Join(r, "f.txt"))
		if err != nil {
			t.Fatalf("read %s: %v", r, err)
		}
		if string(data) != "clean\n" {
			t.Errorf("%s content = %q, want clean", r, data)
		}
	}

	log, err := exec.Command("git", "-C", work, "log", "-1", "--pretty=%B").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "restored") {
		t.Errorf("last commit message = %q, want 'restored'", log)
	}
}

func TestPushFastForwardBeforePush(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	mustWrite(t, filepath.Join(upstream, "f.txt"), "remote-only\n")
	mustRun(t, upstream, "git", "commit", "-q", "-am", "remote-only")
	upHead := readHead(t, upstream)

	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	if got := readHead(t, work); got != upHead {
		t.Errorf("work HEAD after ff push = %s, want %s", got, upHead)
	}
	if got := readHead(t, mirror); got != upHead {
		t.Errorf("mirror HEAD after ff push = %s, want %s", got, upHead)
	}
}

func TestPushPullChainOrder(t *testing.T) {
	work, mirror, upstream := buildChain(t)

	mustWrite(t, filepath.Join(upstream, "f.txt"), "upstream-first\n")
	mustRun(t, upstream, "git", "commit", "-q", "-am", "upstream-first")
	upHead := readHead(t, upstream)

	// Mirror is behind upstream until phase-1 pull chain runs.
	if got := readHead(t, mirror); got == upHead {
		t.Fatal("mirror should be behind upstream before push")
	}

	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	if got := readHead(t, work); got != upHead {
		t.Errorf("work HEAD = %s, want %s", got, upHead)
	}
	if got := readHead(t, mirror); got != upHead {
		t.Errorf("mirror HEAD = %s, want %s", got, upHead)
	}
}

// divergentClean leaves work and mirror with unrelated commits on different files.
func divergentClean(t *testing.T, work, mirror string) {
	t.Helper()
	mustWrite(t, filepath.Join(work, "shared.txt"), "base\n")
	mustRun(t, work, "git", "add", "-A")
	mustRun(t, work, "git", "commit", "-q", "-m", "base")
	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("setup push exit %d", code)
	}
	mustWrite(t, filepath.Join(mirror, "mirror.txt"), "mirror\n")
	mustRun(t, mirror, "git", "add", "-A")
	mustRun(t, mirror, "git", "commit", "-q", "-m", "mirror")
	mustWrite(t, filepath.Join(work, "work.txt"), "work\n")
	mustRun(t, work, "git", "add", "-A")
	mustRun(t, work, "git", "commit", "-q", "-m", "work")
}

// divergentConflict leaves work and mirror with conflicting edits to the same file.
func divergentConflict(t *testing.T, work, mirror string) {
	t.Helper()
	mustWrite(t, filepath.Join(work, "shared.txt"), "base\n")
	mustRun(t, work, "git", "add", "-A")
	mustRun(t, work, "git", "commit", "-q", "-m", "base")
	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("setup push exit %d", code)
	}
	mustWrite(t, filepath.Join(mirror, "f.txt"), "mirror\n")
	mustRun(t, mirror, "git", "commit", "-q", "-am", "mirror")
	mustWrite(t, filepath.Join(work, "f.txt"), "work\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "work")
}

func TestPushAllowMergeClean(t *testing.T) {
	work, mirror, upstream := buildChain(t)
	divergentClean(t, work, mirror)
	workHeadBefore := readHead(t, work)

	if code := run([]string{"-allow-merge", "push", work}); code != 0 {
		t.Fatalf("gitall -allow-merge push exit %d", code)
	}
	workHeadAfter := readHead(t, work)
	if workHeadAfter == workHeadBefore {
		t.Error("work HEAD did not advance after merge")
	}
	if got := readHead(t, mirror); got != workHeadAfter {
		t.Errorf("mirror HEAD = %s, want merged work %s", got, workHeadAfter)
	}
	if got := readHead(t, upstream); got != workHeadAfter {
		t.Errorf("upstream HEAD = %s, want merged work %s", got, workHeadAfter)
	}

	out, err := exec.Command("git", "-C", work, "log", "-1", "--pretty=%B").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	branchOut, err := exec.Command("git", "-C", work, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	want := "gitall: merge origin/" + strings.TrimSpace(string(branchOut))
	if !strings.Contains(string(out), want) {
		t.Errorf("merge message = %q, want containing %q", out, want)
	}
}

func TestPushAllowMergeWithMessage(t *testing.T) {
	work, mirror, _ := buildChain(t)
	divergentClean(t, work, mirror)

	if code := run([]string{"-allow-merge", "-m", "sync merge", "push", work}); code != 0 {
		t.Fatalf("gitall -allow-merge -m push exit %d", code)
	}
	out, err := exec.Command("git", "-C", work, "log", "-1", "--pretty=%B").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "sync merge") {
		t.Errorf("merge message = %q, want containing 'sync merge'", out)
	}
}

func TestPushAllowMergeConflict(t *testing.T) {
	work, mirror, _ := buildChain(t)
	divergentConflict(t, work, mirror)
	workHead := readHead(t, work)

	if code := run([]string{"-allow-merge", "push", work}); code == 0 {
		t.Fatal("expected non-zero exit on merge conflict")
	}
	if got := readHead(t, work); got != workHead {
		t.Errorf("work HEAD changed despite conflict: %s -> %s", workHead, got)
	}
	// mirror may still propagate via phase 3 even when work merge fails
	_ = mirror
}

func TestPushNoMergeOnDivergence(t *testing.T) {
	work, mirror, _ := buildChain(t)
	divergentClean(t, work, mirror)
	workHead := readHead(t, work)

	if code := run([]string{"push", work}); code == 0 {
		t.Fatal("expected non-zero exit when push cannot fast-forward without -allow-merge")
	}
	if got := readHead(t, work); got != workHead {
		t.Errorf("work HEAD should be unchanged, got %s want %s", got, workHead)
	}
	_ = mirror
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

func TestMergeModeGating(t *testing.T) {
	cases := []struct {
		name    string
		mode    MergeMode
		isLocal bool
		want    bool
	}{
		{"local/local", mergeLocal, true, true},
		{"local/network", mergeLocal, false, false},
		{"remote/network", mergeRemote, false, true},
		{"remote/local", mergeRemote, true, true},
		{"none/local", mergeNone, true, false},
		{"none/network", mergeNone, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work, mirror, _ := buildChain(t)
			divergentClean(t, work, mirror)
			workHead := readHead(t, work)

			o := opts{mergeMode: c.mode}
			updated, err := o.syncRemote(work, "origin", c.isLocal)
			if err != nil {
				t.Fatalf("syncRemote: %v", err)
			}
			if c.want {
				if !updated {
					t.Fatal("expected merge, but HEAD did not move")
				}
				if got := readHead(t, work); got == workHead {
					t.Fatal("work HEAD did not advance after merge")
				}
			} else {
				if updated {
					t.Fatal("did not expect merge, but HEAD moved")
				}
				if got := readHead(t, work); got != workHead {
					t.Fatalf("work HEAD should be unchanged, got %s want %s", got, workHead)
				}
			}
		})
	}
}

func TestParseMergeMode(t *testing.T) {
	cases := []struct {
		in   string
		want MergeMode
		ok   bool
	}{
		{"none", mergeNone, true},
		{"local", mergeLocal, true},
		{"remote", mergeRemote, true},
		{"pr", mergePR, true},
		{"0", mergeNone, true},
		{"1", mergeLocal, true},
		{"2", mergeRemote, true},
		{"3", mergePR, true},
		{"", mergeNone, true},
		{"banana", mergeNone, false},
		{"4", mergeNone, false},
	}
	for _, c := range cases {
		got, ok := parseMergeMode(c.in)
		if ok != c.ok {
			t.Errorf("parseMergeMode(%q) ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseMergeMode(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestMergeModeLocalMergesLocalRemote(t *testing.T) {
	work, mirror, _ := buildChain(t)
	divergentClean(t, work, mirror)

	if code := run([]string{"-allow-merge=local", "push", work}); code != 0 {
		t.Fatalf("gitall -allow-merge=local push exit %d", code)
	}
	if got, want := readHead(t, mirror), readHead(t, work); got != want {
		t.Errorf("mirror HEAD = %s, want work HEAD %s", got, want)
	}
}

func TestCheckoutHeadAfterLocalPush(t *testing.T) {
	work, mirror, _ := buildChain(t)
	mustWrite(t, filepath.Join(work, "f.txt"), "new\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "new")
	workHead := readHead(t, work)

	if code := run([]string{"push", work}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	if got := readHead(t, mirror); got != workHead {
		t.Errorf("mirror HEAD = %s, want %s", got, workHead)
	}
	data, err := os.ReadFile(filepath.Join(mirror, "f.txt"))
	if err != nil {
		t.Fatalf("read mirror f.txt: %v", err)
	}
	if string(data) != "new\n" {
		t.Errorf("mirror working tree f.txt = %q, want \"new\\n\"", string(data))
	}
	if err := exec.Command("git", "-C", mirror, "diff", "--exit-code", "HEAD").Run(); err != nil {
		t.Errorf("mirror working tree differs from HEAD after push")
	}
}

func TestRepoLockSerializesConcurrentPushes(t *testing.T) {
	root := t.TempDir()
	mirror := filepath.Join(root, "mirror")
	parent1 := filepath.Join(root, "parent1")
	parent2 := filepath.Join(root, "parent2")

	makeLocalRepo(t, mirror)
	mustRun(t, root, "git", "clone", "-q", mirror, "parent1")
	mustRun(t, root, "git", "clone", "-q", mirror, "parent2")
	for _, p := range []string{parent1, parent2} {
		mustRun(t, p, "git", "config", "user.email", "t@t")
		mustRun(t, p, "git", "config", "user.name", "t")
	}

	mustWrite(t, filepath.Join(parent1, "p1.txt"), "p1\n")
	mustRun(t, parent1, "git", "add", "-A")
	mustRun(t, parent1, "git", "commit", "-q", "-m", "p1")

	mustWrite(t, filepath.Join(parent2, "p2.txt"), "p2\n")
	mustRun(t, parent2, "git", "add", "-A")
	mustRun(t, parent2, "git", "commit", "-q", "-m", "p2")

	if code := run([]string{"-allow-merge=local", "push", parent1, parent2, mirror}); code != 0 {
		t.Fatalf("gitall push exit %d", code)
	}
	for _, name := range []string{"p1.txt", "p2.txt"} {
		if _, err := os.Stat(filepath.Join(mirror, name)); err != nil {
			t.Errorf("mirror missing %s: %v", name, err)
		}
	}
}

func TestMain(m *testing.M) {
	bin := "/tmp/gitall"
	testutil.MustBuildPackage(".", bin)
	os.Setenv("GITALL_BIN", bin)
	os.Exit(m.Run())
}

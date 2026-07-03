package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newProtectedRemote sets up:
//   - a bare "remote" repo whose pre-receive hook rejects pushes to
//     refs/heads/main (simulating GitHub branch protection), but allows any
//     other ref (so gitall-pr/* branches can be pushed).
//   - a "work" clone of it whose origin URL is rewritten (via
//     url.<bare>.insteadOf) to look like a GitHub remote, so `git remote
//     get-url origin` reports "git@github.com:test/test.git" while pushes
//     actually land on the local bare repo.
//
// It returns the work dir, the bare repo dir, and the initial commit sha.
func newProtectedRemote(t *testing.T) (work, bare, initSha string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	bare = filepath.Join(root, "bare.git")
	work = filepath.Join(root, "work")

	mustRun(t, "", "git", "init", "-q", "-b", "main", seed)
	mustRun(t, seed, "git", "config", "user.email", "t@t")
	mustRun(t, seed, "git", "config", "user.name", "t")
	mustWrite(t, filepath.Join(seed, "f.txt"), "init\n")
	mustRun(t, seed, "git", "add", "-A")
	mustRun(t, seed, "git", "commit", "-q", "-m", "init")
	initSha = readHead(t, seed)

	mustRun(t, "", "git", "clone", "-q", "--bare", seed, bare)
	hook := "#!/bin/sh\nwhile read old new ref; do\n  case \"$ref\" in\n    refs/heads/main) echo reject: branch protection >&2; exit 1 ;;\n  esac\ndone\nexit 0\n"
	mustWrite(t, filepath.Join(bare, "hooks", "pre-receive"), hook)
	if err := os.Chmod(filepath.Join(bare, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatalf("chmod pre-receive: %v", err)
	}

	mustRun(t, "", "git", "clone", "-q", bare, work)
	mustRun(t, work, "git", "config", "user.email", "t@t")
	mustRun(t, work, "git", "config", "user.name", "t")
	mustRun(t, work, "git", "remote", "set-url", "origin", "git@github.com:test/test.git")
	mustRun(t, work, "git", "config", "url."+bare+".pushInsteadOf", "git@github.com:test/test.git")

	return work, bare, initSha
}

// pushDivergentBranch creates a commit unrelated to bare's main history and
// pushes it directly to bare under refName (bypassing the pre-receive hook,
// which only rejects refs/heads/main), returning its sha.
func pushDivergentBranch(t *testing.T, bare, refName string) string {
	t.Helper()
	scratch := filepath.Join(t.TempDir(), "scratch")
	mustRun(t, "", "git", "clone", "-q", bare, scratch)
	mustRun(t, scratch, "git", "config", "user.email", "t@t")
	mustRun(t, scratch, "git", "config", "user.name", "t")
	mustRun(t, scratch, "git", "checkout", "-q", "--orphan", "divergent")
	mustWrite(t, filepath.Join(scratch, "d.txt"), "divergent\n")
	mustRun(t, scratch, "git", "add", "-A")
	mustRun(t, scratch, "git", "commit", "-q", "-m", "divergent")
	sha := readHead(t, scratch)
	mustRun(t, scratch, "git", "push", "-q", bare, "divergent:"+refName)
	return sha
}

// writeGHStub writes a fake `gh` executable to dir that answers `pr list`
// with listJSON and records `pr create` invocations (one line of args per
// call) to a log file, whose path is returned.
func writeGHStub(t *testing.T, dir, listJSON string) (createLog string) {
	t.Helper()
	createLog = filepath.Join(dir, "pr-create.log")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = pr ] && [ \"$2\" = list ]; then\n" +
		"  cat <<'EOF'\n" + listJSON + "\nEOF\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = pr ] && [ \"$2\" = create ]; then\n" +
		"  echo \"$@\" >> " + createLog + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected gh invocation: $@\" >&2\n" +
		"exit 1\n"
	ghPath := filepath.Join(dir, "gh")
	mustWrite(t, ghPath, script)
	if err := os.Chmod(ghPath, 0o755); err != nil {
		t.Fatalf("chmod gh stub: %v", err)
	}
	return createLog
}

// runBinaryCaptured runs the gitall binary with extraEnv appended to the
// current environment (last value of any duplicate key wins), returning its
// exit code and combined stdout+stderr.
func runBinaryCaptured(t *testing.T, args []string, extraEnv []string) (int, string) {
	t.Helper()
	bin := os.Getenv("GITALL_BIN")
	if bin == "" {
		bin = "/tmp/gitall"
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run gitall: %v", err)
		}
	}
	return code, out.String()
}

func readRemoteRef(t *testing.T, bare, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s in %s: %v", ref, bare, err)
	}
	return strings.TrimSpace(string(out))
}

func remoteRefExists(bare, ref string) bool {
	return exec.Command("git", "-C", bare, "rev-parse", "--verify", "--quiet", ref).Run() == nil
}

func TestPRFallbackCreatesNewPR(t *testing.T) {
	work, bare, _ := newProtectedRemote(t)
	stubDir := t.TempDir()
	createLog := writeGHStub(t, stubDir, "[]")

	mustWrite(t, filepath.Join(work, "f.txt"), "v2\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "v2")
	workHead := readHead(t, work)

	code, out := runBinaryCaptured(t, []string{"-pr", "push", work}, []string{"PATH=" + stubDir + ":" + os.Getenv("PATH")})
	if code != 0 {
		t.Fatalf("gitall -pr push exit %d, output:\n%s", code, out)
	}

	if !remoteRefExists(bare, "refs/heads/gitall-pr/main-1") {
		t.Fatalf("expected refs/heads/gitall-pr/main-1 on bare repo, output:\n%s", out)
	}
	if got := readRemoteRef(t, bare, "refs/heads/gitall-pr/main-1"); got != workHead {
		t.Errorf("gitall-pr/main-1 = %s, want %s", got, workHead)
	}

	logBytes, err := os.ReadFile(createLog)
	if err != nil {
		t.Fatalf("read create log: %v", err)
	}
	log := string(logBytes)
	for _, want := range []string{"pr create", "-R test/test", "--head gitall-pr/main-1", "--base main", "--fill"} {
		if !strings.Contains(log, want) {
			t.Errorf("pr create log missing %q; got: %s", want, log)
		}
	}
}

func TestPRFallbackReusesOpenPR(t *testing.T) {
	work, bare, initSha := newProtectedRemote(t)

	// Pre-existing open PR branch at the (ancestor) initial commit.
	mustRun(t, bare, "git", "update-ref", "refs/heads/gitall-pr/main-1", initSha)

	stubDir := t.TempDir()
	listJSON := `[{"number":7,"headRefName":"gitall-pr/main-1","headRefOid":"` + initSha + `"}]`
	createLog := writeGHStub(t, stubDir, listJSON)

	mustWrite(t, filepath.Join(work, "f.txt"), "v2\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "v2")
	workHead := readHead(t, work)

	code, out := runBinaryCaptured(t, []string{"-pr", "push", work}, []string{"PATH=" + stubDir + ":" + os.Getenv("PATH")})
	if code != 0 {
		t.Fatalf("gitall -pr push exit %d, output:\n%s", code, out)
	}

	if got := readRemoteRef(t, bare, "refs/heads/gitall-pr/main-1"); got != workHead {
		t.Errorf("gitall-pr/main-1 = %s, want fast-forwarded to %s", got, workHead)
	}
	if remoteRefExists(bare, "refs/heads/gitall-pr/main-2") {
		t.Errorf("unexpected gitall-pr/main-2 created; existing PR should have been reused")
	}
	if logBytes, err := os.ReadFile(createLog); err == nil && len(logBytes) > 0 {
		t.Errorf("gh pr create should not have been invoked; log:\n%s", logBytes)
	}
	if !strings.Contains(out, "updating existing PR #7") {
		t.Errorf("expected output to mention reusing PR #7; got:\n%s", out)
	}
}

func TestPRFallbackSkipsStalePR(t *testing.T) {
	work, bare, _ := newProtectedRemote(t)

	// Pre-existing open PR branch, but at a commit NOT an ancestor of the new
	// HEAD (diverged history) - must be skipped in favor of a new branch.
	staleSha := pushDivergentBranch(t, bare, "refs/heads/gitall-pr/main-1")

	stubDir := t.TempDir()
	listJSON := `[{"number":7,"headRefName":"gitall-pr/main-1","headRefOid":"` + staleSha + `"}]`
	createLog := writeGHStub(t, stubDir, listJSON)

	mustWrite(t, filepath.Join(work, "f.txt"), "v2\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "v2")
	workHead := readHead(t, work)

	code, out := runBinaryCaptured(t, []string{"-pr", "push", work}, []string{"PATH=" + stubDir + ":" + os.Getenv("PATH")})
	if code != 0 {
		t.Fatalf("gitall -pr push exit %d, output:\n%s", code, out)
	}

	if got := readRemoteRef(t, bare, "refs/heads/gitall-pr/main-1"); got != staleSha {
		t.Errorf("stale gitall-pr/main-1 should be untouched, got %s, want %s", got, staleSha)
	}
	if !remoteRefExists(bare, "refs/heads/gitall-pr/main-2") {
		t.Fatalf("expected new gitall-pr/main-2 branch, output:\n%s", out)
	}
	if got := readRemoteRef(t, bare, "refs/heads/gitall-pr/main-2"); got != workHead {
		t.Errorf("gitall-pr/main-2 = %s, want %s", got, workHead)
	}

	logBytes, err := os.ReadFile(createLog)
	if err != nil {
		t.Fatalf("read create log: %v", err)
	}
	if !strings.Contains(string(logBytes), "--head gitall-pr/main-2") {
		t.Errorf("expected gh pr create for gitall-pr/main-2; got: %s", logBytes)
	}
}

func TestPRFallbackDisabledByDefault(t *testing.T) {
	work, bare, _ := newProtectedRemote(t)
	stubDir := t.TempDir()
	createLog := writeGHStub(t, stubDir, "[]")

	mustWrite(t, filepath.Join(work, "f.txt"), "v2\n")
	mustRun(t, work, "git", "commit", "-q", "-am", "v2")

	code, out := runBinaryCaptured(t, []string{"push", work}, []string{"PATH=" + stubDir + ":" + os.Getenv("PATH")})
	if code == 0 {
		t.Fatalf("expected non-zero exit when push is rejected without -pr, output:\n%s", out)
	}

	if remoteRefExists(bare, "refs/heads/gitall-pr/main-1") {
		t.Errorf("no gitall-pr branch should be created without -pr")
	}
	if logBytes, err := os.ReadFile(createLog); err == nil && len(logBytes) > 0 {
		t.Errorf("gh should not have been invoked without -pr; log:\n%s", logBytes)
	}
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func installSleepHook(t *testing.T, repo string) {
	t.Helper()
	hook := filepath.Join(repo, ".git", "hooks", "pre-receive")
	testutil.WriteFile(t, hook, "#!/bin/sh\nexec >/dev/null 2>&1 0>/dev/null\nsleep 5\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatalf("chmod hook: %v", err)
	}
}

// runGitall runs the gitall binary, redirecting stdout/stderr to a temp file so
// that long-lived hook descendants do not keep os.Pipe file descriptors open
// and block command completion.
func runGitall(t *testing.T, env []string, args ...string) (exitCode int, output string, elapsed time.Duration) {
	t.Helper()
	outFile := filepath.Join(t.TempDir(), "gitall.out")
	out, err := os.Create(outFile)
	if err != nil {
		t.Fatalf("create output file: %v", err)
	}
	defer out.Close()

	cmd := exec.Command(os.Getenv("GITALL_BIN"), args...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil
	if env != nil {
		cmd.Env = env
	}

	start := time.Now()
	err = cmd.Run()
	elapsed = time.Since(start)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	out.Close()
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	return exitCode, string(data), elapsed
}

// TestPushTimeoutKillsLongHook verifies that -timeout bounds individual git
// tool invocations: a pre-receive hook that sleeps longer than the timeout is
// killed and the push fails quickly instead of hanging.
func TestPushTimeoutKillsLongHook(t *testing.T) {
	work, _, upstream := testutil.BuildChain(t)
	installSleepHook(t, upstream)

	testutil.WriteFile(t, filepath.Join(work, "f.txt"), "timeout-test\n")
	testutil.Run(t, work, "git", "commit", "-q", "-am", "timeout-test")

	code, out, elapsed := runGitall(t, nil, "-timeout", "200ms", "push", work)

	if code == 0 {
		t.Fatalf("expected non-zero exit\noutput:\n%s", out)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout did not fire: elapsed %v\noutput:\n%s", elapsed, out)
	}
	if !strings.Contains(out, "timed out after 200ms") {
		t.Fatalf("output missing timeout message:\n%s", out)
	}
}

// TestEnvTimeout verifies that GITALL_TIMEOUT is honored when -timeout is not
// supplied.
func TestEnvTimeout(t *testing.T) {
	work, _, upstream := testutil.BuildChain(t)
	installSleepHook(t, upstream)

	testutil.WriteFile(t, filepath.Join(work, "f.txt"), "env-timeout\n")
	testutil.Run(t, work, "git", "commit", "-q", "-am", "env-timeout")

	env := append(os.Environ(), "GITALL_TIMEOUT=100ms")
	code, out, elapsed := runGitall(t, env, "push", work)

	if code == 0 {
		t.Fatalf("expected non-zero exit\noutput:\n%s", out)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout did not fire: elapsed %v\noutput:\n%s", elapsed, out)
	}
	if !strings.Contains(out, "timed out after 100ms") {
		t.Fatalf("output missing timeout message:\n%s", out)
	}
}
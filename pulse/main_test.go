package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func buildPulse(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pulse")
	testutil.BuildPackage(t, ".", path)
	return path
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOnceRunsEveryJobImmediately(t *testing.T) {
	bin := buildPulse(t)
	cfg := writeConfig(t, "job: greet\ninterval: 1h\ncommand: echo hello-from-job\n")

	res := testutil.RunBinary(bin, "-once", "-config", cfg)

	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "greet") {
		t.Errorf("stdout = %q, want job name", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "hello-from-job") {
		t.Errorf("stdout = %q, want job output", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[done] greet: exit 0") {
		t.Errorf("stdout = %q, want [done] line", res.Stdout)
	}
}

func TestBadConfigPathExitsWithUsageError(t *testing.T) {
	bin := buildPulse(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	res := testutil.RunBinary(bin, "-once", "-config", missing)

	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, missing) {
		t.Errorf("stderr = %q, want it to mention %q", res.Stderr, missing)
	}
}

func TestConfigWithZeroJobsExitsWithUsageError(t *testing.T) {
	bin := buildPulse(t)
	cfg := writeConfig(t, "# no jobs here\n")

	res := testutil.RunBinary(bin, "-once", "-config", cfg)

	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", res.ExitCode, res.Stderr)
	}
}

func TestSignalShutsDownCleanlyAfterInFlightCommand(t *testing.T) {
	bin := buildPulse(t)
	cfg := writeConfig(t, "job: tick\ninterval: 50ms\ncommand: true\n")

	cmd := exec.Command(bin, "-config", cfg)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(200 * time.Millisecond) // let a few ticks fire
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("process exited with error: %v\noutput:\n%s", err, stdout.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("process did not exit within 5s of SIGINT\noutput so far:\n%s", stdout.String())
	}

	if !strings.Contains(stdout.String(), "[stop] pulse: shutdown complete") {
		t.Errorf("output = %q, want shutdown line", stdout.String())
	}
}

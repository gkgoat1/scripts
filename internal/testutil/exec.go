package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// RunResult holds captured subprocess output.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// RunBinary runs bin with args and returns exit code plus captured output.
func RunBinary(bin string, args ...string) RunResult {
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return RunResult{
		ExitCode: code,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
}

// BuildPackage builds pkgDir to outPath.
func BuildPackage(t *testing.T, pkgDir, outPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", outPath, pkgDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkgDir, err, out)
	}
}

// MustBuildPackage builds pkgDir to outPath, panicking on failure (for TestMain).
func MustBuildPackage(pkgDir, outPath string) {
	cmd := exec.Command("go", "build", "-o", outPath, pkgDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build " + pkgDir + ": " + err.Error() + "\n" + string(out))
	}
}

// EnvWith prepends key=value to the current environment.
func EnvWith(key, value string) []string {
	return append(os.Environ(), key+"="+value)
}

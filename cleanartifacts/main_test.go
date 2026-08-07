package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkgoat1/scripts/internal/testutil"
)

func defaultOpts(out, errOut interface{ Write([]byte) (int, error) }) cleanOptions {
	return cleanOptions{out: out, errOut: errOut}
}

func TestCleanAllRemovesArtifacts(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "proj", "target", "debug")
	nm := filepath.Join(root, "app", "node_modules", "pkg")
	keep := filepath.Join(root, "app", "src", "main.rs")
	testutil.MkdirAll(t, target, nm, filepath.Dir(keep))
	testutil.WriteFile(t, keep, "fn main(){}")

	var buf bytes.Buffer
	if err := cleanAll(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if testutil.PathExists(target) {
		t.Error("target still exists")
	}
	if testutil.PathExists(nm) {
		t.Error("node_modules still exists")
	}
	if !testutil.PathExists(keep) {
		t.Error("source removed")
	}
}

func TestCleanAllSkipsUnrelated(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "build")
	testutil.MkdirAll(t, other)
	var buf bytes.Buffer
	if err := cleanAll(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(other) {
		t.Error("build removed")
	}
}

func TestCleanAllPreservesPiOwnedNodeModules(t *testing.T) {
	root := t.TempDir()
	// A node_modules with a sibling package.json is treated as Pi-owned.
	pkgDir := filepath.Join(root, "pkg")
	nm := filepath.Join(pkgDir, "node_modules", "dep")
	testutil.MkdirAll(t, nm)
	testutil.WriteFile(t, filepath.Join(pkgDir, "package.json"), `{"name":"pkg"}`)

	// A stray node_modules WITHOUT a sibling package.json is cleaned.
	strayDir := filepath.Join(root, "stray")
	strayNM := filepath.Join(strayDir, "node_modules", "dep")
	testutil.MkdirAll(t, strayNM)

	var buf bytes.Buffer
	if err := cleanAll(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(nm) {
		t.Error("Pi-owned node_modules (sibling package.json) was removed")
	}
	if testutil.PathExists(strayNM) {
		t.Error("stray node_modules still exists")
	}
}

func TestCleanAllToleratesPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission test is meaningless")
	}
	root := t.TempDir()
	// A target we can delete.
	ok := filepath.Join(root, "ok", "target", "out")
	testutil.MkdirAll(t, ok)
	// A protected subtree: parent read-only, so traversal/removal errors with
	// EPERM and must not abort the run. The child under it is a target.
	locked := filepath.Join(root, "locked")
	target := filepath.Join(locked, "target", "deep")
	testutil.MkdirAll(t, target)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	var out, errOut bytes.Buffer
	err := cleanAll(root, defaultOpts(&out, &errOut))
	if err != nil {
		t.Fatalf("permission error must be non-fatal, got %v", err)
	}
	// The unprotected target was still cleaned.
	if testutil.PathExists(ok) {
		t.Error("ok target still exists; walk aborted before completing")
	}
}

func TestCleanRepoRoots(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "alpha")
	repo := filepath.Join(proj, "svc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteTree(t, root, map[string]string{
		"alpha/.prtag": "alpha:\n---\n[metadata]\n",
	})
	testutil.InitRepo(t, repo)
	target := filepath.Join(repo, "target", "out")
	nested := filepath.Join(proj, "target", "stray")
	testutil.MkdirAll(t, target, nested)

	var buf bytes.Buffer
	if err := cleanRepoRoots(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if testutil.PathExists(target) {
		t.Error("repo target still exists")
	}
	if !testutil.PathExists(nested) {
		t.Error("non-repo target removed")
	}
}

func TestCleanRepoRootsPreservesPiNodeModules(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "alpha")
	repo := filepath.Join(proj, "svc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteTree(t, root, map[string]string{
		"alpha/.prtag": "alpha:\n---\n[metadata]\n",
	})
	testutil.InitRepo(t, repo)
	// Pi-owned node_modules at repo root: preserved.
	testutil.WriteFile(t, filepath.Join(repo, "package.json"), `{"name":"svc"}`)
	nm := filepath.Join(repo, "node_modules", "dep")
	testutil.MkdirAll(t, nm)
	// A repo target: cleaned.
	target := filepath.Join(repo, "target", "out")
	testutil.MkdirAll(t, target)

	var buf bytes.Buffer
	if err := cleanRepoRoots(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(nm) {
		t.Error("Pi-owned node_modules was removed")
	}
	if testutil.PathExists(target) {
		t.Error("repo target still exists")
	}
}

func TestCleanTargetsOnlyLeavesBareNodeModules(t *testing.T) {
	root := t.TempDir()
	// Bare node_modules with no package context: left alone in targets-only.
	bareDir := filepath.Join(root, "bare")
	bare := filepath.Join(bareDir, "node_modules", "dep")
	testutil.MkdirAll(t, bare)

	// Structured node_modules with sibling package.json + .package-lock: this
	// is NOT Pi-owned (no matching pi root) so it's a candidate; but it has
	// expected structure. However note: sibling package.json => preserved.
	// Use .package-lock marker only (no sibling package.json) to test the
	// structural gate without triggering Pi-preservation.
	markerDir := filepath.Join(root, "marker")
	markerNM := filepath.Join(markerDir, "node_modules")
	testutil.MkdirAll(t, markerNM)
	testutil.WriteFile(t, filepath.Join(markerNM, ".package-lock.json"), `{}`)

	var buf bytes.Buffer
	if err := cleanTargetsOnly(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(bare) {
		t.Error("bare node_modules removed in targets-only mode")
	}
	if testutil.PathExists(markerNM) {
		t.Error("structured node_modules (marker) still exists; should be cleaned")
	}
}

func TestCleanTargetsOnlyPreservesPiOwned(t *testing.T) {
	root := t.TempDir()
	// node_modules with sibling package.json => Pi-owned, preserved even
	// though it has expected structure.
	pkgDir := filepath.Join(root, "pkg")
	nm := filepath.Join(pkgDir, "node_modules", "dep")
	testutil.MkdirAll(t, nm)
	testutil.WriteFile(t, filepath.Join(pkgDir, "package.json"), `{"name":"pkg"}`)
	// target is always cleaned.
	target := filepath.Join(root, "pkg", "target", "out")
	testutil.MkdirAll(t, target)

	var buf bytes.Buffer
	if err := cleanTargetsOnly(root, defaultOpts(&buf, &buf)); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(nm) {
		t.Error("Pi-owned node_modules removed")
	}
	if testutil.PathExists(target) {
		t.Error("target still exists")
	}
}

func TestDryRunListsAndDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "proj", "target", "out")
	nm := filepath.Join(root, "proj", "node_modules", "dep")
	testutil.MkdirAll(t, target, nm)

	var out bytes.Buffer
	opts := defaultOpts(&out, &out)
	opts.dryRun = true
	if err := cleanAll(root, opts); err != nil {
		t.Fatal(err)
	}
	if !testutil.PathExists(target) {
		t.Error("dry-run deleted target")
	}
	if !testutil.PathExists(nm) {
		t.Error("dry-run deleted node_modules")
	}
	if !strings.Contains(out.String(), filepath.Join(root, "proj", "target")) {
		t.Errorf("dry-run output missing target: %s", out.String())
	}
	if !strings.Contains(out.String(), filepath.Join(root, "proj", "node_modules")) {
		t.Errorf("dry-run output missing node_modules: %s", out.String())
	}
}

func TestRemovePathsListsEach(t *testing.T) {
	root := t.TempDir()
	p1 := filepath.Join(root, "a")
	p2 := filepath.Join(root, "b")
	testutil.MkdirAll(t, p1, p2)
	var out bytes.Buffer
	r := removePaths([]string{p1, p2}, defaultOpts(&out, &out))
	if r != nil {
		t.Fatal(r)
	}
	if !strings.Contains(out.String(), p1) || !strings.Contains(out.String(), p2) {
		t.Errorf("output = %q", out.String())
	}
}

func TestDaemonSingleTick(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "proj", "target", "out")
	testutil.MkdirAll(t, target)

	var out, errOut bytes.Buffer
	opts := defaultOpts(&out, &errOut)
	opts.targetsOnly = true

	// Run the daemon, cancel after the immediate first pass completes.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Give the immediate cleanOnce time to run, then cancel.
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	runDaemon(ctx, root, opts, time.Hour)

	if !strings.Contains(out.String(), "[start]") {
		t.Errorf("missing start line: %s", out.String())
	}
	if !strings.Contains(out.String(), "[stop]") {
		t.Errorf("missing stop line: %s", out.String())
	}
	if testutil.PathExists(target) {
		t.Error("daemon did not clean target on first tick")
	}
}

func TestIsPiOwnedNodeModules(t *testing.T) {
	root := t.TempDir()
	// Sibling package.json -> Pi-owned.
	pkgDir := filepath.Join(root, "pkg")
	nmWithPkg := filepath.Join(pkgDir, "node_modules")
	testutil.MkdirAll(t, nmWithPkg)
	testutil.WriteFile(t, filepath.Join(pkgDir, "package.json"), `{}`)
	if !isPiOwnedNodeModules(nmWithPkg) {
		t.Error("node_modules with sibling package.json should be Pi-owned")
	}

	// No sibling, not a Pi root -> not Pi-owned.
	stray := filepath.Join(root, "stray", "node_modules")
	testutil.MkdirAll(t, stray)
	if isPiOwnedNodeModules(stray) {
		t.Error("stray node_modules should not be Pi-owned")
	}
}

func TestCandidateHasExpectedStructure(t *testing.T) {
	root := t.TempDir()
	// node_modules with .package-lock marker.
	markerNM := filepath.Join(root, "nm")
	testutil.MkdirAll(t, markerNM)
	testutil.WriteFile(t, filepath.Join(markerNM, ".package-lock.json"), `{}`)
	if !(candidate{path: markerNM, name: "node_modules"}).hasExpectedStructure() {
		t.Error("node_modules with .package-lock should pass structure gate")
	}

	// Bare node_modules, no marker, no sibling package.json.
	bare := filepath.Join(root, "bare", "node_modules")
	testutil.MkdirAll(t, bare)
	if (candidate{path: bare, name: "node_modules"}).hasExpectedStructure() {
		t.Error("bare node_modules should fail structure gate")
	}

	// target is always structurally valid.
	tgt := filepath.Join(root, "target")
	testutil.MkdirAll(t, tgt)
	if !(candidate{path: tgt, name: "target"}).hasExpectedStructure() {
		t.Error("target should pass structure gate")
	}
}

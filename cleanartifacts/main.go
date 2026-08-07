// Command cleanartifacts removes build/dependency artifact directories
// (target/ and node_modules/) from a tree. It is safe to leave running in the
// background: node_modules trees that Pi is actively using are preserved,
// filesystem permission errors are non-fatal, and a -daemon mode runs a
// targeted clean on an interval. See docs/cleanartifacts-plan.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gkgoat1/scripts/internal/fsutil"
	"github.com/gkgoat1/scripts/workspace"
)

var artifacts = []string{"target", "node_modules"}

func main() {
	repoRoots := flag.Bool("repo-roots", false, "only remove artifacts at repo-root level (discovered via .prtag workspace scan)")
	targetsOnly := flag.Bool("targets-only", false, "only remove target/ and node_modules/ with the expected on-disk structure")
	daemon := flag.Bool("daemon", false, "run -targets-only on an interval until SIGINT/SIGTERM")
	daemonInterval := flag.Duration("daemon-interval", 30*time.Minute, "daemon tick interval")
	dryRun := flag.Bool("dry-run", false, "list paths that would be removed without deleting them")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	opts := cleanOptions{
		repoRoots:   *repoRoots,
		targetsOnly: *targetsOnly,
		dryRun:      *dryRun,
		out:         os.Stdout,
		errOut:      os.Stderr,
	}

	if *daemon {
		opts.targetsOnly = true
		runDaemon(context.Background(), root, opts, *daemonInterval)
		return
	}

	if err := clean(root, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type cleanOptions struct {
	repoRoots   bool
	targetsOnly bool
	dryRun      bool
	out         io.Writer
	errOut      io.Writer
}

func runDaemon(ctx context.Context, root string, opts cleanOptions, interval time.Duration) {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(opts.out, "[start] cleanartifacts: daemon root=%s interval=%s targets-only=%t\n", root, interval, opts.targetsOnly)
	cleanOnce(root, opts)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(opts.out, "[stop] cleanartifacts: shutdown complete")
			return
		case <-ticker.C:
			cleanOnce(root, opts)
		}
	}
}

// cleanOnce runs one clean pass and logs any errors to opts.errOut without
// returning them. The daemon uses this so a single failing tick never stops
// the loop.
func cleanOnce(root string, opts cleanOptions) {
	if err := clean(root, opts); err != nil {
		fmt.Fprintf(opts.errOut, "[warn] cleanartifacts: %v\n", err)
	}
}

func clean(root string, opts cleanOptions) error {
	switch {
	case opts.repoRoots:
		return cleanRepoRoots(root, opts)
	case opts.targetsOnly:
		return cleanTargetsOnly(root, opts)
	default:
		return cleanAll(root, opts)
	}
}

// candidate describes a directory that matched an artifact name.
type candidate struct {
	path string
	name string // "target" or "node_modules"
}

// shouldPreserve reports whether a candidate must not be deleted because Pi
// is using it. Only node_modules is ever Pi-owned; target/ is always cleanable.
func (c candidate) shouldPreserve() bool {
	if c.name != "node_modules" {
		return false
	}
	return isPiOwnedNodeModules(c.path)
}

// hasExpectedStructure gates -targets-only removal: the artifact must look
// like a real build/dependency tree, not a coincidentally-named directory.
func (c candidate) hasExpectedStructure() bool {
	switch c.name {
	case "target":
		// Cargo/Rust convention: a directory named target at a package root.
		// No stronger marker exists; accept the directory itself.
		return true
	case "node_modules":
		parent := filepath.Dir(c.path)
		if fileExists(filepath.Join(parent, "package.json")) {
			return true
		}
		// npm installs leave a .package-lock.json marker inside node_modules.
		return fileExists(filepath.Join(c.path, ".package-lock.json"))
	}
	return false
}

// removePaths deletes all paths concurrently. Permission-class errors
// (EPERM/EACCES, "operation not permitted") are non-fatal: they are logged
// and skipped. ErrNotExist is success. Any other error is collected and the
// batch returns a joined error so the caller can exit non-zero — but one bad
// path never aborts the rest of the batch.
func removePaths(paths []string, opts cleanOptions) error {
	var outMu sync.Mutex
	writeLine := func(s string) {
		outMu.Lock()
		defer outMu.Unlock()
		fmt.Fprintln(opts.out, s)
	}

	errc := make(chan error, len(paths))
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			writeLine(p)
			if opts.dryRun {
				return
			}
			err := fsutil.RemoveAll(p, opts.errOut)
			if err != nil {
				err = fmt.Errorf("remove %s: %w", p, err)
			}
			errc <- err
		}(p)
	}
	wg.Wait()
	close(errc)

	var errs []error
	for err := range errc {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func cleanRepoRoots(root string, opts cleanOptions) error {
	snap, err := workspace.NewOSScanner(root).Scan(context.Background())
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	var cands []candidate
	for _, proj := range snap.Projects {
		for _, repo := range proj.Repos {
			for _, name := range artifacts {
				path := filepath.Join(repo.Path, name)
				if _, err := os.Stat(path); err == nil {
					cands = append(cands, candidate{path: path, name: name})
				}
			}
		}
	}
	return removeCandidates(cands, opts)
}

func cleanAll(root string, opts cleanOptions) error {
	var cands []candidate
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A permission failure descending into a directory must not abort
			// the whole walk: skip that subtree and continue.
			if fsutil.IsPermission(walkErr) {
				fmt.Fprintf(opts.errOut, "[warn] skipping %s: %v\n", path, walkErr)
				return filepath.SkipDir
			}
			if fsutil.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		for _, name := range artifacts {
			if d.Name() == name {
				cands = append(cands, candidate{path: path, name: name})
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return removeCandidates(cands, opts)
}

func cleanTargetsOnly(root string, opts cleanOptions) error {
	var cands []candidate
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if fsutil.IsPermission(walkErr) {
				fmt.Fprintf(opts.errOut, "[warn] skipping %s: %v\n", path, walkErr)
				return filepath.SkipDir
			}
			if fsutil.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		for _, name := range artifacts {
			if d.Name() == name {
				c := candidate{path: path, name: name}
				if !c.hasExpectedStructure() {
					return nil // name matches but structure doesn't; keep walking
				}
				cands = append(cands, c)
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return removeCandidates(cands, opts)
}

// removeCandidates filters out preserved (Pi-owned) candidates and removes
// the rest.
func removeCandidates(cands []candidate, opts cleanOptions) error {
	var paths []string
	for _, c := range cands {
		if c.shouldPreserve() {
			if opts.dryRun {
				fmt.Fprintf(opts.out, "[preserve] %s\n", c.path)
			}
			continue
		}
		paths = append(paths, c.path)
	}
	return removePaths(paths, opts)
}

// --- Pi node_modules preservation ------------------------------------------

// isPiOwnedNodeModules reports whether nm is a node_modules tree Pi is
// likely loading. Pi installs packages into a directory containing
// package.json; the well-known roots catch the npm/git/CLI install dirs
// even when discovered without a sibling package.json.
func isPiOwnedNodeModules(nm string) bool {
	parent := filepath.Dir(nm)
	if fileExists(filepath.Join(parent, "package.json")) {
		return true
	}
	abs, err := filepath.Abs(nm)
	if err != nil {
		abs = nm
	}
	for _, root := range piNodeModulesRoots() {
		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// piNodeModulesRoots returns the well-known node_modules trees Pi uses:
// the agent npm/git install dirs and the pi CLI install dir. Paths are
// resolved against PI_AGENT_HOME (default $HOME/.pi/agent) and
// PI_INSTALL_DIR (default: derived from the running executable).
func piNodeModulesRoots() []string {
	agentHome := os.Getenv("PI_AGENT_HOME")
	if agentHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			agentHome = filepath.Join(home, ".pi", "agent")
		}
	}
	var roots []string
	if agentHome != "" {
		roots = append(roots, filepath.Join(agentHome, "npm", "node_modules"))
		// git packages live under agentHome/git/<host>/<path>; any
		// node_modules beneath is Pi-owned, so match the git root itself.
		roots = append(roots, filepath.Join(agentHome, "git"))
	}
	if installDir := piInstallDir(); installDir != "" {
		roots = append(roots, filepath.Join(installDir, "node_modules"))
	}
	return roots
}

// piInstallDir resolves the pi CLI install directory. PI_INSTALL_DIR wins;
// otherwise derive it from the running executable's path by walking up to
// the package root that contains node_modules + package.json (the pi install
// layout). Returns "" if it cannot be determined.
func piInstallDir() string {
	if v := os.Getenv("PI_INSTALL_DIR"); v != "" {
		return v
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 8 && dir != "" && dir != "/"; i++ {
		if fileExists(filepath.Join(dir, "package.json")) && fileExists(filepath.Join(dir, "node_modules")) {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

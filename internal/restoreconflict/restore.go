// Package restoreconflict automatically rolls back files that contain Git
// conflict markers to the latest snapshot branch version that does not.
package restoreconflict

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// DefaultSnapshotPrefix is the default Git ref namespace used by the git
// interposer for snapshot branches.
const DefaultSnapshotPrefix = "interpose/snapshot"

// Options control how conflict restoration is performed.
type Options struct {
	// Git is the path to the git binary. If empty, "git" is used.
	Git string
	// Prefix is the snapshot branch prefix (e.g. "interpose/snapshot").
	// If empty, DefaultSnapshotPrefix is used.
	Prefix string
	// DryRun, when true, reports what would be restored but makes no changes.
	DryRun bool
	// Out receives status messages. If nil, os.Stderr is used.
	Out io.Writer
}

// Restore scans every non-ignored file in repo (excluding the .git directory).
// For each file that contains conflict markers, it searches snapshot branches
// (newest first) for the latest version without markers and restores it.
// Each file is processed concurrently.
func Restore(repo string, opts Options) error {
	git := opts.Git
	if git == "" {
		git = "git"
	}
	prefix := opts.Prefix
	if prefix == "" {
		prefix = DefaultSnapshotPrefix
	}
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}

	bare, err := isBare(git, repo)
	if err != nil {
		return fmt.Errorf("check bare: %w", err)
	}
	if bare {
		return nil
	}

	files, err := listFiles(git, repo)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}

	refs, err := listRefs(git, repo, prefix)
	if err != nil {
		return fmt.Errorf("list refs: %w", err)
	}
	if len(refs) == 0 {
		return nil
	}

	workers := runtime.GOMAXPROCS(0)
	if workers < 4 {
		workers = 4
	}

	type job struct {
		path string
		ref  string
	}

	jobs := make(chan string, len(files))
	for _, f := range files {
		jobs <- f
	}
	close(jobs)

	results := make(chan job, len(files))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				data, err := os.ReadFile(filepath.Join(repo, path))
				if err != nil {
					continue
				}
				if !hasConflictMarkers(data) {
					continue
				}
				ref, err := findCleanRef(git, repo, path, refs)
				if err != nil || ref == "" {
					continue
				}
				results <- job{path: path, ref: ref}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var errs []error
	var mu sync.Mutex

	for r := range results {
		if opts.DryRun {
			fmt.Fprintf(out, "[restore] %s: would restore %s from %s\n", repo, r.path, r.ref)
			continue
		}
		if err := checkoutFile(git, repo, r.path, r.ref); err != nil {
			mu.Lock()
			errs = append(errs, fmt.Errorf("%s: %w", r.path, err))
			mu.Unlock()
			continue
		}
		fmt.Fprintf(out, "[restore] %s: restored %s from %s\n", repo, r.path, r.ref)
	}

	if len(errs) > 0 {
		return fmt.Errorf("restore %d file(s): %v", len(errs), errs)
	}
	return nil
}

func listFiles(git, repo string) ([]string, error) {
	cmd := exec.Command(git, "-C", repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	s := string(out)
	if len(s) > 0 && s[len(s)-1] == '\x00' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\x00"), nil
}

func listRefs(git, repo, prefix string) ([]string, error) {
	cmd := exec.Command(git, "-C", repo, "for-each-ref", "--sort=-refname", "--format=%(refname:short)", "refs/heads/"+prefix+"/*")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func isBare(git, repo string) (bool, error) {
	out, err := exec.Command(git, "-C", repo, "rev-parse", "--is-bare-repository").Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func fileAtRef(git, repo, ref, path string) ([]byte, error) {
	cmd := exec.Command(git, "-C", repo, "show", ref+":"+path)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func findCleanRef(git, repo, path string, refs []string) (string, error) {
	for _, ref := range refs {
		data, err := fileAtRef(git, repo, ref, path)
		if err != nil {
			// The file does not exist at this ref.
			continue
		}
		if !hasConflictMarkers(data) {
			return ref, nil
		}
	}
	return "", nil
}

func checkoutFile(git, repo, path, ref string) error {
	cmd := exec.Command(git, "-C", repo, "checkout", ref, "--", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// hasConflictMarkers reports whether data contains Git conflict-marker lines.
// It looks for lines starting with "<<<<<<<" or ">>>>>>>" followed by a
// space, newline, or end-of-file.
func hasConflictMarkers(data []byte) bool {
	for {
		if i := bytes.Index(data, []byte("<<<<<<<")); i >= 0 {
			if lineStart(data, i) && markerEnd(data, i+7) {
				return true
			}
			data = data[i+1:]
			continue
		}
		i := bytes.Index(data, []byte(">>>>>>>"))
		if i < 0 {
			return false
		}
		if lineStart(data, i) && markerEnd(data, i+7) {
			return true
		}
		data = data[i+1:]
	}
}

func lineStart(data []byte, i int) bool {
	return i == 0 || data[i-1] == '\n'
}

func markerEnd(data []byte, j int) bool {
	return j == len(data) || data[j] == ' ' || data[j] == '\n'
}

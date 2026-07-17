// Package restoreconflict automatically rolls back files that contain Git
// conflict markers to the latest snapshot branch version that does not.
package restoreconflict

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/gkgoat1/scripts/interpose/core"
)

// DefaultSnapshotPrefix is the default Git ref namespace used by the git
// interposer for snapshot branches.
const DefaultSnapshotPrefix = "interpose/snapshot"

// Runner is the minimal external-effects boundary used by conflict recovery.
// It permits interposers to run all commands and reads in their active context.
type Runner interface {
	Run(ctx context.Context, command core.Command) (core.Result, error)
	ReadFile(ctx context.Context, path string) ([]byte, error)
	Stderr() io.Writer
}

// Options control how conflict restoration is performed.
type Options struct {
	// Git is the path to the git binary. If empty, "git" is used.
	Git string
	// Prefix is the snapshot branch prefix (e.g. "interpose/snapshot"). If
	// empty, DefaultSnapshotPrefix is used.
	Prefix string
	// DryRun, when true, reports what would be restored but makes no changes.
	DryRun bool
	// Out receives status messages. If nil, os.Stderr is used.
	Out io.Writer
	// Runner performs Git commands and file reads. A nil runner is the host
	// compatibility behavior; sandbox interposers must provide their context.
	Runner Runner
}

type hostRunner struct{ out io.Writer }

func (r hostRunner) Run(ctx context.Context, command core.Command) (core.Result, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir, cmd.Env = command.Dir, command.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = command.Stdin, command.Stdout, command.Stderr
	if cmd.Stdout == nil {
		cmd.Stdout = r.out
	}
	if cmd.Stderr == nil {
		cmd.Stderr = r.out
	}
	if err := cmd.Run(); err != nil {
		return core.Result{ExitCode: 1}, err
	}
	return core.Result{}, nil
}
func (r hostRunner) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (r hostRunner) Stderr() io.Writer { return r.out }

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
	runner := opts.Runner
	if runner == nil {
		runner = hostRunner{out: out}
	}

	bare, err := isBare(runner, git, repo)
	if err != nil {
		return fmt.Errorf("check bare: %w", err)
	}
	if bare {
		return nil
	}
	files, err := listFiles(runner, git, repo)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	refs, err := listRefs(runner, git, repo, prefix)
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
	type job struct{ path, ref string }
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
				data, err := runner.ReadFile(context.Background(), filepath.Join(repo, path))
				if err != nil || !hasConflictMarkers(data) {
					continue
				}
				ref, err := findCleanRef(runner, git, repo, path, refs)
				if err == nil && ref != "" {
					results <- job{path: path, ref: ref}
				}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	var errs []error
	var mu sync.Mutex
	for r := range results {
		if opts.DryRun {
			fmt.Fprintf(out, "[restore] %s: would restore %s from %s\n", repo, r.path, r.ref)
			continue
		}
		if err := checkoutFile(runner, git, repo, r.path, r.ref); err != nil {
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

func runOutput(runner Runner, git, repo string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	_, err := runner.Run(context.Background(), core.Command{Path: git, Args: append([]string{"-C", repo}, args...), Stdout: &out, Stderr: runner.Stderr()})
	return out.Bytes(), err
}

func listFiles(runner Runner, git, repo string) ([]string, error) {
	out, err := runOutput(runner, git, repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
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

func listRefs(runner Runner, git, repo, prefix string) ([]string, error) {
	out, err := runOutput(runner, git, repo, "for-each-ref", "--sort=-refname", "--format=%(refname:short)", "refs/heads/"+prefix+"/*")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func isBare(runner Runner, git, repo string) (bool, error) {
	out, err := runOutput(runner, git, repo, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func fileAtRef(runner Runner, git, repo, ref, path string) ([]byte, error) {
	return runOutput(runner, git, repo, "show", ref+":"+path)
}

func findCleanRef(runner Runner, git, repo, path string, refs []string) (string, error) {
	for _, ref := range refs {
		data, err := fileAtRef(runner, git, repo, ref, path)
		if err == nil && !hasConflictMarkers(data) {
			return ref, nil
		}
	}
	return "", nil
}

func checkoutFile(runner Runner, git, repo, path, ref string) error {
	_, err := runner.Run(context.Background(), core.Command{Path: git, Args: []string{"-C", repo, "checkout", ref, "--", path}, Stdout: runner.Stderr(), Stderr: runner.Stderr()})
	return err
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
func lineStart(data []byte, i int) bool { return i == 0 || data[i-1] == '\n' }
func markerEnd(data []byte, j int) bool { return j == len(data) || data[j] == ' ' || data[j] == '\n' }

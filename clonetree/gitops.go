package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gkgoat1/scripts/internal/gitpath"
)

type gitRunner struct {
	dryRun  bool
	verbose bool
}

type repoState struct {
	head   string
	branch string // empty when detached
}

func (g gitRunner) run(dir string, args ...string) error {
	if g.dryRun {
		if dir != "" {
			fmt.Printf("[git] -C %s %s\n", dir, strings.Join(args, " "))
		} else {
			fmt.Printf("[git] %s\n", strings.Join(args, " "))
		}
		return nil
	}
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if g.verbose {
		if dir != "" {
			fmt.Printf("[git] -C %s %s\n", dir, strings.Join(args, " "))
		} else {
			fmt.Printf("[git] %s\n", strings.Join(args, " "))
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func (g gitRunner) capture(dir string, args ...string) (string, error) {
	if g.dryRun {
		return "", nil
	}
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stdout.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func readRepoState(srcRepo string) (repoState, error) {
	head, err := captureGit(srcRepo, "rev-parse", "HEAD")
	if err != nil {
		return repoState{}, fmt.Errorf("read HEAD in %s: %w", srcRepo, err)
	}
	branch, err := captureGit(srcRepo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		branch = ""
	}
	return repoState{head: head, branch: branch}, nil
}

func captureGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stderr = &stdout
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stdout.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func isDirty(repo string) (bool, error) {
	out, err := captureGit(repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func materializeClone(srcRepo, destRepo string, state repoState, force bool, g gitRunner) error {
	evalSrc, err := gitpath.EvalRepoPath(srcRepo)
	if err != nil {
		return fmt.Errorf("resolve source repo %s: %w", srcRepo, err)
	}

	if _, err := os.Stat(destRepo); err == nil {
		if gitpath.IsGitRepo(destRepo) {
			return updateExistingClone(evalSrc, destRepo, state, force, g)
		}
		if !force {
			return fmt.Errorf("%s exists and is not a git repository (use -force)", destRepo)
		}
		if !g.dryRun {
			if err := os.RemoveAll(destRepo); err != nil {
				return err
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(destRepo), 0o755); err != nil && !g.dryRun {
		return err
	}
	return g.run("", "clone", evalSrc, destRepo)
}

func updateExistingClone(evalSrc, destRepo string, state repoState, force bool, g gitRunner) error {
	url, err := g.capture(destRepo, "remote", "get-url", "origin")
	if err != nil {
		if force {
			if !g.dryRun {
				if err := os.RemoveAll(destRepo); err != nil {
					return err
				}
			}
			return g.run("", "clone", evalSrc, destRepo)
		}
		return fmt.Errorf("%s: no origin remote: %w", destRepo, err)
	}
	resolved, ok := gitpath.ResolveLocalRemote(destRepo, url)
	if !ok || resolved != evalSrc {
		if force {
			if !g.dryRun {
				if err := os.RemoveAll(destRepo); err != nil {
					return err
				}
			}
			return g.run("", "clone", evalSrc, destRepo)
		}
		return fmt.Errorf("%s: origin %q does not match source %q", destRepo, url, evalSrc)
	}

	dirty, err := isDirty(destRepo)
	if err != nil {
		return err
	}
	if dirty && !force {
		return fmt.Errorf("%s: uncommitted changes (use -force)", destRepo)
	}

	if err := g.run(destRepo, "fetch", "origin"); err != nil {
		return err
	}
	return g.run(destRepo, "checkout", state.head)
}

func materializeWorktree(srcRepo, destRepo string, state repoState, force bool, g gitRunner) error {
	if isWorktreeOf(srcRepo, destRepo) {
		if g.verbose {
			fmt.Printf("[skip] %s (existing worktree of %s)\n", destRepo, srcRepo)
		}
		return nil
	}

	if _, err := os.Stat(destRepo); err == nil {
		if !force {
			return fmt.Errorf("%s exists (use -force)", destRepo)
		}
		if err := g.run(srcRepo, "worktree", "remove", "--force", destRepo); err != nil {
			if !g.dryRun {
				if rmErr := os.RemoveAll(destRepo); rmErr != nil {
					return fmt.Errorf("remove %s: %v (worktree remove: %v)", destRepo, rmErr, err)
				}
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(destRepo), 0o755); err != nil && !g.dryRun {
		return err
	}

	// The source checkout already holds its branch, so branch names cannot be
	// checked out again in a linked worktree; use a detached HEAD at source HEAD.
	return g.run(srcRepo, "worktree", "add", "--detach", destRepo, state.head)
}

func isWorktreeOf(srcRepo, destRepo string) bool {
	out, err := captureGit(srcRepo, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	destAbs, err := filepath.Abs(destRepo)
	if err != nil {
		return false
	}
	destEval, err := filepath.EvalSymlinks(destAbs)
	if err != nil {
		destEval = destAbs
	}

	var worktreePath string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			worktreePath = strings.TrimPrefix(line, "worktree ")
			wp, err := filepath.EvalSymlinks(worktreePath)
			if err != nil {
				wp = worktreePath
			}
			if wp == destEval {
				return true
			}
		}
	}
	return false
}

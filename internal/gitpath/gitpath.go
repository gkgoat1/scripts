package gitpath

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HasGitDir reports whether path contains a .git entry.
func HasGitDir(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

// IsGitRepo reports whether path is a git repository (working tree or bare).
func IsGitRepo(path string) bool {
	if HasGitDir(path) {
		return true
	}
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	cmd.Stdout = new(bytes.Buffer)
	cmd.Stderr = new(bytes.Buffer)
	return cmd.Run() == nil
}

// ResolveLocalRemote returns the evaluated real path of url if it refers to a
// local git repository.
func ResolveLocalRemote(repo, url string) (string, bool) {
	p := url
	if strings.HasPrefix(p, "file://") {
		p = strings.TrimPrefix(p, "file://")
		p = strings.TrimPrefix(p, "localhost")
	}
	if strings.Contains(p, "://") {
		return "", false
	}
	if i := strings.Index(p, ":"); i >= 0 {
		if j := strings.Index(p, "/"); j < 0 || i < j {
			return "", false
		}
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		p = filepath.Join(home, p[1:])
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(repo, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	ev, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	if !IsGitRepo(ev) {
		return "", false
	}
	return ev, true
}

// EvalRepoPath absolutizes and evaluates symlinks for a repo directory.
func EvalRepoPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

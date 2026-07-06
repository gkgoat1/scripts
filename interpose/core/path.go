package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrRealBinaryNotFound = errors.New("real binary not found on PATH after interposer")

// ResolveRealBinary finds the first executable named name on PATH entries
// after the directory containing the running interposer.
func ResolveRealBinary(name string) (string, error) {
	selfDir, err := selfDir()
	if err != nil {
		return "", fmt.Errorf("executable path: %w", err)
	}

	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return "", ErrRealBinaryNotFound
	}

	entries := filepath.SplitList(pathEnv)
	foundSelf := false
	for _, entry := range entries {
		entry, err := filepath.EvalSymlinks(entry)
		if err != nil {
			entry = filepath.Clean(entry)
		}
		if !foundSelf {
			if sameDir(entry, selfDir) {
				foundSelf = true
			}
			continue
		}
		candidate := filepath.Join(entry, name)
		if isExecutable(candidate) {
			return candidate, nil
		}
	}
	return "", ErrRealBinaryNotFound
}

func selfDir() (string, error) {
	if override := os.Getenv("INTERPOSE_SELF"); override != "" {
		dir, err := filepath.EvalSymlinks(filepath.Dir(override))
		if err != nil {
			return filepath.Dir(override), nil
		}
		return dir, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(self))
	if err != nil {
		return filepath.Dir(self), nil
	}
	return dir, nil
}

func sameDir(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	if info.Mode()&0o111 != 0 {
		return true
	}
	// Symlinks to executables may not carry mode bits on all platforms.
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false
		}
		tinfo, err := os.Stat(target)
		if err != nil {
			return false
		}
		return !tinfo.IsDir() && tinfo.Mode()&0o111 != 0
	}
	return false
}

// StripNoInterpose removes --no-interpose from args and reports whether it was present.
func StripNoInterpose(args []string) ([]string, bool) {
	var out []string
	found := false
	for _, a := range args {
		if a == "--no-interpose" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

// HasFlag reports whether args contains flag (exact match).
func HasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// Subcommand returns the first non-flag argument, skipping global git-style flags.
func Subcommand(args []string) string {
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-C" || a == "--git-dir" || a == "--work-tree" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

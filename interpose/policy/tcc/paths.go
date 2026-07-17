package tcc

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gkgoat1/scripts/interpose/config"
)

// DefaultProtectedRelative returns home-relative TCC-sensitive directory names.
func DefaultProtectedRelative() []string {
	return []string{
		"Library",
		"Documents",
		"Desktop",
		"Downloads",
		"Pictures",
		"Movies",
		"Music",
	}
}

// DefaultProtectedRoots returns the fixed, config-independent set of
// TCC-sensitive absolute roots (home + DefaultProtectedRelative()). This is
// what a verified caller (e.g. sandboxd) falls back to when policy
// commitment verification fails: config only ever broadens protection, so
// reverting to these can never be more permissive than an operator with no
// custom config already gets.
func DefaultProtectedRoots() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(DefaultProtectedRelative()))
	for _, rel := range DefaultProtectedRelative() {
		roots = append(roots, filepath.Join(home, rel))
	}
	return roots, nil
}

// MatchesRoots reports whether path is or is under any of roots.
func MatchesRoots(path string, roots []string) bool {
	for _, root := range roots {
		root = filepath.Clean(root)
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ProtectedRoots returns absolute paths that should not be traversed:
// DefaultProtectedRoots() plus any config-supplied ExtraProtectedPaths.
func ProtectedRoots() []string {
	roots, err := DefaultProtectedRoots()
	if err != nil {
		return config.Load().ExtraProtectedPaths
	}
	roots = append(roots, config.Load().ExtraProtectedPaths...)
	return roots
}

// NormalizePath cleans and resolves a path as much as possible.
func NormalizePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	} else if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	ev, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = ev
	}
	return path, nil
}

// IsProtected reports whether path is or is under a protected root.
func IsProtected(path string) bool {
	norm, err := NormalizePath(path)
	if err != nil || norm == "" {
		return false
	}
	return MatchesRoots(norm, ProtectedRoots())
}

// IsProtectedDirName reports whether name is a macOS TCC-sensitive directory basename.
func IsProtectedDirName(name string) bool {
	for _, rel := range DefaultProtectedRelative() {
		if name == rel {
			return true
		}
	}
	return false
}

// WouldTraverseProtected reports whether searching from root could enter protected dirs.
func WouldTraverseProtected(root string) bool {
	norm, err := NormalizePath(root)
	if err != nil {
		return false
	}
	home, _ := os.UserHomeDir()
	if norm == home || norm == "/" {
		return true
	}
	return IsProtected(norm)
}

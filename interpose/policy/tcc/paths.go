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

// ProtectedRoots returns absolute paths that should not be traversed.
func ProtectedRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Load().ExtraProtectedPaths
	}
	var roots []string
	for _, rel := range DefaultProtectedRelative() {
		roots = append(roots, filepath.Join(home, rel))
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
	for _, root := range ProtectedRoots() {
		root = filepath.Clean(root)
		if norm == root || strings.HasPrefix(norm, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
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

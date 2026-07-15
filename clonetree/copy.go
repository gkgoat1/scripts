package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func copyNonRepoFiles(srcRoot, destRoot string, repoRoots []string, force, dryRun, verbose bool) error {
	repoAbs := make([]string, len(repoRoots))
	for i, r := range repoRoots {
		repoAbs[i] = normalizePath(r)
	}

	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == ".prtag" {
			return nil
		}
		if isRepoRoot(path, repoAbs) {
			return filepath.SkipDir
		}
		if isUnderRepo(path, repoAbs) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		destPath := filepath.Join(destRoot, rel)
		if d.IsDir() {
			if dryRun {
				if verbose {
					fmt.Printf("[mkdir] %s\n", destPath)
				}
				return nil
			}
			return os.MkdirAll(destPath, 0o755)
		}

		if _, err := os.Stat(destPath); err == nil && !force {
			if verbose {
				fmt.Printf("[skip] %s (exists)\n", destPath)
			}
			return nil
		}

		if dryRun {
			fmt.Printf("[copy] %s -> %s\n", path, destPath)
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		if verbose {
			fmt.Printf("[copy] %s -> %s\n", path, destPath)
		}
		return copyFile(path, destPath)
	})
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func isRepoRoot(path string, repoRoots []string) bool {
	clean := normalizePath(path)
	for _, r := range repoRoots {
		if clean == r {
			return true
		}
	}
	return false
}

func isUnderRepo(path string, repoRoots []string) bool {
	clean := normalizePath(path)
	for _, r := range repoRoots {
		if clean == r {
			return false
		}
		rel, err := filepath.Rel(r, clean)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func normalizePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	ev, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs)
	}
	return ev
}

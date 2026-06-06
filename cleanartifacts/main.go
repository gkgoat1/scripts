package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gkgoat1/scripts/workspace"
)

var artifacts = []string{"target", "node_modules"}

func main() {
	repoRoots := flag.Bool("repo-roots", false, "only remove artifacts at repo-root level (discovered via .prtag workspace scan)")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	var err error
	if *repoRoots {
		err = cleanRepoRoots(root)
	} else {
		err = cleanAll(root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// removePaths deletes all paths concurrently and returns the first error encountered.
func removePaths(paths []string) error {
	errc := make(chan error, len(paths))
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			fmt.Println(p)
			if err := os.RemoveAll(p); err != nil {
				errc <- err
			}
		}(p)
	}
	wg.Wait()
	close(errc)
	return <-errc
}

func cleanRepoRoots(root string) error {
	snap, err := workspace.NewOSScanner(root).Scan(context.Background())
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	var paths []string
	for _, proj := range snap.Projects {
		for _, repo := range proj.Repos {
			for _, name := range artifacts {
				path := filepath.Join(repo.Path, name)
				if _, err := os.Stat(path); err == nil {
					paths = append(paths, path)
				}
			}
		}
	}
	return removePaths(paths)
}

func cleanAll(root string) error {
	// Collect matching paths first so the walk's SkipDir logic is unaffected,
	// then delete all of them concurrently.
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		for _, name := range artifacts {
			if d.Name() == name {
				paths = append(paths, path)
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return removePaths(paths)
}

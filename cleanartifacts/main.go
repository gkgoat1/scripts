package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

func cleanRepoRoots(root string) error {
	snap, err := workspace.NewOSScanner(root).Scan(context.Background())
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	for _, proj := range snap.Projects {
		for _, repo := range proj.Repos {
			for _, name := range artifacts {
				path := filepath.Join(repo.Path, name)
				if _, err := os.Stat(path); err == nil {
					fmt.Println(path)
					if err := os.RemoveAll(path); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func cleanAll(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		for _, name := range artifacts {
			if d.Name() == name {
				fmt.Println(path)
				if err := os.RemoveAll(path); err != nil {
					return err
				}
				return filepath.SkipDir
			}
		}
		return nil
	})
}

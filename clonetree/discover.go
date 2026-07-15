package main

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gkgoat1/scripts/internal/gitpath"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
	"github.com/gkgoat1/scripts/workspace"
)

type repoEntry struct {
	Abs string
	Rel string
}

func discover(mode, src string, verbose bool) ([]repoEntry, error) {
	var absRepos []string
	var err error
	switch mode {
	case "prtag":
		absRepos, err = discoverPrtag(src, verbose)
	case "any":
		absRepos, err = discoverAny([]string{src})
	default:
		return nil, fmt.Errorf("unknown discovery mode %q", mode)
	}
	if err != nil {
		return nil, err
	}
	absRepos = dedupeRepos(absRepos)

	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	srcAbs, err = filepath.EvalSymlinks(srcAbs)
	if err != nil {
		return nil, err
	}

	var entries []repoEntry
	for _, r := range absRepos {
		rel, err := filepath.Rel(srcAbs, r)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(rel, "..") {
			continue
		}
		entries = append(entries, repoEntry{Abs: r, Rel: rel})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Rel < entries[j].Rel
	})
	return entries, nil
}

func skipWalkDir(name string) bool {
	return strings.HasPrefix(name, ".") || tcc.IsProtectedDirName(name)
}

func discoverAny(roots []string) ([]string, error) {
	var repos []string
	for _, root := range roots {
		r, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if skipWalkDir(d.Name()) {
				return fs.SkipDir
			}
			if gitpath.HasGitDir(path) {
				repos = append(repos, path)
				return fs.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return repos, nil
}

func discoverPrtag(src string, verbose bool) ([]string, error) {
	snap, err := workspace.NewOSScanner(src).Scan(context.Background())
	if err != nil {
		return nil, fmt.Errorf("workspace scan: %w", err)
	}
	var repos []string
	for _, p := range snap.Projects {
		if verbose {
			fmt.Printf("[project] %s (%s)\n", p.Path, p.Tag.Name)
		}
		for _, r := range p.Repos {
			repos = append(repos, r.Path)
		}
	}
	return dedupeRepos(repos), nil
}

func dedupeRepos(repos []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range repos {
		rp, err := filepath.EvalSymlinks(r)
		if err != nil {
			rp = r
		}
		if seen[rp] {
			continue
		}
		seen[rp] = true
		out = append(out, rp)
	}
	sort.Strings(out)
	return out
}

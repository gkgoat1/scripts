// Command clonetree mirrors a source directory tree into a destination:
// git repositories are materialized via clone or worktree, and non-repo files
// are copied except .prtag markers.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type options struct {
	method  string
	from    string
	dryRun  bool
	verbose bool
	force   bool
}

func main() {
	method := flag.String("method", "", `materialization method: "clone" or "worktree" (required)`)
	from := flag.String("from", "prtag", `discovery mode: "prtag" or "any"`)
	dryRun := flag.Bool("n", false, "dry run: print actions without executing")
	verbose := flag.Bool("v", false, "verbose output")
	force := flag.Bool("force", false, "overwrite existing non-repo files; replace blocking paths")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: clonetree -method clone|worktree [flags] <src> <dest>")
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprintln(flag.CommandLine.Output(), "flags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *method == "" {
		fmt.Fprintln(os.Stderr, "clonetree: -method is required (clone or worktree)")
		flag.Usage()
		os.Exit(2)
	}
	switch *method {
	case "clone", "worktree":
	default:
		fmt.Fprintf(os.Stderr, "clonetree: -method must be clone or worktree, got %q\n", *method)
		os.Exit(2)
	}
	switch *from {
	case "prtag", "any":
	default:
		fmt.Fprintf(os.Stderr, "clonetree: -from must be prtag or any, got %q\n", *from)
		os.Exit(2)
	}

	args := flag.Args()
	if len(args) != 2 {
		flag.Usage()
		os.Exit(2)
	}

	o := options{
		method:  *method,
		from:    *from,
		dryRun:  *dryRun,
		verbose: *verbose,
		force:   *force,
	}

	if err := run(o, args[0], args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "clonetree: %v\n", err)
		os.Exit(1)
	}
}

func run(o options, src, dest string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source %q is not a directory", srcAbs)
	}

	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if !o.dryRun {
		if err := os.MkdirAll(destAbs, 0o755); err != nil {
			return fmt.Errorf("create dest: %w", err)
		}
	}

	repos, err := discover(o.from, srcAbs, o.verbose)
	if err != nil {
		return err
	}
	if o.verbose {
		fmt.Printf("clonetree: %d repository(s) discovered\n", len(repos))
	}

	repoRoots := make([]string, len(repos))
	for i, r := range repos {
		repoRoots[i] = r.Abs
	}

	if err := copyNonRepoFiles(srcAbs, destAbs, repoRoots, o.force, o.dryRun, o.verbose); err != nil {
		return fmt.Errorf("copy non-repo files: %w", err)
	}

	g := gitRunner{dryRun: o.dryRun, verbose: o.verbose}
	var wg sync.WaitGroup
	errc := make(chan error, len(repos))
	for _, r := range repos {
		wg.Add(1)
		go func(r repoEntry) {
			defer wg.Done()
			destRepo := filepath.Join(destAbs, r.Rel)
			state, err := readRepoState(r.Abs)
			if err != nil {
				errc <- fmt.Errorf("%s: %w", r.Rel, err)
				return
			}
			switch o.method {
			case "clone":
				err = materializeClone(r.Abs, destRepo, state, o.force, g)
			case "worktree":
				err = materializeWorktree(r.Abs, destRepo, state, o.force, g)
			}
			if err != nil {
				errc <- fmt.Errorf("%s: %w", r.Rel, err)
			}
		}(r)
	}
	wg.Wait()
	close(errc)

	var errs []error
	for err := range errc {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d repo(s) failed: %v", len(errs), errs[0])
	}
	return nil
}

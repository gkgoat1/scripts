// Command gitall finds Git repositories and pushes or pulls them to all of
// their remotes.
//
// Repositories are discovered in one of two ways:
//
//   - -from any (default): every directory containing a .git entry under the
//     given roots.
//   - -from prtag: directories containing a .prtag marker (see docs/prtag.md)
//     are treated as project roots and scanned for nested repositories.
//
// A repository is only pushed or pulled when it has no uncommitted changes,
// unless the -m flag is provided to stage and commit them first.
//
// Local (filesystem) remotes are handled recursively so that a chain of local
// mirrors syncs end to end:
//
//   - push:   the current repository is pushed first, then each local remote is
//     pushed (recursively) afterwards.
//   - pull:   each local remote is pulled (recursively) first, then the current
//     repository is pulled.
//
// For example, given ~/work --origin--> ~/mirror --origin--> github.com, a push
// of ~/work propagates to ~/mirror and then to GitHub, while a pull flows the
// other way. Cycles are prevented by tracking the repositories on the current
// recursion path (resolved through symlinks).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gkgoat1/scripts/prtag"
)

type opts struct {
	mode      string // "any" or "prtag"
	action    string // "push" or "pull"
	all       bool   // push --all --tags
	rebase    bool   // pull --rebase
	commitMsg string // if set, commit uncommitted changes before push/pull
	dryRun    bool
	verbose   bool
}

func main() {
	mode := flag.String("from", "any", `discovery mode: "any" (dirs with .git) or "prtag" (dirs with a .prtag marker, scanned for repos)`)
	all := flag.Bool("all", false, "push all branches and tags (push only)")
	rebase := flag.Bool("rebase", false, "pull with --rebase (pull only)")
	commitMsg := flag.String("m", "", "commit message: if set, commit uncommitted changes before pushing or pulling")
	dryRun := flag.Bool("n", false, "dry run: print actions without running git")
	verbose := flag.Bool("v", false, "verbose output")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: gitall [flags] <push|pull> [root ...]")
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprintln(flag.CommandLine.Output(), "flags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	action := strings.ToLower(args[0])
	roots := args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}
	switch action {
	case "push", "pull":
	default:
		fmt.Fprintf(os.Stderr, "gitall: action must be push or pull, got %q\n", action)
		os.Exit(2)
	}
	switch *mode {
	case "any", "prtag":
	default:
		fmt.Fprintf(os.Stderr, "gitall: -from must be any or prtag, got %q\n", *mode)
		os.Exit(2)
	}

	o := opts{mode: *mode, action: action, all: *all, rebase: *rebase, commitMsg: *commitMsg, dryRun: *dryRun, verbose: *verbose}

	repos, err := discoverRepos(*mode, roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gitall: discovery: %v\n", err)
		os.Exit(1)
	}
	repos = dedupeRepos(repos)
	if len(repos) == 0 {
		fmt.Println("gitall: no repositories found")
		return
	}
	if o.verbose {
		fmt.Printf("gitall: %d repository(s) discovered\n", len(repos))
	}

	// Each discovered repo is operated on independently with a fresh
	// recursion stack. A repo reached both via discovery and as a local
	// remote may therefore be operated on more than once; that is correct;
	// the second pass propagates any commits the first pass delivered to it.
	failed := 0
	results := make(chan bool, len(repos))
	for _, r := range repos {
		go func(r string) {
			results <- operate(r, o, map[string]bool{})
		}(r)
	}
	for range repos {
		if !<-results {
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// operate pushes or pulls a single repository, recursing into local remotes.
// stack holds the repositories on the current recursion path (resolved through
// symlinks) to break cycles. It returns false if any git operation for this
// repository (or a descendant recursion) failed.
//
// Local remotes are processed concurrently; dependency order is still honored
// because each repository waits for its local-remote children (pull) or
// finishes before them (push).
func operate(repo string, o opts, stack map[string]bool) bool {
	rp, err := filepath.EvalSymlinks(repo)
	if err != nil {
		rp = repo
	}
	if stack[rp] {
		return true // cycle: already on this recursion path
	}

	remotes, err := o.remotes(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: %v\n", repo, err)
		return false
	}
	local := localRemotes(repo, remotes)

	clean, err := o.maybeCommit(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: commit: %v\n", repo, err)
		return false
	}

	childStack := copyStack(stack)
	childStack[rp] = true

	if o.action == "push" {
		ok := pushRepo(repo, clean, remotes, o)
		if !operateAll(repo, local, o, childStack) {
			ok = false
		}
		return ok
	}
	ok := operateAll(repo, local, o, childStack)
	if !pullRepo(repo, clean, remotes, o) {
		ok = false
	}
	return ok
}

// pushRepo pushes the current repository to all remotes when it is clean.
func pushRepo(repo string, clean bool, remotes []string, o opts) bool {
	if !clean {
		fmt.Printf("[skip] %s: uncommitted changes\n", repo)
		return true
	}
	ok := true
	for _, r := range remotes {
		if url, err := remoteURL(repo, r); err == nil {
			if lr, lok := resolveLocalRemote(repo, url); lok {
				o.ensurePushable(lr)
			}
		}
		fmt.Printf("[push] %s -> %s\n", repo, r)
		if err := o.git(repo, "push", r); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: push %s: %v\n", repo, r, err)
			ok = false
		}
		if o.all {
			if err := o.git(repo, "push", r, "--all"); err != nil {
				fmt.Fprintf(os.Stderr, "[error] %s: push --all %s: %v\n", repo, r, err)
				ok = false
			}
			if err := o.git(repo, "push", r, "--tags"); err != nil {
				fmt.Fprintf(os.Stderr, "[error] %s: push --tags %s: %v\n", repo, r, err)
				ok = false
			}
		}
	}
	return ok
}

// pullRepo pulls the current repository from all remotes when it is clean.
func pullRepo(repo string, clean bool, remotes []string, o opts) bool {
	if !clean {
		fmt.Printf("[skip] %s: uncommitted changes\n", repo)
		return true
	}
	ok := true
	for _, r := range remotes {
		fmt.Printf("[pull] %s <- %s\n", repo, r)
		args := []string{"pull"}
		if o.rebase {
			args = append(args, "--rebase")
		}
		args = append(args, r)
		if err := o.git(repo, args...); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: pull %s: %v\n", repo, r, err)
			ok = false
		}
	}
	return ok
}

// operateAll runs operate on each repository concurrently and returns true
// only if every operation succeeded. parent is used only for logging the
// dependency edge.
func operateAll(parent string, repos []string, o opts, stack map[string]bool) bool {
	if len(repos) == 0 {
		return true
	}
	results := make(chan bool, len(repos))
	for _, r := range repos {
		fmt.Printf("[recurse] %s -> %s\n", parent, r)
		go func(r string) {
			results <- operate(r, o, stack)
		}(r)
	}
	ok := true
	for range repos {
		if !<-results {
			ok = false
		}
	}
	return ok
}

// copyStack returns a shallow copy of stack for use by child goroutines.
func copyStack(stack map[string]bool) map[string]bool {
	c := make(map[string]bool, len(stack)+1)
	for k, v := range stack {
		c[k] = v
	}
	return c
}

// localRemotes returns the resolved filesystem paths of remotes that point to a
// local git repository.
func localRemotes(repo string, remotes []string) []string {
	var out []string
	for _, r := range remotes {
		url, err := remoteURL(repo, r)
		if err != nil {
			continue
		}
		if lr, ok := resolveLocalRemote(repo, url); ok {
			out = append(out, lr)
		}
	}
	return out
}

// ---- git helpers ----

func (o opts) git(repo string, args ...string) error {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q %s\n", repo, strings.Join(args, " "))
		return nil
	}
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (o opts) remotes(repo string) ([]string, error) {
	out, err := o.capture(repo, "remote")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func remoteURL(repo, remote string) (string, error) {
	out, err := capture(repo, "remote", "get-url", remote)
	return strings.TrimSpace(out), err
}

func (o opts) isClean(repo string) (bool, error) {
	if bare, err := o.isBare(repo); err == nil && bare {
		return true, nil
	}
	out, err := o.capture(repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// maybeCommit returns true if repo has no uncommitted changes. If it does and
// o.commitMsg is set, it stages all changes and commits them, then returns
// true on success. When o.commitMsg is empty and the repo is dirty, it returns
// false so the caller can skip the push/pull.
func (o opts) maybeCommit(repo string) (bool, error) {
	clean, err := o.isClean(repo)
	if err != nil || clean {
		return clean, err
	}
	if o.commitMsg == "" {
		return false, nil
	}
	fmt.Printf("[commit] %s: %s\n", repo, o.commitMsg)
	if err := o.git(repo, "add", "-A"); err != nil {
		return false, fmt.Errorf("add: %w", err)
	}
	if err := o.git(repo, "commit", "-m", o.commitMsg); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// ensurePushable makes a non-bare local remote accept pushes to its current
// branch. Mid-chain repos are working trees whose current branch is checked
// out, which git denies by default; updateInstead updates the working tree to
// match the incoming ref so the mirror stays in sync.
func (o opts) ensurePushable(target string) {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q config receive.denyCurrentBranch updateInstead\n", target)
		return
	}
	var sink bytes.Buffer
	cmd := exec.Command("git", "-C", target, "config", "receive.denyCurrentBranch", "updateInstead")
	cmd.Stdout = &sink
	cmd.Stderr = &sink
	_ = cmd.Run()
}

func (o opts) isBare(repo string) (bool, error) {
	out, err := o.capture(repo, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

func (o opts) capture(repo string, args ...string) (string, error) {
	return capture(repo, args...)
}

func capture(repo string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// ---- local remote resolution ----

// resolveLocalRemote returns the evaluated, real path of url if it refers to a
// local git repository, reporting ok=false for network remotes or non-repo
// paths.
func resolveLocalRemote(repo, url string) (string, bool) {
	p := url
	if strings.HasPrefix(p, "file://") {
		p = strings.TrimPrefix(p, "file://")
		p = strings.TrimPrefix(p, "localhost")
	}
	if strings.Contains(p, "://") {
		return "", false // http(s)://, ssh://, git://, ...
	}
	// scp-like syntax: [user@]host:path (colon before any slash)
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
	if !isGitRepo(ev) {
		return "", false
	}
	return ev, true
}

// isGitRepo reports whether path is a git repository (working tree or bare).
func isGitRepo(path string) bool {
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	cmd.Stdout = new(bytes.Buffer)
	cmd.Stderr = new(bytes.Buffer)
	return cmd.Run() == nil
}

// hasGitDir reports whether path contains a .git entry (cheap discovery check).
func hasGitDir(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

// ---- discovery ----

func discoverRepos(mode string, roots []string) ([]string, error) {
	if mode == "prtag" {
		return discoverPrtag(roots)
	}
	return discoverAny(roots)
}

// discoverAny walks the roots and returns every directory containing a .git
// entry. It does not descend into discovered repositories.
func discoverAny(roots []string) ([]string, error) {
	var repos []string
	for _, root := range roots {
		r, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			if hasGitDir(path) {
				repos = append(repos, path)
				return fs.SkipDir // don't descend into a found repo
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return repos, nil
}

// discoverPrtag finds .prtag markers under roots and scans each marker's
// directory for nested git repositories.
func discoverPrtag(roots []string) ([]string, error) {
	type proj struct {
		dir  string
		name string
	}
	var projects []proj
	for _, root := range roots {
		r, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if d.Name() == ".prtag" {
					dir := filepath.Dir(path)
					name := dir
					if f, perr := prtag.ReadFile(path); perr == nil && f.Name != "" {
						name = f.Name
					}
					projects = append(projects, proj{dir: dir, name: name})
				}
				return nil
			}
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Dedupe project dirs, preserving discovery order.
	seen := map[string]bool{}
	var repos []string
	for _, p := range projects {
		rp, err := filepath.EvalSymlinks(p.dir)
		if err != nil {
			rp = p.dir
		}
		if seen[rp] {
			continue
		}
		seen[rp] = true
		fmt.Printf("[project] %s (%s)\n", p.dir, p.name)
		rs, err := discoverAny([]string{p.dir})
		if err != nil {
			return nil, err
		}
		repos = append(repos, rs...)
	}
	return repos, nil
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

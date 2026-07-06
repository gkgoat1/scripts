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
//   - push:   each local remote is pulled (recursively) first, then the current
//     repository is synced and pushed, then each local remote is synced and
//     pushed (recursively) afterwards. Before each push, gitall fetches the
//     remote and fast-forwards the current branch when possible.
//   - pull:   each local remote is pulled (recursively) first, then the current
//     repository is pulled.
//
// For example, given ~/work --origin--> ~/mirror --origin--> github.com, a push
// of ~/work pulls upstream into mirror first, syncs and pushes work, then syncs
// and pushes mirror to GitHub. A pull flows the other way. Cycles are prevented
// by tracking the repositories on the current recursion path (resolved through
// symlinks).
//
// With -allow-merge, a push that cannot fast-forward will attempt a merge
// commit when there are no conflicts; -m sets the merge message (and still
// commits uncommitted changes before push/pull when set).
//
// With -pr, a failed push to a GitHub remote falls back to opening (or
// updating) a pull request via the gh CLI instead of failing outright. The PR
// branch is named gitall-pr/<base>-<N>: if an open PR already exists from a
// prior gitall-pr/<base>-* branch whose tip is an ancestor of the current
// HEAD, that branch is fast-forwarded and its PR is reused; otherwise a new
// sequentially numbered branch and PR are created.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gkgoat1/scripts/prtag"
)

type opts struct {
	mode          string // "any" or "prtag"
	action        string // "push" or "pull"
	all           bool   // push --all --tags
	rebase        bool   // pull --rebase
	commitMsg     string // if set, commit uncommitted changes before push/pull
	dryRun        bool
	verbose       bool
	createPR      bool // on push failure to a GitHub remote, open/update a PR via gh
	allowMerge    bool // on push, merge when ff-only sync fails and there are no conflicts
	skipPullFirst bool // internal: skip phase-1 pull chain during push recursion
}

func (o opts) withAction(action string) opts {
	o.action = action
	return o
}

func (o opts) withSkipPullFirst(skip bool) opts {
	o.skipPullFirst = skip
	return o
}

func main() {
	mode := flag.String("from", "any", `discovery mode: "any" (dirs with .git) or "prtag" (dirs with a .prtag marker, scanned for repos)`)
	all := flag.Bool("all", false, "push all branches and tags (push only)")
	rebase := flag.Bool("rebase", false, "pull with --rebase (pull only)")
	commitMsg := flag.String("m", "", "commit message: if set, commit uncommitted changes before pushing or pulling")
	dryRun := flag.Bool("n", false, "dry run: print actions without running git")
	verbose := flag.Bool("v", false, "verbose output")
	createPR := flag.Bool("pr", false, "on push failure to a GitHub remote, open/update a PR via gh")
	allowMerge := flag.Bool("allow-merge", false, "on push, merge remote changes when fast-forward is not possible (push only)")
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

	fmt.Printf("commitMsg: %q\n", *commitMsg)

	o := opts{mode: *mode, action: action, all: *all, rebase: *rebase, commitMsg: *commitMsg, dryRun: *dryRun, verbose: *verbose, createPR: *createPR, allowMerge: *allowMerge}

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
// pulls them before pushing (push).
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
		return operatePush(repo, clean, remotes, local, o, childStack)
	}
	ok := operateAll(repo, local, o, childStack)
	if !pullRepo(repo, clean, remotes, o) {
		ok = false
	}
	return ok
}

// operatePush syncs and pushes a repository in three phases: pull the local
// remote chain first (unless skipPullFirst), sync and push the current repo,
// then sync and push each local remote (recursively, with skipPullFirst set).
func operatePush(repo string, clean bool, remotes, local []string, o opts, stack map[string]bool) bool {
	ok := true
	if !o.skipPullFirst {
		if !operateAll(repo, local, o.withAction("pull"), stack) {
			ok = false
		}
	}
	if !syncAndPushRepo(repo, clean, remotes, o) {
		ok = false
	}
	if !operateAll(repo, local, o.withSkipPullFirst(true), stack) {
		ok = false
	}
	return ok
}

// syncAndPushRepo fetches and fast-forwards (or merges with -allow-merge) from
// each remote, then pushes. PR fallback runs only after a push still fails.
func syncAndPushRepo(repo string, clean bool, remotes []string, o opts) bool {
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
		if err := o.syncRemote(repo, r); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: sync %s: %v\n", repo, r, err)
			ok = false
			continue
		}
		fmt.Printf("[push] %s -> %s\n", repo, r)
		if err := o.git(repo, "push", r); err != nil {
			if o.createPR {
				if prErr := o.fallbackCreatePR(repo, r); prErr != nil {
					fmt.Fprintf(os.Stderr, "[error] %s: push %s: %v\n", repo, r, err)
					fmt.Fprintf(os.Stderr, "[error] %s: pr fallback %s: %v\n", repo, r, prErr)
					ok = false
				}
			} else {
				fmt.Fprintf(os.Stderr, "[error] %s: push %s: %v\n", repo, r, err)
				ok = false
			}
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

// syncRemote fetches remote and fast-forwards the current branch. When ff-only
// fails and -allow-merge is set, it attempts a merge commit instead.
func (o opts) syncRemote(repo, remote string) error {
	bare, err := o.isBare(repo)
	if err != nil {
		return err
	}
	if bare {
		return nil
	}
	branch, err := o.currentBranch(repo)
	if err != nil {
		return err
	}
	if branch == "HEAD" {
		fmt.Printf("[skip] %s: sync %s: detached HEAD\n", repo, remote)
		return nil
	}
	fmt.Printf("[sync] %s <- %s\n", repo, remote)
	if err := o.git(repo, "fetch", remote); err != nil {
		fmt.Printf("[skip] %s: sync %s: fetch failed\n", repo, remote)
		return nil
	}
	ref := remote + "/" + branch
	if !o.remoteBranchExists(repo, ref) {
		fmt.Printf("[skip] %s: sync %s: no remote branch %s\n", repo, remote, branch)
		return nil
	}
	if err := o.git(repo, "merge", "--ff-only", ref); err != nil {
		if !o.allowMerge {
			fmt.Printf("[sync] %s: %s: cannot fast-forward (use -allow-merge to merge)\n", repo, remote)
			return nil
		}
		msg := o.commitMsg
		if msg == "" {
			msg = fmt.Sprintf("gitall: merge %s/%s", remote, branch)
		}
		fmt.Printf("[merge] %s: %s/%s\n", repo, remote, branch)
		if err := o.git(repo, "merge", ref, "-m", msg,"--no-ff"); err != nil {
			return fmt.Errorf("merge: %w", err)
		}
	}
	return nil
}

func (o opts) remoteBranchExists(repo, ref string) bool {
	_, err := o.capture(repo, "rev-parse", "--verify", ref)
	return err == nil
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

// remotePushURL returns the URL remote actually pushes to, which may differ
// from remoteURL if a separate push URL (or pushInsteadOf rewrite) is
// configured for it.
func remotePushURL(repo, remote string) (string, error) {
	out, err := capture(repo, "remote", "get-url", "--push", remote)
	return strings.TrimSpace(out), err
}

// ---- GitHub PR fallback ----

// githubRepoSlug extracts "owner/repo" from a GitHub remote URL (SSH,
// ssh://, or http(s)://), reporting ok=false for any other host or malformed
// URL.
func githubRepoSlug(url string) (string, bool) {
	u := strings.TrimSuffix(url, ".git")
	prefixes := []string{"git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/"}
	for _, p := range prefixes {
		if !strings.HasPrefix(u, p) {
			continue
		}
		rest := strings.TrimPrefix(u, p)
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "/" + parts[1], true
		}
		return "", false
	}
	return "", false
}

// prBranchName returns the name of the Nth PR branch gitall creates for base.
func prBranchName(base string, n int) string {
	return fmt.Sprintf("gitall-pr/%s-%d", base, n)
}

// matchPRBranch reports whether name is a branch gitall created for base
// (gitall-pr/<base>-<N>), returning N.
func matchPRBranch(name, base string) (int, bool) {
	prefix := "gitall-pr/" + base + "-"
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func (o opts) currentBranch(repo string) (string, error) {
	out, err := o.capture(repo, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// gh runs the gh CLI in repo, streaming its output like o.git.
func (o opts) gh(repo string, args ...string) error {
	if o.dryRun {
		fmt.Printf("  [dry-run] gh -C %q %s\n", repo, strings.Join(args, " "))
		return nil
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ghJSON runs the gh CLI in repo and decodes its JSON stdout into v.
func (o opts) ghJSON(repo string, args []string, v any) error {
	var out bytes.Buffer
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return json.Unmarshal(out.Bytes(), v)
}

// openPR is the subset of `gh pr list --json` fields fallbackCreatePR needs.
type openPR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
}

// openPRsFrom returns open PRs against base whose head branch was created by
// this tool (gitall-pr/<base>-<N>), sorted ascending by N.
func (o opts) openPRsFrom(repo, slug, base string) ([]openPR, error) {
	var all []openPR
	args := []string{"pr", "list", "-R", slug, "--base", base, "--state", "open", "--json", "number,headRefName,headRefOid", "--limit", "100"}
	if err := o.ghJSON(repo, args, &all); err != nil {
		return nil, err
	}
	numOf := func(pr openPR) int {
		n, _ := matchPRBranch(pr.HeadRefName, base)
		return n
	}
	var out []openPR
	for _, pr := range all {
		if _, ok := matchPRBranch(pr.HeadRefName, base); ok {
			out = append(out, pr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return numOf(out[i]) < numOf(out[j]) })
	return out, nil
}

// remoteBranchNumbers returns all N in use by gitall-pr/<base>-<N> branches on
// the repo at pushURL, regardless of PR state, so a closed PR's branch number
// is never reused.
func (o opts) remoteBranchNumbers(repo, pushURL, base string) ([]int, error) {
	out, err := o.capture(repo, "ls-remote", "--heads", pushURL, "gitall-pr/"+base+"-*")
	if err != nil {
		return nil, err
	}
	var nums []int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "refs/heads/")
		if n, ok := matchPRBranch(name, base); ok {
			nums = append(nums, n)
		}
	}
	return nums, nil
}

// isAncestorOfHead reports whether sha (a commit at pushURL) is an ancestor
// of the local HEAD, fetching it first so it's available locally.
func (o opts) isAncestorOfHead(repo, pushURL, sha string) (bool, error) {
	if err := o.git(repo, "fetch", pushURL, sha); err != nil {
		return false, err
	}
	_, err := o.capture(repo, "merge-base", "--is-ancestor", sha, "HEAD")
	return err == nil, nil
}

// fallbackCreatePR is invoked when a push to remote fails and -pr is set. It
// reuses an existing open PR from this tool if one's tip is an ancestor of
// HEAD, fast-forwarding its branch; otherwise it pushes a new sequentially
// numbered branch and opens a PR for it.
func (o opts) fallbackCreatePR(repo, remote string) error {
	url, err := remoteURL(repo, remote)
	if err != nil {
		return fmt.Errorf("remote url: %w", err)
	}
	slug, ok := githubRepoSlug(url)
	if !ok {
		return fmt.Errorf("not a GitHub remote: %s", url)
	}
	base, err := o.currentBranch(repo)
	if err != nil {
		return fmt.Errorf("current branch: %w", err)
	}
	if base == "HEAD" {
		return fmt.Errorf("cannot open a PR from a detached HEAD")
	}
	pushURL, err := remotePushURL(repo, remote)
	if err != nil {
		return fmt.Errorf("remote push url: %w", err)
	}

	candidates, err := o.openPRsFrom(repo, slug, base)
	if err != nil {
		return fmt.Errorf("list open PRs: %w", err)
	}
	for _, c := range candidates {
		ancestor, err := o.isAncestorOfHead(repo, pushURL, c.HeadRefOid)
		if err != nil || !ancestor {
			continue
		}
		fmt.Printf("[pr] %s: updating existing PR #%d (%s)\n", repo, c.Number, c.HeadRefName)
		if err := o.git(repo, "push", remote, "HEAD:refs/heads/"+c.HeadRefName); err != nil {
			return fmt.Errorf("push %s: %w", c.HeadRefName, err)
		}
		return nil
	}

	used, err := o.remoteBranchNumbers(repo, pushURL, base)
	if err != nil {
		return fmt.Errorf("list remote branches: %w", err)
	}
	n := 1
	for _, u := range used {
		if u >= n {
			n = u + 1
		}
	}
	head := prBranchName(base, n)
	fmt.Printf("[pr] %s: push failed, creating PR branch %s -> %s\n", repo, head, base)
	if err := o.git(repo, "push", remote, "HEAD:refs/heads/"+head); err != nil {
		return fmt.Errorf("push %s: %w", head, err)
	}
	fmt.Printf("[pr] %s: creating pull request %s -> %s on %s\n", repo, head, base, slug)
	if err := o.gh(repo, "pr", "create", "-R", slug, "--head", head, "--base", base, "--fill"); err != nil {
		return fmt.Errorf("gh pr create: %w", err)
	}
	return nil
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
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "Library" {
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

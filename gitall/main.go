// Command gitall finds Git repositories and pushes or pulls every local branch
// to all of their remotes.
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
//   - push:   each local remote is pulled (recursively) first, then every
//     local branch of the current repository is synced and pushed, then each
//     local remote is synced and pushed (recursively) afterwards. Before each
//     branch is pushed, gitall fetches the remote and fast-forwards that branch
//     when possible via ref/index plumbing (never checking out other branches).
//   - pull:   each local remote is pulled (recursively) first, then every local
//     branch of the current repository is pulled the same way.
//
// For example, given ~/work --origin--> ~/mirror --origin--> github.com, a push
// of ~/work pulls upstream into mirror first, syncs and pushes work, then syncs
// and pushes mirror to GitHub. A pull flows the other way. Cycles are prevented
// by tracking the repositories on the current recursion path (resolved through
// symlinks).
//
// With -allow-merge=<mode>, a push that cannot fast-forward can create a
// merge commit. Valid modes are none (default), local (merge local remotes
// only), remote (merge any remote), and pr (merge any remote and fall back to
// a GitHub PR on push failure). Bare -allow-merge is shorthand for pr. The
// -m flag sets the merge commit message.
//
// With merge mode pr (or the deprecated -pr flag), a failed push to a GitHub
// remote falls back to opening (or updating) a pull request via the gh CLI.
// PRs are always created against the remote of the failed push, never an
// upstream. The PR branch is named gitall-pr/<base>-<N>: if an open PR
// already exists from a prior gitall-pr/<base>-* branch whose tip is an
// ancestor of the failed branch tip, that branch is fast-forwarded and its PR
// is reused; otherwise a new sequentially numbered branch and PR are created.
//
// A per-command timeout can be set with -timeout (for example, -timeout=30s)
// or via the GITALL_TIMEOUT environment variable, and a default can be
// configured in ~/.config/interpose/config with the key "tool-timeout".
package main

import (
	"bytes"
	"context"
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
	"sync"
	"time"

	"github.com/gkgoat1/scripts/internal/proxypass"
	"github.com/gkgoat1/scripts/internal/restoreconflict"
	"github.com/gkgoat1/scripts/interpose/config"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
	"github.com/gkgoat1/scripts/prtag"
)

// MergeMode controls how gitall handles non-fast-forward situations.
type MergeMode int

const (
	mergeNone   MergeMode = iota // never merge
	mergeLocal                   // merge into local (filesystem) remotes only
	mergeRemote                  // merge into local and network remotes
	mergePR                      // merge into remotes, then fall back to PR on push failure
)

// expandBareAllowMerge turns a bare "-allow-merge" argument into
// "-allow-merge=pr" so the string flag can parse it, while leaving
// "-allow-merge=local" and friends untouched.
func expandBareAllowMerge(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "-allow-merge" || a == "--allow-merge" {
			out[i] = "-allow-merge=pr"
		} else {
			out[i] = a
		}
	}
	return out
}

// parseMergeMode parses a merge-mode flag value. It accepts integer levels
// 0-3 or the names none/local/remote/pr. An empty string parses to mergeNone
// to support the bare flag semantics elsewhere.
func parseMergeMode(s string) (MergeMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "none":
		return mergeNone, true
	case "1", "local":
		return mergeLocal, true
	case "2", "remote":
		return mergeRemote, true
	case "3", "pr":
		return mergePR, true
	}
	return mergeNone, false
}

func (m MergeMode) String() string {
	switch m {
	case mergeNone:
		return "none"
	case mergeLocal:
		return "local"
	case mergeRemote:
		return "remote"
	case mergePR:
		return "pr"
	}
	return fmt.Sprintf("MergeMode(%d)", int(m))
}

type opts struct {
	mode          string // "any" or "prtag"
	action        string // "push" or "pull"
	all           bool   // push tags too (all branches are always pushed)
	rebase        bool   // pull --rebase
	commitMsg     string // if set, commit uncommitted changes before push/pull
	dryRun        bool
	verbose       bool
	mergeMode     MergeMode     // how to handle non-fast-forward syncs and push failures
	skipPullFirst bool          // internal: skip phase-1 pull chain during push recursion
	timeout       time.Duration // maximum time any single external tool invocation may run
	proxyURL      string        // if non-empty, inject HTTP_PROXY/HTTPS_PROXY into child git processes
	locks         *repoLocks    // per-repo concurrency guard
}

// repoLocks serializes all operations on a single resolved repository path.
type repoLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// newRepoLocks creates an empty lock registry.
func newRepoLocks() *repoLocks {
	return &repoLocks{locks: make(map[string]*sync.Mutex)}
}

// withLock runs f while holding the mutex for path. The path should already
// be resolved via filepath.EvalSymlinks.
func (r *repoLocks) withLock(path string, f func()) {
	if r == nil {
		f()
		return
	}
	r.mu.Lock()
	lk, ok := r.locks[path]
	if !ok {
		lk = &sync.Mutex{}
		r.locks[path] = lk
	}
	r.mu.Unlock()
	lk.Lock()
	defer lk.Unlock()
	f()
}

func (o opts) withAction(action string) opts {
	o.action = action
	return o
}

func (o opts) withSkipPullFirst(skip bool) opts {
	o.skipPullFirst = skip
	return o
}

// ctx returns a context for the current operation. If a timeout is configured,
// the returned context is cancelled after that duration.
func (o opts) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if o.timeout > 0 {
		return context.WithTimeout(parent, o.timeout)
	}
	return parent, func() {}
}

// wrapTimeout improves error messages when a command is killed because it
// exceeded the configured timeout.
func (o opts) wrapTimeout(ctx context.Context, err error) error {
	if err == nil || ctx.Err() != context.DeadlineExceeded {
		return err
	}
	return fmt.Errorf("timed out after %s: %w", o.timeout, err)
}

func main() {
	mode := flag.String("from", "any", `discovery mode: "any" (dirs with .git) or "prtag" (dirs with a .prtag marker, scanned for repos)`)
	all := flag.Bool("all", false, "push tags too (all branches are always pushed)")
	rebase := flag.Bool("rebase", false, "pull with --rebase (pull only)")
	commitMsg := flag.String("m", "", "commit message: if set, commit uncommitted changes before pushing or pulling")
	timeout := flag.Duration("timeout", 0, "tool timeout: maximum time any single external command may run (e.g. 30s); 0 disables")
	if env := os.Getenv("GITALL_TIMEOUT"); env != "" {
		if d, err := time.ParseDuration(env); err == nil {
			*timeout = d
		} else {
			fmt.Fprintf(os.Stderr, "gitall: invalid GITALL_TIMEOUT %q: %v\n", env, err)
			os.Exit(2)
		}
	}
	dryRun := flag.Bool("n", false, "dry run: print actions without running git")
	verbose := flag.Bool("v", false, "verbose output")
	proxy := flag.Bool("proxy", false, "start a loopback passthrough proxy and inject it into child git processes")
	// -pr is retained as an alias for -allow-merge=pr.
	prFlag := flag.Bool("pr", false, "deprecated: use -allow-merge=pr instead")

	// -allow-merge now accepts a value (none/local/remote/pr or 0-3) in
	// addition to the old bare flag, which maps to the most permissive mode.
	// Expand a bare "-allow-merge" to "-allow-merge=pr" before flag.Parse.
	args := expandBareAllowMerge(os.Args[1:])
	mergeStr := flag.String("allow-merge", "none", "merge mode on non-fast-forward: none (default), local, remote, pr (bare flag means pr)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: gitall [flags] <push|pull> [root ...]")
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprintln(flag.CommandLine.Output(), "flags:")
		flag.PrintDefaults()
	}
	flag.CommandLine.Parse(args)

	cmdArgs := flag.Args()
	if len(cmdArgs) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	action := strings.ToLower(cmdArgs[0])
	roots := cmdArgs[1:]
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

	if *timeout == 0 {
		if d, err := time.ParseDuration(config.Load().ToolTimeout); err == nil && d > 0 {
			*timeout = d
		}
	}

	mergeMode, ok := parseMergeMode(*mergeStr)
	if !ok {
		fmt.Fprintf(os.Stderr, "gitall: -allow-merge must be none, local, remote, pr, or 0-3, got %q\n", *mergeStr)
		os.Exit(2)
	}

	if *prFlag {
		mergeMode = mergePR
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o := opts{
		mode:      *mode,
		action:    action,
		all:       *all,
		rebase:    *rebase,
		commitMsg: *commitMsg,
		dryRun:    *dryRun,
		verbose:   *verbose,
		mergeMode: mergeMode,
		timeout:   *timeout,
		locks:     newRepoLocks(),
	}

	if *proxy {
		px, err := proxypass.Start(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gitall: start proxy: %v\n", err)
			os.Exit(1)
		}
		o.proxyURL = px.URLString()
		fmt.Printf("[proxy] gitall: child proxy on %s\n", o.proxyURL)
	}

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
//
// Access to each resolved repo is serialized by o.locks so concurrent pushes
// or pulls to the same repository do not corrupt refs, index, or working tree.
func operate(repo string, o opts, stack map[string]bool) bool {
	rp, err := filepath.EvalSymlinks(repo)
	if err != nil {
		rp = repo
	}
	if stack[rp] {
		return true // cycle: already on this recursion path
	}

	var ok bool
	o.locks.withLock(rp, func() {
		ok = operateLocked(repo, rp, o, stack)
	})
	return ok
}

func operateLocked(repo, rp string, o opts, stack map[string]bool) bool {
	remotes, err := o.remotes(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: %v\n", repo, err)
		return false
	}

	if err := restoreConflicted(repo, o.dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: restore conflicts: %v\n", repo, err)
		return false
	}
	local := localRemotes(o, repo, remotes)

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

// checkoutHead force-updates the index and working tree of repo to match HEAD.
// It is used after a local tip has been moved by ref plumbing (or a push into
// a local remote) so the checked-out branch's worktree matches the new tip.
// Failures are logged but not fatal. -f is required because update-ref alone
// leaves the old index staged relative to the new HEAD; plain checkout HEAD
// will not remove those entries.
func checkoutHead(repo string, o opts) {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q checkout -f HEAD\n", repo)
		return
	}
	if bare, err := o.isBare(repo); err == nil && bare {
		return
	}
	if err := o.git(repo, "checkout", "-f", "HEAD"); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: checkout -f HEAD: %v\n", repo, err)
	}
}

// syncAndPushRepo fetches and fast-forwards (or merges according to mergeMode)
// every local branch from each remote, then pushes every local branch. PR
// fallback runs only when mergeMode >= mergePR and a push to a network remote
// fails.
//
// Non-current branches are updated with ref/index plumbing so the working tree
// and HEAD stay put. The checked-out branch is refreshed with checkout HEAD
// only when that branch's tip itself moves.
//
// For local remotes, both the sync (fetch/merge into the current repo) and the
// push into the target repo are protected by the target repo's mutex so that
// concurrent pushes to the same local mirror do not corrupt its refs.
func syncAndPushRepo(repo string, clean bool, remotes []string, o opts) bool {
	if !clean {
		fmt.Printf("[skip] %s: uncommitted changes\n", repo)
		return true
	}
	ok := true
	for _, r := range remotes {
		url, urlErr := o.remoteURL(repo, r)
		isLocal := false
		var localPath string
		if urlErr == nil {
			if lr, lok := resolveLocalRemote(repo, url); lok {
				isLocal = true
				localPath = lr
				o.ensurePushable(lr)
			}
		}

		remoteOK := false
		syncPush := func() {
			remoteOK = syncAndPushRemote(repo, r, isLocal, o)
			if !remoteOK {
				ok = false
			}
		}
		// Lock local remotes while reading/writing them so concurrent
		// pushes from different source repos do not corrupt refs.
		if isLocal && localPath != "" {
			o.locks.withLock(localPath, syncPush)
			if remoteOK {
				checkoutHead(localPath, o)
			}
		} else {
			syncPush()
		}
	}
	return ok
}

// syncAndPushRemote synchronizes and pushes each local branch to remote. It
// intentionally pushes branches individually instead of using `push --all`:
// that preserves per-branch merge and PR-fallback behavior and lets one
// unpushable branch be reported without skipping the others.
func syncAndPushRemote(repo, remote string, isLocal bool, o opts) bool {
	ok := true
	bare, bareErr := o.isBare(repo)
	if bareErr != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: detect bare repository: %v\n", repo, bareErr)
		return false
	}
	fetched := true
	if !bare {
		if err := o.git(repo, "fetch", remote); err != nil {
			fmt.Printf("[skip] %s: sync %s: fetch failed\n", repo, remote)
			fetched = false
		}
	}
	if !forEachLocalBranch(repo, o, func(branch string) bool {
		if fetched && !bare {
			updated, err := o.syncRemote(repo, remote, branch, isLocal)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[error] %s: sync %s (%s): %v\n", repo, remote, branch, err)
				return false
			}
			if updated {
				o.maybeCheckoutHead(repo, branch)
			}
		}

		fmt.Printf("[push] %s (%s) -> %s\n", repo, branch, remote)
		refspec := branch + ":refs/heads/" + branch
		if err := o.git(repo, "push", remote, refspec); err != nil {
			if !isLocal && o.mergeMode >= mergePR {
				if prErr := o.fallbackCreatePR(repo, remote, branch); prErr != nil {
					fmt.Fprintf(os.Stderr, "[error] %s: push %s (%s): %v\n", repo, remote, branch, err)
					fmt.Fprintf(os.Stderr, "[error] %s: pr fallback %s (%s): %v\n", repo, remote, branch, prErr)
					return false
				}
				return true
			}
			fmt.Fprintf(os.Stderr, "[error] %s: push %s (%s): %v\n", repo, remote, branch, err)
			return false
		}
		return true
	}) {
		ok = false
	}
	if o.all {
		fmt.Printf("[push] %s (tags) -> %s\n", repo, remote)
		if err := o.git(repo, "push", remote, "--tags"); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: push tags %s: %v\n", repo, remote, err)
			ok = false
		}
	}
	return ok
}

// syncRemote fast-forwards branch from remote/<branch> using ref plumbing.
// When a fast-forward is impossible it may create a merge commit depending on
// mergeMode and whether the remote is local. Fetch is assumed to have already
// run. It returns true if the branch tip moved.
func (o opts) syncRemote(repo, remote, branch string, isLocal bool) (bool, error) {
	ref := remote + "/" + branch
	if !o.remoteBranchExists(repo, ref) {
		fmt.Printf("[skip] %s: sync %s: no remote branch %s\n", repo, remote, branch)
		return false, nil
	}
	fmt.Printf("[sync] %s (%s) <- %s\n", repo, branch, remote)
	canMerge := o.mergeMode >= mergeRemote || (isLocal && o.mergeMode >= mergeLocal)
	return o.updateBranchFromRemote(repo, branch, ref, remote, canMerge, false)
}

// updateBranchFromRemote brings refs/heads/<branch> up to date with remoteRef.
// When the histories have diverged, rebase selects a plumbing rebase; otherwise
// a merge commit is created when allowMerge is true.
func (o opts) updateBranchFromRemote(repo, branch, remoteRef, remote string, allowMerge, rebase bool) (bool, error) {
	ours, err := o.branchTip(repo, branch)
	if err != nil {
		return false, err
	}
	theirs, err := o.revParse(repo, remoteRef)
	if err != nil {
		return false, err
	}
	if ours == theirs {
		return false, nil
	}
	theirsIsAncestor, err := o.isAncestor(repo, theirs, ours)
	if err != nil {
		return false, err
	}
	if theirsIsAncestor {
		return false, nil
	}
	oursIsAncestor, err := o.isAncestor(repo, ours, theirs)
	if err != nil {
		return false, err
	}
	if oursIsAncestor {
		if err := o.ffUpdateBranch(repo, branch, theirs); err != nil {
			return false, err
		}
		return true, nil
	}
	if rebase {
		fmt.Printf("[rebase] %s: %s onto %s\n", repo, branch, remoteRef)
		if err := o.rebaseUpdateBranch(repo, branch, ours, theirs); err != nil {
			return false, err
		}
		return true, nil
	}
	if !allowMerge {
		fmt.Printf("[sync] %s: %s: cannot fast-forward (use -allow-merge to merge)\n", repo, remote)
		return false, nil
	}
	msg := o.commitMsg
	if msg == "" {
		msg = fmt.Sprintf("gitall: merge %s/%s", remote, branch)
	}
	fmt.Printf("[merge] %s: %s/%s\n", repo, remote, branch)
	if err := o.mergeUpdateBranch(repo, branch, ours, theirs, msg); err != nil {
		return false, err
	}
	return true, nil
}

func (o opts) branchTip(repo, branch string) (string, error) {
	return o.revParse(repo, "refs/heads/"+branch)
}

func (o opts) revParse(repo, rev string) (string, error) {
	out, err := o.capture(repo, "rev-parse", "--verify", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (o opts) isAncestor(repo, ancestor, descendant string) (bool, error) {
	_, err := o.capture(repo, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	// Non-zero exit from merge-base --is-ancestor means "not an ancestor"
	// when both revs resolve; distinguish that from a real failure.
	if _, verifyErr := o.capture(repo, "rev-parse", "--verify", ancestor); verifyErr != nil {
		return false, err
	}
	if _, verifyErr := o.capture(repo, "rev-parse", "--verify", descendant); verifyErr != nil {
		return false, err
	}
	return false, nil
}

func (o opts) ffUpdateBranch(repo, branch, toSHA string) error {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q update-ref refs/heads/%s %s\n", repo, branch, toSHA)
		return nil
	}
	return o.git(repo, "update-ref", "refs/heads/"+branch, toSHA)
}

// mergeUpdateBranch creates a merge commit of theirs into ours on branch using
// merge-tree and commit-tree, never touching the working tree. Cargo.lock-only
// conflicts are dropped via a temporary index.
func (o opts) mergeUpdateBranch(repo, branch, ours, theirs, msg string) error {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q merge-tree/commit-tree/update-ref %s\n", repo, branch)
		return nil
	}
	tree, conflicts, err := o.mergeTreeWrite(repo, ours, theirs, "")
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		var locks []string
		for _, path := range conflicts {
			if filepath.Base(path) != "Cargo.lock" {
				return fmt.Errorf("merge: conflict in %s", path)
			}
			locks = append(locks, path)
		}
		tree, err = o.stripPathsFromTree(repo, tree, locks)
		if err != nil {
			return fmt.Errorf("resolve Cargo.lock conflicts: %w", err)
		}
	}
	commit, err := o.capture(repo, "commit-tree", tree, "-p", ours, "-p", theirs, "-m", msg)
	if err != nil {
		return fmt.Errorf("commit-tree: %w", err)
	}
	return o.git(repo, "update-ref", "refs/heads/"+branch, strings.TrimSpace(commit))
}

// rebaseUpdateBranch replays ours.. commits onto theirs with merge-tree
// cherry-picks and moves branch to the new tip.
func (o opts) rebaseUpdateBranch(repo, branch, ours, theirs string) error {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q rebase %s onto %s\n", repo, branch, theirs)
		return nil
	}
	out, err := o.capture(repo, "rev-list", "--reverse", theirs+".."+ours)
	if err != nil {
		return fmt.Errorf("rev-list: %w", err)
	}
	onto := theirs
	for _, commit := range strings.Fields(out) {
		parent, err := o.revParse(repo, commit+"^")
		if err != nil {
			return fmt.Errorf("parent of %s: %w", commit, err)
		}
		tree, conflicts, err := o.mergeTreeWrite(repo, onto, commit, parent)
		if err != nil {
			return fmt.Errorf("rebase %s: %w", commit, err)
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("rebase: conflict replaying %s (%s)", commit, strings.Join(conflicts, ", "))
		}
		msg, err := o.capture(repo, "log", "-1", "--pretty=%B", commit)
		if err != nil {
			return err
		}
		authorEnv, err := o.commitAuthorEnv(repo, commit)
		if err != nil {
			return err
		}
		newCommit, err := o.captureWithEnv(repo, authorEnv, "commit-tree", tree, "-p", onto, "-m", msg)
		if err != nil {
			return fmt.Errorf("commit-tree: %w", err)
		}
		onto = strings.TrimSpace(newCommit)
	}
	return o.git(repo, "update-ref", "refs/heads/"+branch, onto)
}

func (o opts) commitAuthorEnv(repo, commit string) ([]string, error) {
	out, err := o.capture(repo, "log", "-1", "--pretty=%an%n%ae%n%aD", commit)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(parts) < 3 {
		return nil, fmt.Errorf("author fields for %s: %q", commit, out)
	}
	return []string{
		"GIT_AUTHOR_NAME=" + parts[0],
		"GIT_AUTHOR_EMAIL=" + parts[1],
		"GIT_AUTHOR_DATE=" + parts[2],
	}, nil
}

// mergeTreeWrite runs git merge-tree --write-tree. mergeBase may be empty.
// On conflicts it still returns the tree OID plus the conflicted paths.
func (o opts) mergeTreeWrite(repo, ours, theirs, mergeBase string) (tree string, conflicts []string, err error) {
	args := []string{"merge-tree", "--write-tree", "--name-only"}
	if mergeBase != "" {
		args = append(args, "--merge-base="+mergeBase)
	}
	args = append(args, ours, theirs)
	out, runErr := o.capture(repo, args...)
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		if runErr != nil {
			return "", nil, fmt.Errorf("merge-tree: %w", runErr)
		}
		return "", nil, fmt.Errorf("merge-tree: empty output")
	}
	tree = strings.TrimSpace(lines[0])
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		conflicts = append(conflicts, line)
	}
	if runErr != nil && len(conflicts) == 0 {
		return "", nil, fmt.Errorf("merge-tree: %w\n%s", runErr, out)
	}
	return tree, conflicts, nil
}

// stripPathsFromTree loads tree into a temporary index, removes paths, and
// writes a new tree OID. The repository working tree is never used.
func (o opts) stripPathsFromTree(repo, tree string, paths []string) (string, error) {
	f, err := os.CreateTemp("", "gitall-index-*")
	if err != nil {
		return "", err
	}
	indexPath := f.Name()
	f.Close()
	defer os.Remove(indexPath)

	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := o.captureWithEnv(repo, env, "read-tree", tree); err != nil {
		return "", fmt.Errorf("read-tree: %w", err)
	}
	rmArgs := append([]string{"rm", "-f", "--cached", "--"}, paths...)
	if _, err := o.captureWithEnv(repo, env, rmArgs...); err != nil {
		return "", fmt.Errorf("rm cached: %w", err)
	}
	out, err := o.captureWithEnv(repo, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func (o opts) remoteBranchExists(repo, ref string) bool {
	_, err := o.capture(repo, "rev-parse", "--verify", ref)
	return err == nil
}

// localBranches returns all local branch names in a stable order. Unlike the
// checked-out branch, each of these is explicitly synchronized so a feature
// branch cannot be silently left behind when another branch is active.
func (o opts) localBranches(repo string) ([]string, error) {
	out, err := o.capture(repo, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// remoteBranches returns the branch names advertised by remote after it has
// been fetched. The symbolic <remote>/HEAD ref is deliberately excluded.
func (o opts) remoteBranches(repo, remote string) ([]string, error) {
	out, err := o.capture(repo, "for-each-ref", "--format=%(refname:strip=3)", "refs/remotes/"+remote)
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, branch := range strings.Fields(out) {
		if branch != "HEAD" {
			branches = append(branches, branch)
		}
	}
	return branches, nil
}

func (o opts) localBranchExists(repo, branch string) bool {
	_, err := o.capture(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// createTrackingBranches materializes any branch present only on a remote as a
// local tracking branch. Fetch alone leaves such a ref under refs/remotes and
// a later local-remote hop would not push it onward; creating the branch makes
// complete branch propagation through a chain possible.
func (o opts) createTrackingBranches(repo, remote string) error {
	branches, err := o.remoteBranches(repo, remote)
	if err != nil {
		return err
	}
	for _, branch := range branches {
		if o.localBranchExists(repo, branch) {
			continue
		}
		fmt.Printf("[track] %s: %s from %s\n", repo, branch, remote)
		if err := o.git(repo, "branch", "--track", branch, remote+"/"+branch); err != nil {
			return fmt.Errorf("create tracking branch %s: %w", branch, err)
		}
	}
	return nil
}

// forEachLocalBranch runs f for every local branch without checking any of
// them out. Branch updates use ref/index plumbing, so the working tree and
// current HEAD are left alone. Bare repositories use the same iteration.
func forEachLocalBranch(repo string, o opts, f func(branch string) bool) bool {
	branches, err := o.localBranches(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %s: list local branches: %v\n", repo, err)
		return false
	}
	ok := true
	for _, branch := range branches {
		if !f(branch) {
			ok = false
		}
	}
	return ok
}

// maybeCheckoutHead refreshes the working tree only when branch is the
// currently checked-out branch (its tip moved via plumbing).
func (o opts) maybeCheckoutHead(repo, branch string) {
	cur, err := o.currentBranch(repo)
	if err != nil || cur != branch {
		return
	}
	checkoutHead(repo, o)
}

// pullRepo pulls every local branch from every remote when it is clean.
// Updates use ref/index plumbing so commits on one branch cannot land on
// another and the working tree is untouched for non-current branches.
func pullRepo(repo string, clean bool, remotes []string, o opts) bool {
	if !clean {
		fmt.Printf("[skip] %s: uncommitted changes\n", repo)
		return true
	}
	ok := true
	for _, r := range remotes {
		// Fetch once before enumerating branches. This both refreshes existing
		// tracking refs and lets branches created upstream be materialized below.
		if err := o.git(repo, "fetch", r); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: fetch %s: %v\n", repo, r, err)
			ok = false
			continue
		}
		if err := o.createTrackingBranches(repo, r); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: track branches from %s: %v\n", repo, r, err)
			ok = false
			continue
		}
		if !forEachLocalBranch(repo, o, func(branch string) bool {
			ref := r + "/" + branch
			if !o.remoteBranchExists(repo, ref) {
				fmt.Printf("[skip] %s: pull %s: no remote branch %s\n", repo, r, branch)
				return true
			}
			fmt.Printf("[pull] %s (%s) <- %s\n", repo, branch, r)
			updated, err := o.updateBranchFromRemote(repo, branch, ref, r, true, o.rebase)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[error] %s: pull %s (%s): %v\n", repo, r, branch, err)
				return false
			}
			if updated {
				o.maybeCheckoutHead(repo, branch)
			}
			return true
		}) {
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

// restoreConflicted rolls back files containing conflict markers to the
// newest snapshot-branch version that does not have them.
func restoreConflicted(repo string, dryRun bool) error {
	return restoreconflict.Restore(repo, restoreconflict.Options{
		Git:    "git",
		Prefix: config.Load().SnapshotPrefix,
		DryRun: dryRun,
		Out:    os.Stdout,
	})
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
func localRemotes(o opts, repo string, remotes []string) []string {
	var out []string
	for _, r := range remotes {
		url, err := o.remoteURL(repo, r)
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
	return o.gitWithEnv(repo, nil, args...)
}

func (o opts) gitWithEnv(repo string, extraEnv []string, args ...string) error {
	if o.dryRun {
		fmt.Printf("  [dry-run] git -C %q %s\n", repo, strings.Join(args, " "))
		return nil
	}
	ctx, cancel := o.ctx(nil)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = o.childEnv(extraEnv)
	return o.wrapTimeout(ctx, cmd.Run())
}

// childEnv builds the environment for a child git process, applying the
// optional passthrough proxy and any extra KEY=VALUE entries.
func (o opts) childEnv(extraEnv []string) []string {
	env := os.Environ()
	if o.proxyURL != "" {
		env = appendProxiedEnv(env, env, o.proxyURL)
	}
	for _, e := range extraEnv {
		key, val, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		env = setEnv(env, key, val)
	}
	return env
}

// appendProxiedEnv injects the proxy URL into a child process environment.
// It respects an existing user NO_PROXY by appending, not replacing.
func appendProxiedEnv(baseEnv []string, existing []string, proxyURL string) []string {
	if existing == nil {
		existing = baseEnv
	}
	noProxy := ""
	for _, e := range existing {
		if strings.HasPrefix(e, "NO_PROXY=") {
			noProxy = strings.TrimPrefix(e, "NO_PROXY=")
			break
		}
	}
	out := append([]string(nil), existing...)
	out = setEnv(out, "HTTP_PROXY", proxyURL)
	out = setEnv(out, "HTTPS_PROXY", proxyURL)
	out = setEnv(out, "NO_PROXY", mergeNoProxyDefaults(noProxy))
	return out
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func mergeNoProxyDefaults(existing string) string {
	defaults := []string{"localhost", "127.0.0.1", "::1", "*.local"}
	seen := make(map[string]bool)
	for _, h := range defaults {
		seen[h] = true
	}
	var extra []string
	if existing != "" {
		for _, h := range strings.Split(existing, ",") {
			h = strings.TrimSpace(h)
			if h != "" && !seen[h] {
				seen[h] = true
				extra = append(extra, h)
			}
		}
	}
	return strings.Join(append(defaults, extra...), ",")
}

func (o opts) remotes(repo string) ([]string, error) {
	out, err := o.capture(repo, "remote")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (o opts) remoteURL(repo, remote string) (string, error) {
	out, err := o.capture(repo, "remote", "get-url", remote)
	return strings.TrimSpace(out), err
}

// remotePushURL returns the URL remote actually pushes to, which may differ
// from remoteURL if a separate push URL (or pushInsteadOf rewrite) is
// configured for it.
func (o opts) remotePushURL(repo, remote string) (string, error) {
	out, err := o.capture(repo, "remote", "get-url", "--push", remote)
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
	ctx, cancel := o.ctx(nil)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repo
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return o.wrapTimeout(ctx, cmd.Run())
}

// ghJSON runs the gh CLI in repo and decodes its JSON stdout into v.
func (o opts) ghJSON(repo string, args []string, v any) error {
	var out bytes.Buffer
	ctx, cancel := o.ctx(nil)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repo
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return o.wrapTimeout(ctx, err)
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

// isAncestorOfBranch reports whether sha (a commit at pushURL) is an ancestor
// of the local branch tip, fetching it first so it's available locally.
func (o opts) isAncestorOfBranch(repo, pushURL, sha, branch string) (bool, error) {
	if err := o.git(repo, "fetch", pushURL, sha); err != nil {
		return false, err
	}
	return o.isAncestor(repo, sha, "refs/heads/"+branch)
}

// fallbackCreatePR is invoked when a push of branch to remote fails and
// mergeMode >= mergePR. It reuses an existing open PR from this tool if one's
// tip is an ancestor of branch, fast-forwarding its branch; otherwise it
// pushes a new sequentially numbered branch and opens a PR for it.
// PRs are always created against the named remote's repository (its fetch
// URL slug) so they target the remote that is configured for this repo,
// never an inferred upstream.
func (o opts) fallbackCreatePR(repo, remote, branch string) error {
	pushURL, err := o.remotePushURL(repo, remote)
	if err != nil {
		return fmt.Errorf("remote push url: %w", err)
	}
	url, err := o.remoteURL(repo, remote)
	if err != nil {
		return fmt.Errorf("remote url: %w", err)
	}
	slug, ok := githubRepoSlug(url)
	if !ok {
		return fmt.Errorf("not a GitHub remote: %s", url)
	}
	if branch == "" || branch == "HEAD" {
		return fmt.Errorf("cannot open a PR from a detached HEAD")
	}
	base := branch

	candidates, err := o.openPRsFrom(repo, slug, base)
	if err != nil {
		return fmt.Errorf("list open PRs: %w", err)
	}
	refspecSrc := "refs/heads/" + branch
	for _, c := range candidates {
		ancestor, err := o.isAncestorOfBranch(repo, pushURL, c.HeadRefOid, branch)
		if err != nil || !ancestor {
			continue
		}
		fmt.Printf("[pr] %s: updating existing PR #%d (%s)\n", repo, c.Number, c.HeadRefName)
		if err := o.git(repo, "push", remote, refspecSrc+":refs/heads/"+c.HeadRefName); err != nil {
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
	if err := o.git(repo, "push", remote, refspecSrc+":refs/heads/"+head); err != nil {
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
	ctx, cancel := o.ctx(nil)
	defer cancel()
	var sink bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", target, "config", "receive.denyCurrentBranch", "updateInstead")
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
	return o.captureWithEnv(repo, nil, args...)
}

func (o opts) captureWithEnv(repo string, extraEnv []string, args ...string) (string, error) {
	var out bytes.Buffer
	ctx, cancel := o.ctx(nil)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if len(extraEnv) > 0 || o.proxyURL != "" {
		cmd.Env = o.childEnv(extraEnv)
	}
	if err := cmd.Run(); err != nil {
		return out.String(), o.wrapTimeout(ctx, err)
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

// skipWalkDir reports whether discovery should not descend into a directory.
func skipWalkDir(name string) bool {
	return strings.HasPrefix(name, ".") || tcc.IsProtectedDirName(name)
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
			if skipWalkDir(d.Name()) {
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
			if d.Name() == ".git" || skipWalkDir(d.Name()) {
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

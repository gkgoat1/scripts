# Plan: enhanced merge modes, checkout, mutex guarding, and PR target fix

## Goal

Extend `gitall` with four increasingly permissive merge modes, enforce
`checkout HEAD` after local repos are updated, make every repository
operation concurrency-safe with a per-repo mutex, and fix PR creation to
always target the configured remote (Git behavior) rather than an upstream.

## 1. Multi-level `-allow-merge`

Replace the current boolean `-allow-merge` flag with an integer/enum value
`0`–`3`. The existing `-allow-merge` flag stays accepted as a synonym for the
most permissive level, preserving backwards compatibility.

### Levels

| Level | Name | Meaning |
|-------|------|---------|
| `0` | `none` | Never merge (current default; same as before `-allow-merge` existed). |
| `1` | `local` | Merge only into local-remotes (filesystem paths). For example, a local mirror may receive merges to keep it in sync; network remotes are never merged into. |
| `2` | `remote` | Merge into local-remotes and network remotes (HTTP/SSH/etc.), but never create a PR. |
| `3` | `pr` | Same as `remote`, plus when a push to a GitHub remote still fails, fall back to opening/updating a PR. This is the current `-allow-merge` + `-pr` combination. |

### CLI surface

Options:

- `-allow-merge=local` or `-allow-merge=1`
- `-allow-merge=remote` or `-allow-merge=2`
- `-allow-merge=pr` or `-allow-merge=3`
- Bare `-allow-merge` (no `=`) maps to `pr` (`3`) so existing invocations keep working.

### Internal representation

```go
// MergeMode is one of mergeNone, mergeLocal, mergeRemote, mergePR.
type MergeMode int

const (
    mergeNone MergeMode = iota   // 0
    mergeLocal                   // 1
    mergeRemote                  // 2
    mergePR                      // 3
)

func ParseMergeMode(s string) (MergeMode, bool)
```

### Behavioral changes

- `syncRemote` currently checks `o.allowMerge` to decide whether to fall back
  to a merge after `--ff-only` fails. Replace that check with
  `o.mergeMode >= mergeLocal` when the remote is local, and
  `o.mergeMode >= mergeRemote` otherwise.
- PR fallback currently triggers only when `-pr` is set. Replace with
  `o.mergeMode >= mergePR`.
- `fallbackCreatePR` remains the implementation for PR creation; only the
  gating condition changes.
- Default is `mergeNone`, so without any flag `gitall` behaves exactly as it
  does today.

## 2. Checkout `HEAD` after local-repo updates

Whenever `gitall` pushes or pulls a repository that is itself a local remote
of another repo on the current run, its `HEAD` may move (because of
`receive.denyCurrentBranch updateInstead` or because the repo was pulled).
After any such update, checkout `HEAD` so the working tree matches the new
`HEAD`.

### Definition of "local repo updated"

A repo is "updated" when:

- It is the target of a push from another repo on the current run (local
  remote receiving refs), or
- It is pulled and `git pull` moves `HEAD` (fast-forward or merge).

A merge commit created by `gitall` itself already updates the current repo
naturally; this requirement mostly affects local-remotes that were not the
primary target of the command.

### Implementation

After a successful push or pull in `operate`, if `repo` is a local remote of
any currently-running repo, call:

```go
o.git(repo, "checkout", "HEAD")
```

This is a no-op when `HEAD` already matches the working tree (Git returns
success immediately). If `checkout HEAD` fails, log it but do not fail the
whole operation; a divergent working tree is a secondary issue.

### Concurrency note

Multiple parent repos may push to the same local remote concurrently. The
per-repo mutex described below serializes `checkout HEAD` (and all other
operations) per repository, so races are prevented.

## 3. Mutex-guard every repo

### Problem

`gitall` already processes each discovered repo in its own goroutine, and
local-remotes inside those goroutines recursively. Nothing currently prevents
one goroutine from pushing to a repo while another goroutine is reading or
writing the same repo as its own target or as a local remote. On shared local
mirrors this can corrupt refs, working trees, or the index.

### Approach

Introduce a repo lock registry:

```go
// repoLocks maps a resolved repository path to a mutex that serializes all
// operations on that repo for one gitall invocation.
type repoLocks struct {
    mu    sync.Mutex
    locks map[string]*sync.Mutex
}

func (r *repoLocks) Lock(path string)
func (r *repoLocks) Unlock(path string)
func (r *repoLocks) WithLock(path string, f func())
```

- Keys are resolved filesystem paths (`filepath.EvalSymlinks`).
- A lock is created lazily on first use.
- One `repoLocks` instance is created at the top of `main()` and passed down
  through `opts`.

### Where to acquire the lock

Acquire the repo's mutex for the entire duration of `operate(path, o, stack)`.
This is the simplest and safest scope: while `operate` runs, no other
goroutine can enter `operate` on the same resolved path.

```go
func operate(repo string, o opts, stack map[string]bool) bool {
    rp, err := filepath.EvalSymlinks(repo)
    if err != nil {
        rp = repo
    }
    o.locks.WithLock(rp, func() {
        os.Exit ... // not real; operate returns bool
    })
}
```

In practice, wrap the body of `operate` in the lock.

### Why not finer-grained locking?

Finer locking could allow more concurrency (e.g., one push per remote), but
`git` operations on a single repo already contend on the index, refs, and
working tree. Serializing per repo is correct, simple, and matches how Git
thinks about a repository as a single mutable resource.

### Recursive local-remote chains

The lock registry is global, so when repo A pushes to local repo B, B's
`operate` (triggered either from discovery or as a recursive child of A) will
block until A's operation on B finishes, and vice versa. This prevents
concurrent mutation of B from multiple parents.

### Deadlock avoidance

- The recursion stack prevents A→B→A reentry on the same call path.
- The mutex is acquired only at the start of `operate` and released on return;
  no nested locking on the same mutex occurs.
- Local-remote recursion always happens while the parent repo's lock is held
  (parent waits for children in `operateAll`). This is fine because children
  lock a different repo.

## 4. PR creation targets the configured remote

### Current behavior

`fallbackCreatePR` creates a PR with:

```go
o.gh(repo, "pr", "create", "-R", slug, "--head", head, "--base", base, "--fill")
```

If the repo has an `origin` that points to a fork and an `upstream` remote,
Git's `git push origin head` pushes to the fork, but `gh pr create -R owner/repo`
while checked out in the repo may infer an upstream repository instead of the
push remote.

### Desired behavior

Always create the PR against the same remote that received (or would have
received) the push. That is the remote named in the failed push, not
necessarily the default `origin` or `upstream`.

### Implementation

1. Pass the remote name (`r`) into `fallbackCreatePR`.
2. Resolve the push remote's slug using `githubRepoSlug` on the *push URL*,
   not just the fetch URL.
3. Use `gh pr create -R <push-slug>` so the PR is opened in the repository
   that matches the push destination.
4. Keep the existing fallback heuristics for open-PR reuse and branch
   numbering unchanged.

Signature change:

```go
func (o opts) fallbackCreatePR(repo, remote, base string) error
```

Call sites:

```go
if err := o.git(repo, "push", r); err != nil {
    if o.mergeMode >= mergePR {
        if prErr := o.fallbackCreatePR(repo, r, base); prErr != nil {
            ...
        }
    }
    ...
}
```

## 5. Combined example flows

### `gitall push -allow-merge=local`

1. Discover repos.
2. For each repo, serialize access.
3. On sync phase: fast-forward when possible; if ff-only fails and the remote
   is local, merge; if the remote is network, skip/abort and log.
4. Push. If push fails due to the sync, the failure is logged.
5. After the primary repo or any local remote is updated, run
   `git checkout HEAD`.

### `gitall push -allow-merge=remote`

Same as `local`, but merges are allowed for network remotes too. No PRs are
created.

### `gitall push -allow-merge=pr`

Same as `remote`, but a failed push to a GitHub remote falls back to a PR.

### `gitall push -allow-merge` (bare flag)

Same as `-allow-merge=pr`.

## 6. Testing plan

### Merge-mode parsing

- `ParseMergeMode("0") == mergeNone`
- `ParseMergeMode("none") == mergeNone`
- `ParseMergeMode("local") == mergeLocal`
- `ParseMergeMode("remote") == mergeRemote`
- `ParseMergeMode("pr") == mergePR`
- `ParseMergeMode("3") == mergePR`
- `ParseMergeMode("banana")` returns `false`

### Merge-mode behavior

Add or extend tests in `gitall/main_test.go`:

1. Fast-forward succeeds in all modes.
2. Non-fast-forward with local remote: merge at `local`+, skip at `none`.
3. Non-fast-forward with network remote: merge at `remote`+, skip at `local` and `none`.
4. Failed push with network remote: PR fallback only at `pr`.

Use a mockable `opts` or a test repo with a local remote.

### Checkout HEAD

- In a test repo that is a local remote, push a new commit via parent repo,
  then assert the child repo's working tree reflects the new `HEAD`.

### Mutex guarding

Concurrency test:

1. Create a shared local mirror that is the local remote of two independent
   repos.
2. Push both repos concurrently with `-allow-merge=local`.
3. Assert no Git error from concurrent index/ref updates.
4. Assert both parents' changes are present in the mirror.

### PR target

- Extend `gitall/pr_fallback_test.go` (or equivalent) to verify that
  `fallbackCreatePR` uses the remote's push URL slug.

## 7. Rollout steps

1. Add `MergeMode` parsing and replace `allowMerge bool` with `mergeMode MergeMode` in `opts`.
2. Update `flag` parsing so `-allow-merge` and `-allow-merge=<value>` both work.
3. Update `syncRemote` merge gating to use the new modes.
4. Update PR fallback gating to require `mergePR`.
5. Add repo lock registry and wrap `operate`.
6. Add `git checkout HEAD` after local-remote updates and after pulls.
7. Update `fallbackCreatePR` to take the remote name and use its push URL slug.
8. Update tests and docs (`docs/gitall.md`).
9. Run `go test ./gitall/...` and `make test`.

## 8. Out of scope

- Changing remote discovery or URL normalization.
- New network protocols or authentication schemes.
- Any change to the existing firewall/proxy passthrough behavior; this plan is
  entirely about `gitall` push/pull semantics.
- Backwards-incompatible removal of `-allow-merge`; it is widened, not
  removed.
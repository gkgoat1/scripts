# `clonetree`

`clonetree` mirrors a source directory tree into a destination path:

- **Git repositories** are materialized with `git clone` (local file path) or `git worktree add`
- **Non-repo files** are copied from source to destination
- **`.prtag` files** are intentionally omitted (local project markers)

## Usage

```sh
clonetree -method clone|worktree [-from prtag|any] [-n] [-v] [-force] <src> <dest>
```

| Flag | Description |
|------|-------------|
| `-method clone\|worktree` | Required. `clone` creates standalone copies; `worktree` creates linked checkouts sharing the source object store. |
| `-from prtag\|any` | Discovery mode (default `prtag`). |
| `-n` | Dry run. |
| `-v` | Verbose output. |
| `-force` | Overwrite existing non-repo files; replace paths blocking repo materialization. |

Shell wrapper: [`clonetree.sh`](../clonetree.sh).

## Discovery

- **`prtag` (default):** uses the [`workspace` scanner](../workspace/scanner.go) — projects marked with `.prtag`, nested `.git/` directories are repos.
- **`any`:** every directory containing a `.git` entry under `<src>`.

## Clone method

Each repo is cloned directly from the source checkout on disk:

```sh
git clone <evaluatedSrcRepo> <destRepo>
```

No network remotes are used. `git clone` records the source path as `origin`.

On re-run, if the destination is already a clone whose `origin` resolves to the same source repo, `clonetree` runs `git fetch origin` and checks out the source HEAD. Uncommitted changes in the destination cause failure unless `-force` is set.

## Worktree method

Each repo is linked via:

```sh
git -C <srcRepo> worktree add --detach <destRepo> <HEAD>
```

The source checkout already holds its branch, so branch names cannot be reused in a linked worktree.

Worktree destinations remain tied to the source repositories — deleting or moving the source breaks linked worktrees. Use `-method clone` for standalone copies.

## Example

Source layout:

```
~/work/
  .prtag
  Makefile
  frontend/   # git repo
  backend/    # git repo
```

```sh
clonetree -method clone ~/work ~/work-copy
```

Result:

```
~/work-copy/
  Makefile          # copied
  frontend/         # git clone of ~/work/frontend
  backend/          # git clone of ~/work/backend
```

No `.prtag` at the destination unless you add one manually.

## Re-run behavior

- **Non-repo files:** skipped if they already exist at the destination (use `-force` to overwrite).
- **Repos (clone):** updated via fetch + checkout when origin matches source.
- **Repos (worktree):** skipped if already registered as a worktree of the source.

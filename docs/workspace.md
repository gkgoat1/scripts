# `workspace` scanner library (v1)

The `workspace` package scans a root directory and returns an abstracted view of projects and repositories.

## What is scanned

- A **project** is any directory containing a `.prtag` file.
- Nested projects are included independently.
- A **repo** is any directory containing a `.git` directory (not a `.git` file).

## Public API

```go
type FS interface {
    ReadFile(path string) ([]byte, error)
    ReadDir(path string) ([]fs.DirEntry, error)
    Stat(path string) (fs.FileInfo, error)
    Join(elem ...string) string
}

type Scanner struct { /* ... */ }
func NewScanner(root string, filesystem FS) *Scanner
func NewOSScanner(root string) *Scanner

func (s *Scanner) Scan(ctx context.Context) (Snapshot, error)
func (s *Scanner) Rescan(ctx context.Context, prev Snapshot) (Snapshot, Diff, error)
```

## Data model

```go
type Snapshot struct {
    Root     string
    Projects []Project
}

type Project struct {
    Path  string
    Tag   prtag.File
    Repos []Repo
}

type Repo struct {
    Path string
}

type Diff struct {
    AddedProjects       []Project
    RemovedProjectPaths []string
    ChangedProjects     []Project
}
```

## Behavior details

- Scan output is deterministic:
  - projects sorted by path
  - repos sorted by path
- Symlink directories are not traversed.
- `Rescan` performs a full scan and computes a diff against the previous snapshot.
- Root access/traversal errors fail fast.
- `.prtag` parse/read errors are reported with project path context and return a `ScanError`.
- On per-project errors, scan still returns a best-effort partial snapshot.


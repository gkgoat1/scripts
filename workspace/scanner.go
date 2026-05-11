package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gkgoat1/scripts/prtag"
)

type FS interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]fs.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
	Join(elem ...string) string
}

type Scanner struct {
	fs   FS
	root string
}

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

type ProjectError struct {
	ProjectPath string
	Err         error
}

func (e ProjectError) Error() string {
	return fmt.Sprintf("project %q: %v", e.ProjectPath, e.Err)
}

func (e ProjectError) Unwrap() error {
	return e.Err
}

type ScanError struct {
	Errors []error
}

func (e ScanError) Error() string {
	if len(e.Errors) == 0 {
		return "scan error"
	}
	var b strings.Builder
	b.WriteString("scan encountered errors")
	for _, err := range e.Errors {
		b.WriteString("; ")
		b.WriteString(err.Error())
	}
	return b.String()
}

func NewScanner(root string, filesystem FS) *Scanner {
	return &Scanner{
		fs:   filesystem,
		root: root,
	}
}

func NewOSScanner(root string) *Scanner {
	return NewScanner(root, osFS{})
}

func (s *Scanner) Scan(ctx context.Context) (Snapshot, error) {
	rootInfo, err := s.fs.Stat(s.root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat root %q: %w", s.root, err)
	}
	if !rootInfo.IsDir() {
		return Snapshot{}, fmt.Errorf("scan root %q is not a directory", s.root)
	}

	projects, errs, err := s.scanProjects(ctx, s.root)
	if err != nil {
		return Snapshot{}, err
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Path < projects[j].Path
	})
	for i := range projects {
		sort.Slice(projects[i].Repos, func(a, b int) bool {
			return projects[i].Repos[a].Path < projects[i].Repos[b].Path
		})
	}

	snap := Snapshot{
		Root:     s.root,
		Projects: projects,
	}
	if len(errs) > 0 {
		return snap, ScanError{Errors: errs}
	}
	return snap, nil
}

func (s *Scanner) Rescan(ctx context.Context, prev Snapshot) (Snapshot, Diff, error) {
	next, err := s.Scan(ctx)
	diff := computeDiff(prev, next)
	if err != nil {
		return next, diff, err
	}
	return next, diff, nil
}

func (s *Scanner) scanProjects(ctx context.Context, dir string) ([]Project, []error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		// Fail fast for traversal/root read errors.
		return nil, nil, fmt.Errorf("read dir %q: %w", dir, err)
	}
	sortDirEntries(entries)

	var projects []Project
	var errs []error

	hasPRTag := false
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() == ".prtag" {
			hasPRTag = true
			break
		}
	}

	if hasPRTag {
		p, err := s.buildProject(dir)
		if err != nil {
			errs = append(errs, ProjectError{ProjectPath: dir, Err: err})
		} else {
			projects = append(projects, p)
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == ".git" {
			continue
		}
		if isSymlinkDirEntry(entry) {
			continue
		}

		child := s.fs.Join(dir, entry.Name())
		childProjects, childErrs, err := s.scanProjects(ctx, child)
		if err != nil {
			return nil, nil, err
		}
		projects = append(projects, childProjects...)
		errs = append(errs, childErrs...)
	}

	return projects, errs, nil
}

func (s *Scanner) buildProject(projectPath string) (Project, error) {
	prtagPath := s.fs.Join(projectPath, ".prtag")
	b, err := s.fs.ReadFile(prtagPath)
	if err != nil {
		return Project{}, fmt.Errorf("read .prtag: %w", err)
	}
	tag, err := prtag.Parse(b)
	if err != nil {
		return Project{}, fmt.Errorf("parse .prtag: %w", err)
	}

	repos, err := s.scanRepos(projectPath)
	if err != nil {
		return Project{}, err
	}
	return Project{
		Path:  projectPath,
		Tag:   tag,
		Repos: repos,
	}, nil
}

func (s *Scanner) scanRepos(projectPath string) ([]Repo, error) {
	var repos []Repo
	var walk func(string) error

	walk = func(dir string) error {
		entries, err := s.fs.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read dir %q while scanning repos: %w", dir, err)
		}
		sortDirEntries(entries)

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if isSymlinkDirEntry(entry) {
				continue
			}
			if entry.Name() == ".git" {
				repos = append(repos, Repo{Path: dir})
				continue
			}
			if err := walk(s.fs.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(projectPath); err != nil {
		return nil, err
	}
	return repos, nil
}

func computeDiff(prev, next Snapshot) Diff {
	prevByPath := make(map[string]Project, len(prev.Projects))
	nextByPath := make(map[string]Project, len(next.Projects))
	for _, p := range prev.Projects {
		prevByPath[p.Path] = p
	}
	for _, p := range next.Projects {
		nextByPath[p.Path] = p
	}

	var diff Diff

	for path, p := range nextByPath {
		old, ok := prevByPath[path]
		if !ok {
			diff.AddedProjects = append(diff.AddedProjects, p)
			continue
		}
		if !sameProject(old, p) {
			diff.ChangedProjects = append(diff.ChangedProjects, p)
		}
	}

	for path := range prevByPath {
		if _, ok := nextByPath[path]; !ok {
			diff.RemovedProjectPaths = append(diff.RemovedProjectPaths, path)
		}
	}

	sort.Slice(diff.AddedProjects, func(i, j int) bool {
		return diff.AddedProjects[i].Path < diff.AddedProjects[j].Path
	})
	sort.Slice(diff.ChangedProjects, func(i, j int) bool {
		return diff.ChangedProjects[i].Path < diff.ChangedProjects[j].Path
	})
	sort.Strings(diff.RemovedProjectPaths)

	return diff
}

func sameProject(a, b Project) bool {
	if a.Path != b.Path {
		return false
	}
	if a.Tag != b.Tag {
		return false
	}
	if len(a.Repos) != len(b.Repos) {
		return false
	}
	for i := range a.Repos {
		if a.Repos[i].Path != b.Repos[i].Path {
			return false
		}
	}
	return true
}

func sortDirEntries(entries []fs.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
}

func isSymlinkDirEntry(entry fs.DirEntry) bool {
	return entry.Type()&fs.ModeSymlink != 0
}

type osFS struct{}

func (osFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (osFS) ReadDir(path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}
func (osFS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }
func (osFS) Join(elem ...string) string            { return filepath.Join(elem...) }

var _ FS = osFS{}
var _ error = ScanError{}
var _ error = ProjectError{}

func (e ScanError) Unwrap() error {
	if len(e.Errors) == 0 {
		return nil
	}
	return errors.Join(e.Errors...)
}

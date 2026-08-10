package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Adapter reads one application's on-disk state without changing it.
type Adapter interface {
	Name() Agent
	Scan(context.Context, ScanOptions) ([]Artifact, []SourceStatus, error)
}

// ScanOptions is intentionally local-only. Home can be overridden for tests.
type ScanOptions struct {
	Home             string
	ArtifactRoots    []string
	DenyGlobs        []string
	WorkflowIndexing *bool
}

func DefaultAdapters() []Adapter {
	return []Adapter{
		ClaudeAdapter{}, CursorAdapter{}, CodexAdapter{}, PiAdapter{}, PiWorkflowsAdapter{}, AntigravityAdapter{}, FilesystemAdapter{},
	}
}

// ScanAll isolates a broken client layout so other local sources continue to
// be available.
func ScanAll(ctx context.Context, options ScanOptions) ([]Artifact, []SourceStatus) {
	var artifacts []Artifact
	var statuses []SourceStatus
	for _, adapter := range DefaultAdapters() {
		if adapter.Name() == AgentPiWorkflows && options.WorkflowIndexing != nil && !*options.WorkflowIndexing {
			continue
		}
		found, state, err := adapter.Scan(ctx, options)
		artifacts = append(artifacts, found...)
		statuses = append(statuses, state...)
		if err != nil {
			statuses = append(statuses, SourceStatus{Agent: adapter.Name(), State: "error", Detail: err.Error(), SyncedAt: time.Now().UTC()})
		}
	}
	return artifacts, statuses
}

type ClaudeAdapter struct{}

func (ClaudeAdapter) Name() Agent { return AgentClaude }

func (ClaudeAdapter) Scan(ctx context.Context, options ScanOptions) ([]Artifact, []SourceStatus, error) {
	root := filepath.Join(options.Home, ".claude")
	var artifacts []Artifact
	var statuses []SourceStatus
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, []SourceStatus{{Agent: AgentClaude, Path: root, State: "unavailable", Detail: "Claude Code profile not found", SyncedAt: time.Now().UTC()}}, nil
	}
	for _, path := range discoverFiles(filepath.Join(root, "projects"), func(path string) bool { return strings.HasSuffix(path, ".jsonl") }, options.DenyGlobs) {
		if err := ctx.Err(); err != nil {
			return artifacts, statuses, err
		}
		artifact, err := parseJSONLConversation(path, AgentClaude, "")
		if err != nil {
			statuses = append(statuses, sourceError(AgentClaude, path, err))
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	for _, spec := range []struct {
		path string
		kind Kind
	}{{filepath.Join(root, "plans"), KindPlan}, {filepath.Join(root, "tasks"), KindTask}} {
		for _, path := range discoverFiles(spec.path, func(path string) bool {
			return strings.HasSuffix(path, ".md") || (spec.kind == KindTask && strings.HasSuffix(path, ".json"))
		}, options.DenyGlobs) {
			var artifact Artifact
			var err error
			if strings.HasSuffix(path, ".json") {
				artifact, err = parseJSONArtifact(path, AgentClaude, spec.kind, "")
			} else {
				artifact, err = parseMarkdown(path, AgentClaude, spec.kind, "")
			}
			if err != nil {
				statuses = append(statuses, sourceError(AgentClaude, path, err))
				continue
			}
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, statuses, nil
}

type CursorAdapter struct{}

func (CursorAdapter) Name() Agent { return AgentCursor }

func (CursorAdapter) Scan(ctx context.Context, options ScanOptions) ([]Artifact, []SourceStatus, error) {
	var artifacts []Artifact
	var statuses []SourceStatus
	workspaceStorage := filepath.Join(options.Home, "Library", "Application Support", "Cursor", "User", "workspaceStorage")
	if _, err := os.Stat(workspaceStorage); os.IsNotExist(err) {
		statuses = append(statuses, SourceStatus{Agent: AgentCursor, Path: workspaceStorage, State: "unavailable", Detail: "Cursor workspace storage not found", SyncedAt: time.Now().UTC()})
	}
	for _, path := range discoverFiles(workspaceStorage, func(path string) bool {
		return strings.Contains(filepath.ToSlash(path), "/chatSessions/") && strings.HasSuffix(path, ".jsonl")
	}, options.DenyGlobs) {
		if err := ctx.Err(); err != nil {
			return artifacts, statuses, err
		}
		workspace := workspaceFromCursorStorage(filepath.Dir(filepath.Dir(path)))
		artifact, err := parseJSONLConversation(path, AgentCursor, workspace)
		if err != nil {
			statuses = append(statuses, sourceError(AgentCursor, path, err))
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	for _, path := range discoverFiles(filepath.Join(options.Home, ".cursor", "plans"), func(path string) bool {
		return strings.HasSuffix(path, ".md")
	}, options.DenyGlobs) {
		artifact, err := parseMarkdown(path, AgentCursor, KindPlan, "")
		if err != nil {
			statuses = append(statuses, sourceError(AgentCursor, path, err))
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, statuses, nil
}

type codexThread struct {
	ID      string
	Rollout string
	CWD     string
	Title   string
	Parent  string
	Goal    string
	Updated time.Time
}

type CodexAdapter struct{}

func (CodexAdapter) Name() Agent { return AgentCodex }

func (CodexAdapter) Scan(ctx context.Context, options ScanOptions) ([]Artifact, []SourceStatus, error) {
	root := filepath.Join(options.Home, ".codex")
	threads, threadArtifacts, metadataStatus := readCodexMetadata(root)
	var artifacts []Artifact
	var statuses []SourceStatus
	if metadataStatus.Path != "" {
		statuses = append(statuses, metadataStatus)
	}
	artifacts = append(artifacts, threadArtifacts...)
	for _, path := range discoverFiles(filepath.Join(root, "sessions"), func(path string) bool { return strings.HasSuffix(path, ".jsonl") }, options.DenyGlobs) {
		if err := ctx.Err(); err != nil {
			return artifacts, statuses, err
		}
		thread := threads[filepath.Clean(path)]
		artifact, err := parseJSONLConversation(path, AgentCodex, thread.CWD)
		if err != nil {
			statuses = append(statuses, sourceError(AgentCodex, path, err))
			continue
		}
		if thread.Title != "" {
			artifact.Title = thread.Title
		}
		if thread.ID != "" {
			artifact.ID = StableID(string(AgentCodex), filepath.Clean(path), thread.ID)
			artifact.SourceRecord = thread.ID
		}
		artifact.ParentID = thread.Parent
		if !thread.Updated.IsZero() {
			artifact.UpdatedAt = thread.Updated
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, statuses, nil
}

func readCodexMetadata(root string) (map[string]codexThread, []Artifact, SourceStatus) {
	path := filepath.Join(root, "state_5.sqlite")
	threads := map[string]codexThread{}
	if _, err := os.Stat(path); err != nil {
		return threads, nil, SourceStatus{Agent: AgentCodex, Path: path, State: "unavailable", Detail: "Codex state database not found", SyncedAt: time.Now().UTC()}
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return threads, nil, sourceError(AgentCodex, path, err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, rollout_path, cwd, title, updated_at_ms FROM threads`)
	if err != nil {
		return threads, nil, sourceError(AgentCodex, path, err)
	}
	defer rows.Close()
	byID := map[string]codexThread{}
	for rows.Next() {
		var id, rollout, cwd, title string
		var updated int64
		if err := rows.Scan(&id, &rollout, &cwd, &title, &updated); err != nil {
			continue
		}
		entry := codexThread{ID: id, Rollout: filepath.Clean(rollout), CWD: CanonicalWorkspace(cwd), Title: title, Updated: time.UnixMilli(updated).UTC()}
		byID[id] = entry
		threads[filepath.Clean(rollout)] = entry
	}
	if rows.Err() != nil {
		return threads, nil, sourceError(AgentCodex, path, rows.Err())
	}
	// Spawn edges retain multi-agent lineage without inventing conclusions.
	edges, err := db.Query(`SELECT parent_thread_id, child_thread_id FROM thread_spawn_edges`)
	if err == nil {
		for edges.Next() {
			var parent, child string
			if edges.Scan(&parent, &child) == nil {
				item := byID[child]
				item.Parent = parent
				byID[child] = item
			}
		}
		edges.Close()
	}
	for id, item := range byID {
		if parent, ok := byID[item.Parent]; ok {
			item.Parent = StableID(string(AgentCodex), parent.Rollout, parent.ID)
		}
		byID[id] = item
	}
	for rollout, item := range threads {
		item.Parent = byID[item.ID].Parent
		threads[rollout] = item
	}
	artifacts := readCodexGoals(root, byID)
	return threads, artifacts, SourceStatus{Agent: AgentCodex, Path: path, State: "indexed", Detail: "Codex thread metadata, goals, and spawn lineage read", SyncedAt: time.Now().UTC()}
}

// readCodexGoals deliberately uses the documented local metadata store. A
// missing or newer schema simply leaves goals unavailable; it never probes
// arbitrary Codex caches for replacement data.
func readCodexGoals(root string, threads map[string]codexThread) []Artifact {
	path := filepath.Join(root, "goals_1.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT thread_id, objective, status, created_at_ms, updated_at_ms FROM thread_goals`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var artifacts []Artifact
	for rows.Next() {
		var threadID, objective, status string
		var created, updated int64
		if rows.Scan(&threadID, &objective, &status, &created, &updated) != nil || strings.TrimSpace(objective) == "" {
			continue
		}
		thread := threads[threadID]
		artifacts = append(artifacts, Artifact{
			ID:           StableID("codex-goal", path, threadID, objective),
			Agent:        AgentCodex,
			Kind:         KindTask,
			Workspace:    thread.CWD,
			Title:        firstLine(objective, 120),
			SourcePath:   path,
			SourceRecord: threadID,
			ParentID:     thread.Parent,
			CreatedAt:    time.UnixMilli(created).UTC(),
			UpdatedAt:    time.UnixMilli(updated).UTC(),
			Text:         fmt.Sprintf("Objective: %s\nStatus: %s\nThread: %s", objective, status, threadID),
		})
	}
	return artifacts
}

type PiAdapter struct{}

func (PiAdapter) Name() Agent { return AgentPi }

func (PiAdapter) Scan(ctx context.Context, options ScanOptions) ([]Artifact, []SourceStatus, error) {
	root := filepath.Join(options.Home, ".pi", "agent", "sessions")
	var artifacts []Artifact
	var statuses []SourceStatus
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, []SourceStatus{{Agent: AgentPi, Path: root, State: "unavailable", Detail: "Pi session store not found", SyncedAt: time.Now().UTC()}}, nil
	}
	for _, path := range discoverFiles(root, func(path string) bool { return strings.HasSuffix(path, ".jsonl") }, options.DenyGlobs) {
		if err := ctx.Err(); err != nil {
			return artifacts, statuses, err
		}
		artifact, err := parseJSONLConversation(path, AgentPi, "")
		if err != nil {
			statuses = append(statuses, sourceError(AgentPi, path, err))
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, statuses, nil
}

// AntigravityAdapter is deliberately version-gated. Its persistent chat
// layout must be captured as a fixture before it is parsed; Chromium cache
// directories are never considered a source of truth.
type AntigravityAdapter struct{}

const antigravityFixtureVersion = "2.4.3"

func (AntigravityAdapter) Name() Agent { return AgentAntigravity }

func (AntigravityAdapter) Scan(_ context.Context, options ScanOptions) ([]Artifact, []SourceStatus, error) {
	if runtime.GOOS != "darwin" {
		return nil, []SourceStatus{{Agent: AgentAntigravity, State: "unsupported", Detail: "Antigravity adapter is macOS-first", SyncedAt: time.Now().UTC()}}, nil
	}
	app := "/Applications/Antigravity.app/Contents/Info.plist"
	profile := filepath.Join(options.Home, "Library", "Application Support", "Antigravity")
	if _, err := os.Stat(profile); err != nil {
		return nil, []SourceStatus{{Agent: AgentAntigravity, Path: profile, State: "unavailable", Detail: "Antigravity profile not found", SyncedAt: time.Now().UTC()}}, nil
	}
	version := plistVersion(app)
	if version != antigravityFixtureVersion {
		return nil, []SourceStatus{{Agent: AgentAntigravity, Path: profile, State: "unsupported", Detail: fmt.Sprintf("supported fixture layout is %s; found %q", antigravityFixtureVersion, version), SyncedAt: time.Now().UTC()}}, nil
	}
	// The supported persistent layout is intentionally narrow. It is absent on
	// this machine until a chat fixture is captured, which is reported rather
	// than guessed from Cache, blob_storage, or session cache files.
	known := filepath.Join(profile, "User", "workspaceStorage")
	if _, err := os.Stat(known); err != nil {
		return nil, []SourceStatus{{Agent: AgentAntigravity, Path: profile, State: "unsupported", Detail: "2.4.3 profile found but no verified persistent chat layout is present", SyncedAt: time.Now().UTC()}}, nil
	}
	var artifacts []Artifact
	for _, path := range discoverFiles(known, func(path string) bool {
		return strings.Contains(filepath.ToSlash(path), "/chatSessions/") && strings.HasSuffix(path, ".jsonl")
	}, options.DenyGlobs) {
		artifact, err := parseJSONLConversation(path, AgentAntigravity, workspaceFromCursorStorage(filepath.Dir(filepath.Dir(path))))
		if err == nil {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, []SourceStatus{{Agent: AgentAntigravity, Path: known, State: "indexed", Detail: "version-gated Antigravity chat layout", SyncedAt: time.Now().UTC(), ArtifactN: len(artifacts)}}, nil
}

// FilesystemAdapter indexes only explicit roots. It does not crawl arbitrary
// projects or the home directory.
type FilesystemAdapter struct{}

func (FilesystemAdapter) Name() Agent { return AgentFilesystem }

func (FilesystemAdapter) Scan(ctx context.Context, options ScanOptions) ([]Artifact, []SourceStatus, error) {
	var artifacts []Artifact
	var statuses []SourceStatus
	for _, root := range options.ArtifactRoots {
		workspace := CanonicalWorkspace(root)
		if _, err := os.Stat(workspace); os.IsNotExist(err) {
			statuses = append(statuses, SourceStatus{Agent: AgentFilesystem, Path: workspace, State: "unavailable", Detail: "configured artifact root not found", SyncedAt: time.Now().UTC()})
			continue
		}
		for _, path := range discoverFiles(workspace, func(path string) bool {
			lower := strings.ToLower(path)
			return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".plan")
		}, options.DenyGlobs) {
			if err := ctx.Err(); err != nil {
				return artifacts, statuses, err
			}
			artifact, err := parseMarkdown(path, AgentFilesystem, KindArtifact, workspace)
			if err != nil {
				statuses = append(statuses, sourceError(AgentFilesystem, path, err))
				continue
			}
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, statuses, nil
}

func discoverFiles(root string, include func(string) bool, denyGlobs []string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := entry.Name()
		if entry.IsDir() {
			switch base {
			case ".git", "node_modules", "Cache", "cache", "GPUCache", "Code Cache", "blob_storage":
				return filepath.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(path)
		if strings.Contains(lower, "credential") || strings.Contains(lower, "auth") || strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".db") {
			return nil
		}
		for _, glob := range denyGlobs {
			if ok, _ := filepath.Match(glob, path); ok {
				return nil
			}
		}
		if include(path) {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

func sourceError(agent Agent, path string, err error) SourceStatus {
	return SourceStatus{Agent: agent, Path: path, State: "error", Detail: err.Error(), SyncedAt: time.Now().UTC()}
}

func plistVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := string(data)
	needle := "CFBundleShortVersionString"
	i := strings.Index(text, needle)
	if i < 0 {
		return ""
	}
	rest := text[i+len(needle):]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start < 0 || end < 0 || end <= start+8 {
		return ""
	}
	return strings.TrimSpace(rest[start+8 : end])
}

// Keep encoding/json imported in this file: several external workspace.json
// variants use a "workspace" field and future adapters share this helper.
var _ = json.Valid

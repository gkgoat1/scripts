// Package core contains AgentWeave's local, provenance-preserving index.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Agent identifies the application that produced an artifact.
type Agent string

const (
	AgentClaude      Agent = "claude"
	AgentCursor      Agent = "cursor"
	AgentCodex       Agent = "codex"
	AgentPi          Agent = "pi"
	AgentAntigravity Agent = "antigravity"
	AgentFilesystem  Agent = "filesystem"
)

// Kind describes the role of an indexed artifact.
type Kind string

const (
	KindConversation Kind = "conversation"
	KindPlan         Kind = "plan"
	KindTask         Kind = "task"
	KindArtifact     Kind = "artifact"
)

// Artifact is the loss-minimized representation of one local source item.
// Text is retained locally so reads and citations remain stable after a source
// application is closed or its internal layout changes.
type Artifact struct {
	ID           string    `json:"id"`
	Agent        Agent     `json:"agent"`
	Kind         Kind      `json:"kind"`
	Workspace    string    `json:"workspace,omitempty"`
	Title        string    `json:"title"`
	SourcePath   string    `json:"source_path"`
	SourceRecord string    `json:"source_record,omitempty"`
	ParentID     string    `json:"parent_id,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
	Text         string    `json:"-"`
}

// Normalize makes fields safe for use as a local index key.
func (a *Artifact) Normalize() error {
	if a.Agent == "" || a.Kind == "" || a.SourcePath == "" {
		return fmt.Errorf("artifact needs agent, kind, and source path")
	}
	a.SourcePath = filepath.Clean(a.SourcePath)
	a.Workspace = CanonicalWorkspace(a.Workspace)
	a.Title = strings.TrimSpace(a.Title)
	if a.Title == "" {
		a.Title = filepath.Base(a.SourcePath)
	}
	a.Text = strings.TrimSpace(a.Text)
	if a.Text == "" {
		return fmt.Errorf("artifact %s contains no indexable text", a.SourcePath)
	}
	if a.ID == "" {
		a.ID = StableID(string(a.Agent), string(a.Kind), a.SourcePath, a.SourceRecord)
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = a.UpdatedAt
	}
	return nil
}

// StableID creates a non-secret, deterministic identifier from local metadata.
func StableID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// CanonicalWorkspace intentionally uses the exact local workspace path. It
// does not conflate worktrees which happen to have the same git remote.
func CanonicalWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	if strings.HasPrefix(workspace, "file://") {
		workspace = strings.TrimPrefix(workspace, "file://")
		workspace = strings.ReplaceAll(workspace, "%20", " ")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return filepath.Clean(workspace)
	}
	return filepath.Clean(abs)
}

// SourceStatus records whether an adapter was able to inspect one source.
type SourceStatus struct {
	Agent       Agent     `json:"agent"`
	Path        string    `json:"path"`
	State       string    `json:"state"` // indexed, unchanged, unavailable, unsupported, error
	Detail      string    `json:"detail,omitempty"`
	SyncedAt    time.Time `json:"synced_at"`
	ArtifactN   int       `json:"artifact_count,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

// Chunk is a bounded, citeable unit of an artifact.
type Chunk struct {
	Ref        string `json:"ref"`
	ArtifactID string `json:"artifact_id"`
	Ordinal    int    `json:"ordinal"`
	Body       string `json:"body"`
}

// SearchRequest limits searches to one explicit registered workspace unless
// IncludeGlobal is deliberately set by a caller.
type SearchRequest struct {
	Workspace     string  `json:"workspace"`
	Query         string  `json:"query"`
	Agents        []Agent `json:"agents,omitempty"`
	Kinds         []Kind  `json:"kinds,omitempty"`
	Limit         int     `json:"limit,omitempty"`
	IncludeGlobal bool    `json:"include_global,omitempty"`
}

// SearchResult carries evidence, never an unsupported conclusion.
type SearchResult struct {
	Ref        string    `json:"ref"`
	ArtifactID string    `json:"artifact_id"`
	Agent      Agent     `json:"agent"`
	Kind       Kind      `json:"kind"`
	Workspace  string    `json:"workspace,omitempty"`
	Title      string    `json:"title"`
	SourcePath string    `json:"source_path"`
	UpdatedAt  time.Time `json:"updated_at"`
	Excerpt    string    `json:"excerpt"`
	Score      float64   `json:"score"`
}

// SynthesisRequest asks for an evidence dossier or one client-side sampled
// answer. Sampling is handled by the MCP facade, never by the core index.
type SynthesisRequest struct {
	Workspace  string   `json:"workspace"`
	Question   string   `json:"question"`
	Selection  []string `json:"selection,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	Generation string   `json:"generation"` // evidence or sample
}

// EvidenceDossier is safe to return without any model invocation.
type EvidenceDossier struct {
	Question        string         `json:"question"`
	Detail          string         `json:"detail,omitempty"`
	Evidence        []SearchResult `json:"evidence"`
	PotentialIssues []string       `json:"potential_issues,omitempty"`
	MissingSupport  []string       `json:"missing_support,omitempty"`
	Prompt          string         `json:"prompt"`
}

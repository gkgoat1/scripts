package core

// This adapter intentionally has a very small trust boundary: only the user
// saved directory and manifest-proven project buckets are read.  A project key
// is not reversible attribution; the manifest is the attribution grant.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PiWorkflowsAdapter struct{}

func (PiWorkflowsAdapter) Name() Agent { return AgentPiWorkflows }

type workflowManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Workspace     string `json:"workspace"`
	ProjectKey    string `json:"project_key"`
	SavedDir      string `json:"saved_dir"`
	UpdatedAt     string `json:"updated_at"`
}
type savedWorkflow struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Script      string          `json:"script"`
	Parameters  json.RawMessage `json:"parameters"`
	SavedAt     string          `json:"savedAt"`
}
type persistedRun struct {
	RunID        string     `json:"runId"`
	WorkflowName string     `json:"workflowName"`
	SessionID    string     `json:"sessionId"`
	Status       string     `json:"status"`
	Phases       []string   `json:"phases"`
	CurrentPhase string     `json:"currentPhase"`
	Args         any        `json:"args"`
	Logs         []any      `json:"logs"`
	Agents       []runAgent `json:"agents"`
	Result       any        `json:"result"`
	Journal      []any      `json:"journal"`
	StartedAt    string     `json:"startedAt"`
	UpdatedAt    string     `json:"updatedAt"`
	CompletedAt  string     `json:"completedAt"`
}
type runAgent struct {
	ID            any          `json:"id"`
	Label         string       `json:"label"`
	Prompt        string       `json:"prompt"`
	Status        string       `json:"status"`
	Phase         string       `json:"phase"`
	ResultPreview string       `json:"resultPreview"`
	Result        any          `json:"result"`
	Error         string       `json:"error"`
	History       []runHistory `json:"history"`
}
type runHistory struct {
	Role      string `json:"role"`
	Kind      string `json:"kind"`
	ToolName  string `json:"toolName"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	IsError   bool   `json:"isError"`
}

func (PiWorkflowsAdapter) Scan(ctx context.Context, o ScanOptions) ([]Artifact, []SourceStatus, error) {
	root := filepath.Join(o.Home, ".pi", "workflows")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, []SourceStatus{{Agent: AgentPiWorkflows, Path: root, State: "unavailable", Detail: "Pi workflows not found", SyncedAt: time.Now().UTC()}}, nil
	}
	arts, sts, err := scanSaved(ctx, filepath.Join(root, "saved"), "", "user", "", 3, o.DenyGlobs)
	if err != nil {
		return arts, sts, err
	}
	projects := filepath.Join(root, "projects")
	es, err := safeDirEntries(projects)
	if os.IsNotExist(err) {
		return arts, sts, nil
	}
	if err != nil {
		return arts, sts, err
	}
	for _, e := range es {
		if ctx.Err() != nil {
			return arts, sts, ctx.Err()
		}
		if !e.IsDir() || unsafeName(e.Name()) {
			continue
		}
		pr := filepath.Join(projects, e.Name())
		mpath := filepath.Join(pr, "agentweave-manifest.json")
		m, err := validateWorkflowManifest(mpath, pr, e.Name())
		if err != nil {
			sts = append(sts, SourceStatus{Agent: AgentPiWorkflows, Path: mpath, State: "metadata_incomplete", Detail: err.Error(), SyncedAt: time.Now().UTC()})
			continue
		}
		a, s, err := scanSaved(ctx, m.SavedDir, m.Workspace, "project", m.ProjectKey, 1, o.DenyGlobs)
		arts, sts = append(arts, a...), append(sts, s...)
		if err != nil {
			return arts, sts, err
		}
		runsDir := filepath.Join(pr, "runs")
		if err := containedExistingDir(pr, runsDir); err != nil && !os.IsNotExist(err) {
			sts = append(sts, sourceError(AgentPiWorkflows, runsDir, err))
			continue
		}
		a, s, err = scanRuns(ctx, runsDir, m, o.DenyGlobs)
		arts, sts = append(arts, a...), append(sts, s...)
		if err != nil {
			return arts, sts, err
		}
	}
	return arts, sts, nil
}
func validateWorkflowManifest(path, root, key string) (workflowManifest, error) {
	var m workflowManifest
	b, err := readRetry(path)
	if err != nil {
		return m, fmt.Errorf("invalid workflow manifest: %w", err)
	}
	if json.Unmarshal(b, &m) != nil {
		return m, fmt.Errorf("invalid workflow manifest")
	}
	m.Workspace = CanonicalWorkspace(m.Workspace)
	if m.SchemaVersion != 1 || m.Workspace == "" || m.ProjectKey != key || m.ProjectKey != workflowProjectKey(m.Workspace) {
		return m, fmt.Errorf("invalid workflow manifest attribution")
	}
	want := filepath.Join(root, "saved")
	if m.SavedDir == "" {
		m.SavedDir = want
	}
	if CanonicalWorkspace(m.SavedDir) != CanonicalWorkspace(want) {
		return m, fmt.Errorf("manifest saved_dir is not expected project saved directory")
	}
	if err := containedPath(root, m.SavedDir); err != nil {
		return m, err
	}
	return m, nil
}
func workflowProjectKey(workspace string) string {
	h := sha256.Sum256([]byte(workspace))
	slug := strings.ToLower(filepath.Base(workspace))
	var b strings.Builder
	dash := false
	for _, r := range slug {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = s[:48]
	}
	if s == "" {
		s = "project"
	}
	return s + "-" + hex.EncodeToString(h[:])[:12]
}
func containedPath(root, path string) error {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(r, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workflow path escapes project root")
	}
	return nil
}
func containedExistingDir(root, path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	if err := containedPath(root, path); err != nil {
		return err
	}
	i, err := os.Stat(path)
	if err != nil || !i.IsDir() {
		return fmt.Errorf("workflow path is not a directory")
	}
	return nil
}
func safeDirEntries(dir string) ([]os.DirEntry, error) {
	i, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	if i.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing symlink directory %s", dir)
	}
	es, e := os.ReadDir(dir)
	sort.Slice(es, func(i, j int) bool { return es[i].Name() < es[j].Name() })
	return es, e
}
func readRetry(p string) ([]byte, error) {
	b, e := os.ReadFile(p)
	if e == nil {
		return b, nil
	}
	time.Sleep(10 * time.Millisecond)
	return os.ReadFile(p)
}
func unsafeName(n string) bool {
	return strings.HasPrefix(n, ".") || strings.Contains(n, "..") || strings.Contains(n, ".tmp") || strings.Contains(n, ".bak") || strings.Contains(n, ".lock")
}
func scanSaved(ctx context.Context, dir, workspace, scope, key string, precedence int, deny []string) ([]Artifact, []SourceStatus, error) {
	es, err := safeDirEntries(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Artifact
	var sts []SourceStatus
	for _, e := range es {
		if ctx.Err() != nil {
			return out, sts, ctx.Err()
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || unsafeName(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		i, err := os.Lstat(p)
		if err != nil || i.Mode()&os.ModeSymlink != 0 || denied(p, deny) {
			continue
		}
		b, err := readRetry(p)
		var w savedWorkflow
		if err == nil {
			err = json.Unmarshal(b, &w)
		}
		if err != nil || !validWorkflowName(w.Name) || strings.TrimSuffix(e.Name(), ".json") != w.Name {
			if err == nil {
				err = fmt.Errorf("invalid workflow definition")
			}
			sts = append(sts, staleError(p, err))
			continue
		}
		saved := parseWorkflowTime(w.SavedAt)
		if saved.IsZero() {
			saved = i.ModTime().UTC()
		}
		params := "[]"
		if json.Valid(w.Parameters) {
			params = string(w.Parameters)
		}
		id := StableID("pi-workflow-definition", p, scope, workspace, key, w.Name)
		out = append(out, Artifact{ID: id, Agent: AgentPiWorkflows, Kind: KindWorkflow, Workspace: workspace, Title: "Workflow: " + w.Name, SourcePath: p, SourceRecord: w.Name, CreatedAt: saved, UpdatedAt: saved, Text: renderDefinition(w, scope), WorkflowDefinition: &WorkflowDefinition{Name: w.Name, Scope: scope, ProjectKey: key, Precedence: precedence, SavedAt: saved, ScriptHash: sha256hex(w.Script), ParametersJSON: params, MetaParseStatus: "unavailable", Visibility: scope}})
	}
	return out, sts, nil
}
func validWorkflowName(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && len(s) <= 128 && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00")
}
func sha256hex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func renderDefinition(w savedWorkflow, scope string) string {
	return fmt.Sprintf("Workflow: %s\nScope: %s\nDescription: %s\nParameters: %s\nScript:\n%s", w.Name, scope, w.Description, w.Parameters, w.Script)
}
func scanRuns(ctx context.Context, dir string, m workflowManifest, deny []string) ([]Artifact, []SourceStatus, error) {
	es, err := safeDirEntries(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Artifact
	var sts []SourceStatus
	for _, e := range es {
		if ctx.Err() != nil {
			return out, sts, ctx.Err()
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || unsafeName(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		i, _ := os.Lstat(p)
		if i == nil || i.Mode()&os.ModeSymlink != 0 || denied(p, deny) {
			continue
		}
		b, err := readRetry(p)
		var r persistedRun
		if err == nil {
			err = json.Unmarshal(b, &r)
		}
		if err != nil || r.RunID == "" || strings.TrimSuffix(e.Name(), ".json") != r.RunID || !validWorkflowName(r.RunID) {
			if err == nil {
				err = fmt.Errorf("invalid workflow run")
			}
			sts = append(sts, staleError(p, err))
			continue
		}
		created := parseWorkflowTime(r.StartedAt)
		updated := parseWorkflowTime(r.UpdatedAt)
		if updated.IsZero() {
			updated = created
		}
		parent := StableID("pi-workflow-definition", filepath.Join(m.SavedDir, r.WorkflowName+".json"), "project", m.Workspace, m.ProjectKey, r.WorkflowName)
		records := runRecords(r)
		for n, record := range records {
			stamp := record.at
			if stamp.IsZero() {
				stamp = updated
			}
			out = append(out, Artifact{ID: StableID("pi-workflow-run", p, r.RunID, fmt.Sprint(n)), Agent: AgentPiWorkflows, Kind: KindWorkflowRun, Workspace: m.Workspace, Title: "Workflow run: " + r.WorkflowName, SourcePath: p, SourceRecord: fmt.Sprintf("%s:%d", r.RunID, n), ParentID: parent, CreatedAt: created, UpdatedAt: stamp, Text: record.text, WorkflowRun: &WorkflowRun{RunID: r.RunID, WorkflowName: r.WorkflowName, SessionID: r.SessionID, Status: r.Status, RecordType: record.typ, ParentDefinitionID: parent, StartedAt: created, FinishedAt: parseWorkflowTime(r.CompletedAt), SourceFingerprint: sha256hex(string(b))}})
		}
	}
	return out, sts, nil
}

type runRecord struct {
	text, typ string
	at        time.Time
}

func runRecords(r persistedRun) []runRecord {
	out := []runRecord{{fmt.Sprintf("Workflow run: %s\nRun ID: %s\nStatus: %s\nArguments: %s", r.WorkflowName, r.RunID, r.Status, jsonText(r.Args)), "state", parseWorkflowTime(r.StartedAt)}}
	for _, a := range r.Agents {
		if a.Prompt != "" {
			out = append(out, runRecord{"Agent prompt: " + a.Prompt, "prompt", parseWorkflowTime(r.StartedAt)})
		}
		for _, h := range a.History {
			if h.Text != "" {
				out = append(out, runRecord{fmt.Sprintf("%s: %s", h.Role, h.Text), "history", parseWorkflowTime(h.Timestamp)})
			}
		}
		if a.ResultPreview != "" {
			out = append(out, runRecord{"Agent result: " + a.ResultPreview, "result", parseWorkflowTime(r.UpdatedAt)})
		}
	}
	for _, v := range r.Logs {
		out = append(out, runRecord{"Log: " + jsonText(v), "log", parseWorkflowTime(r.UpdatedAt)})
	}
	if r.Result != nil {
		out = append(out, runRecord{"Result: " + jsonText(r.Result), "result", parseWorkflowTime(r.CompletedAt)})
	}
	return out
}
func jsonText(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	if len(b) > 16000 {
		b = append(b[:16000], []byte("…")...)
	}
	return string(b)
}
func parseWorkflowTime(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func staleError(p string, e error) SourceStatus {
	return SourceStatus{Agent: AgentPiWorkflows, Path: p, State: "stale", Detail: e.Error(), SyncedAt: time.Now().UTC()}
}
func denied(path string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
	}
	return false
}

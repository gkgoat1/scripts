package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeWorkflowJSON(t *testing.T, p string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestWorkflowProjectKeyMatchesPiContract(t *testing.T) {
	for _, p := range []string{"/tmp/Hello (world)@x", "/tmp/---", "/tmp/abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"} {
		h := sha256.Sum256([]byte(p))
		want := workflowProjectKey(p)
		if want[len(want)-12:] != hex.EncodeToString(h[:])[:12] {
			t.Fatalf("bad hash %q", want)
		}
	}
	h := sha256sum("/tmp/Hello (world)@x")
	if got := workflowProjectKey("/tmp/Hello (world)@x"); got != "hello-world-x-"+hex.EncodeToString(h[:])[:12] {
		t.Fatalf("got %s", got)
	}
}
func sha256sum(s string) [32]byte { return sha256.Sum256([]byte(s)) }
func TestPiWorkflowsManifestDefinitionsAndRunRecords(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "work tree")
	if err := os.MkdirAll(ws, 0700); err != nil {
		t.Fatal(err)
	}
	key := workflowProjectKey(ws)
	root := filepath.Join(home, ".pi", "workflows", "projects", key)
	writeWorkflowJSON(t, filepath.Join(root, "agentweave-manifest.json"), map[string]any{"schema_version": 1, "workspace": ws, "project_key": key, "saved_dir": filepath.Join(root, "saved")})
	writeWorkflowJSON(t, filepath.Join(root, "saved", "demo.json"), map[string]any{"name": "demo", "description": "find needles", "script": "workflow('x')", "savedAt": "2025-01-01T00:00:00Z"})
	writeWorkflowJSON(t, filepath.Join(root, "runs", "r1.json"), map[string]any{"runId": "r1", "workflowName": "demo", "status": "completed", "phases": []string{}, "agents": []any{map[string]any{"prompt": "look here", "history": []any{map[string]any{"role": "assistant", "text": "found needle", "timestamp": "2025-01-01T00:00:01Z"}}}}, "logs": []string{"done"}, "startedAt": "2025-01-01T00:00:00Z", "updatedAt": "2025-01-01T00:00:02Z"})
	writeWorkflowJSON(t, filepath.Join(root, "runs", "r1.json.bak"), map[string]any{"runId": "bad"})
	writeWorkflowJSON(t, filepath.Join(root, "runs", "junk.tmp.json"), map[string]any{"runId": "junk"})
	a, s, e := PiWorkflowsAdapter{}.Scan(context.Background(), ScanOptions{Home: home})
	if e != nil {
		t.Fatal(e)
	}
	if len(s) != 0 {
		t.Fatalf("statuses %#v", s)
	}
	if len(a) != 5 {
		t.Fatalf("artifacts %d: %#v", len(a), a)
	}
	var defs, runs int
	for _, x := range a {
		if x.Kind == KindWorkflow {
			defs++
			if x.WorkflowDefinition.ScriptHash != sha256hex("workflow('x')") {
				t.Fatal("script hash")
			}
		} else if x.Kind == KindWorkflowRun {
			runs++
			if x.WorkflowRun.SourceFingerprint == "" || x.ParentID == "" {
				t.Fatal("missing run provenance")
			}
		}
	}
	if defs != 1 || runs != 4 {
		t.Fatalf("defs/runs %d/%d", defs, runs)
	}
}
func TestWorkflowManifestRejectsEscapedSaved(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "work")
	_ = os.Mkdir(ws, 0700)
	key := workflowProjectKey(ws)
	project := filepath.Join(root, "project")
	_ = os.MkdirAll(project, 0700)
	writeWorkflowJSON(t, filepath.Join(project, "agentweave-manifest.json"), map[string]any{"schema_version": 1, "workspace": ws, "project_key": key, "saved_dir": filepath.Join(root, "outside")})
	if _, err := validateWorkflowManifest(filepath.Join(project, "agentweave-manifest.json"), project, key); err == nil {
		t.Fatal("accepted escaped path")
	}
}

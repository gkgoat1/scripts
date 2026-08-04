package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAntigravityFixtureDeclaresOnlyPersistentStore(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "antigravity", antigravityFixtureVersion, "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		FixtureVersion string   `json:"fixture_version"`
		Persistent     string   `json:"persistent_store"`
		NeverRead      []string `json:"never_read"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.FixtureVersion != antigravityFixtureVersion || fixture.Persistent == "" || len(fixture.NeverRead) == 0 {
		t.Fatalf("invalid Antigravity fixture: %#v", fixture)
	}
}

func TestPiAdapterExtractsWorkspaceAndMessages(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ".pi", "agent", "sessions", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := "{\"type\":\"session\",\"cwd\":\"/work/demo\",\"id\":\"session-1\"}\n" +
		"{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"Make a plan\"}}\n" +
		"{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"Use tests.\"}]}}\n" +
		"{not finished\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts, statuses, err := PiAdapter{}.Scan(context.Background(), ScanOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 0 || len(artifacts) != 1 {
		t.Fatalf("Scan() artifacts=%#v statuses=%#v", artifacts, statuses)
	}
	if artifacts[0].Workspace != "/work/demo" || artifacts[0].Title != "Make a plan" {
		t.Fatalf("artifact = %#v", artifacts[0])
	}
}

func TestCodexMetadataPreservesSpawnLineageAndGoals(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := sql.Open("sqlite", filepath.Join(root, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	parentRollout := filepath.Join(root, "sessions", "parent.jsonl")
	childRollout := filepath.Join(root, "sessions", "child.jsonl")
	_, err = state.Exec(`CREATE TABLE threads (id TEXT, rollout_path TEXT, cwd TEXT, title TEXT, updated_at_ms INTEGER);
CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT);
INSERT INTO threads VALUES ('parent', ?, '/work/demo', 'Parent', 1000), ('child', ?, '/work/demo', 'Child', 2000);
INSERT INTO thread_spawn_edges VALUES ('parent', 'child');`, parentRollout, childRollout)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	goals, err := sql.Open("sqlite", filepath.Join(root, "goals_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = goals.Exec(`CREATE TABLE thread_goals (thread_id TEXT, objective TEXT, status TEXT, created_at_ms INTEGER, updated_at_ms INTEGER);
INSERT INTO thread_goals VALUES ('child', 'Inspect fixtures', 'in_progress', 1000, 2000);`)
	if err != nil {
		goals.Close()
		t.Fatal(err)
	}
	if err := goals.Close(); err != nil {
		t.Fatal(err)
	}
	threads, artifacts, status := readCodexMetadata(root)
	if status.State != "indexed" {
		t.Fatalf("metadata status=%#v", status)
	}
	wantParent := StableID(string(AgentCodex), parentRollout, "parent")
	if threads[childRollout].Parent != wantParent || len(artifacts) != 1 || artifacts[0].ParentID != wantParent {
		t.Fatalf("threads=%#v artifacts=%#v", threads, artifacts)
	}
}

func TestAntigravityAdapterDoesNotReadCacheAsHistory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Library", "Application Support", "Antigravity", "Cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifacts, statuses, err := AntigravityAdapter{}.Scan(context.Background(), ScanOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 || len(statuses) != 1 || statuses[0].State != "unsupported" {
		t.Fatalf("unexpected Antigravity scan artifacts=%#v statuses=%#v", artifacts, statuses)
	}
}

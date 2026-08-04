package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIndexSyncSearchAndPrivacyBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(source, []byte("# Migration\nUse a WAL transaction for the index."), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := Open(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	artifact := Artifact{Agent: AgentClaude, Kind: KindPlan, Workspace: filepath.Join(dir, "project-a"), Title: "Migration", SourcePath: source, Text: "Use a WAL transaction for the index."}
	if _, err := index.Sync(context.Background(), []Artifact{artifact}, nil); err != nil {
		t.Fatal(err)
	}
	results, err := index.Search(context.Background(), SearchRequest{Workspace: filepath.Join(dir, "project-a"), Query: "WAL transaction"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Agent != AgentClaude {
		t.Fatalf("Search() = %#v, want one Claude result", results)
	}
	results, err = index.Search(context.Background(), SearchRequest{Workspace: filepath.Join(dir, "project-b"), Query: "WAL transaction"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("cross-workspace result leaked: %#v", results)
	}
}

func TestIndexSkipsUnchangedAndReplacesChangedSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "chat.jsonl")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(source, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	index, err := Open(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	newArtifact := func(text string) Artifact {
		return Artifact{Agent: AgentPi, Kind: KindConversation, Workspace: dir, SourcePath: source, SourceRecord: "one", UpdatedAt: time.Now().UTC(), Text: text}
	}
	statuses, err := index.Sync(context.Background(), []Artifact{newArtifact("first")}, nil)
	if err != nil || statuses[len(statuses)-1].State != "indexed" {
		t.Fatalf("initial Sync() statuses=%#v err=%v", statuses, err)
	}
	statuses, err = index.Sync(context.Background(), []Artifact{newArtifact("first")}, nil)
	if err != nil || statuses[len(statuses)-1].State != "unchanged" {
		t.Fatalf("unchanged Sync() statuses=%#v err=%v", statuses, err)
	}
	write("second")
	statuses, err = index.Sync(context.Background(), []Artifact{newArtifact("second")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	results, err := index.Search(context.Background(), SearchRequest{Workspace: dir, Query: "second"})
	if err != nil || len(results) != 1 {
		t.Fatalf("changed source search results=%#v err=%v statuses=%#v", results, err, statuses)
	}
	old, err := index.Search(context.Background(), SearchRequest{Workspace: dir, Query: "first"})
	if err != nil || len(old) != 0 {
		t.Fatalf("old chunks remain=%#v err=%v", old, err)
	}
}

func TestDossierRejectsInvalidGeneration(t *testing.T) {
	t.Parallel()
	index, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	if _, err := index.Dossier(context.Background(), SynthesisRequest{Workspace: "/tmp/x", Question: "what", Generation: "auto"}); err == nil {
		t.Fatal("Dossier accepted implicit generation")
	}
}

func TestDossierReportsMissingEvidenceDeterministically(t *testing.T) {
	t.Parallel()
	index, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	dossier, err := index.Dossier(context.Background(), SynthesisRequest{Workspace: "/tmp/empty", Question: "What was decided?", Generation: "evidence"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dossier.Evidence) != 0 || len(dossier.MissingSupport) != 1 {
		t.Fatalf("dossier=%#v", dossier)
	}
}

func TestStatusRetainsUnsupportedOrUnavailableDiagnostics(t *testing.T) {
	t.Parallel()
	index, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	missing := filepath.Join(t.TempDir(), "missing-layout")
	if _, err := index.Sync(context.Background(), nil, []SourceStatus{{Agent: AgentAntigravity, Path: missing, State: "unsupported", Detail: "fixture not available", SyncedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	statuses, err := index.Status(context.Background())
	if err != nil || len(statuses) != 1 || statuses[0].State != "unsupported" {
		t.Fatalf("Status() = %#v, %v", statuses, err)
	}
}

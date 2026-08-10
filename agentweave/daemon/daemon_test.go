package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gkgoat1/scripts/agentweave/core"
)

func TestDaemonRoundTripEnforcesScopedRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	if err := os.WriteFile(source, []byte("cross-agent evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := core.Open(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	workspace := filepath.Join(dir, "one")
	if _, err := index.Sync(context.Background(), []core.Artifact{{Agent: core.AgentCodex, Kind: core.KindConversation, Workspace: workspace, SourcePath: source, Text: "cross-agent evidence"}}, nil); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "agentweave-daemon-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "agentweave.sock")
	server, err := Listen(&Service{Index: index}, socket)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("sandbox does not permit Unix-domain socket binds")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Serve(ctx) }()
	client := Client{Socket: socket}
	var results []core.SearchResult
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		results, err = client.Search(context.Background(), core.SearchRequest{Workspace: workspace, Query: "evidence"})
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || len(results) != 1 {
		t.Fatalf("Search() results=%#v err=%v", results, err)
	}
	if _, err := client.Read(context.Background(), filepath.Join(dir, "two"), []string{results[0].Ref}, 100); err != nil {
		// A scoped read may simply return no rows; it must never expose content.
		t.Fatal(err)
	}
	read, err := client.Read(context.Background(), filepath.Join(dir, "two"), []string{results[0].Ref}, 100)
	if err != nil || len(read) != 0 {
		t.Fatalf("cross-workspace Read() = %#v, %v", read, err)
	}
}

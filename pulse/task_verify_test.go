package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/pulse/tasks"
)

func TestV2TaskRequiresAnchor(t *testing.T) {
	v := taskCommitmentVerifier{Anchor: fakeAnchorReader{err: errFake("missing")}}
	task := tasks.Task{ID: "x", Domain: tasks.RapidService, Command: "true"}
	if ok, _, err := v.Verify(task); ok || err == nil {
		t.Fatalf("ok=%v err=%v, want anchor failure", ok, err)
	}
}

func TestV2TaskDomainCannotReuseProof(t *testing.T) {
	scheduled := tasks.Task{ID: "x", Domain: tasks.Scheduled, Command: "true", Interval: 1}
	tree, err := commitment.Build([]commitment.Leaf{scheduled.CommitLeaf()})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := tree.ProofFor(scheduled.CommitLeaf().Key())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tasks.proof")
	if err := os.WriteFile(path, commitment.EncodeProofFile(tree.Root(), map[string]commitment.Proof{scheduled.CommitLeaf().Key(): proof}), 0o644); err != nil {
		t.Fatal(err)
	}
	v := taskCommitmentVerifier{Anchor: fakeAnchorReader{root: tree.Root()}, ProofFile: path}
	rapid := tasks.Task{ID: "x", Domain: tasks.RapidService, Command: "true"}
	ok, reason, err := v.Verify(rapid)
	if err != nil || ok || !bytes.Contains([]byte(reason), []byte("no commitment")) {
		t.Fatalf("ok=%v reason=%q err=%v", ok, reason, err)
	}
}

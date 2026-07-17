package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
	interposeconfig "github.com/gkgoat1/scripts/interpose/config"
)

type fakeAnchorReader struct {
	root [32]byte
	err  error
}

func (f fakeAnchorReader) ReadRoot() ([32]byte, error) { return f.root, f.err }

func TestVerifyPolicyNotInstalledUsesLiveConfig(t *testing.T) {
	cfg := interposeconfig.Config{ExtraProtectedPaths: []string{"/Users/g/secret"}}
	extra, trusted, msg := verifyPolicy(cfg, fakeAnchorReader{err: anchor.ErrAnchorNotInstalled}, commitment.ProofFile{}, nil)

	if trusted {
		t.Error("trusted = true, want false when the anchor was never installed")
	}
	if msg != "" {
		t.Errorf("logMsg = %q, want empty (not-installed is not a warning condition)", msg)
	}
	if len(extra) != 1 || extra[0] != "/Users/g/secret" {
		t.Errorf("extra = %v, want the live config's ExtraProtectedPaths unchanged", extra)
	}
}

func TestVerifyPolicyAnchorBrokenFallsBackToDefaults(t *testing.T) {
	cfg := interposeconfig.Config{ExtraProtectedPaths: []string{"/Users/g/secret"}}
	extra, trusted, msg := verifyPolicy(cfg, fakeAnchorReader{err: errors.New("plutil: command not found")}, commitment.ProofFile{}, nil)

	if trusted {
		t.Error("trusted = true, want false for a broken anchor")
	}
	if extra != nil {
		t.Errorf("extra = %v, want nil (built-in defaults only) when the anchor is unreadable", extra)
	}
	if !strings.Contains(msg, "anchor unreadable") {
		t.Errorf("logMsg = %q", msg)
	}
}

func TestVerifyPolicyMissingProofFallsBackToDefaults(t *testing.T) {
	cfg := interposeconfig.Config{ExtraProtectedPaths: []string{"/Users/g/secret"}}
	extra, trusted, msg := verifyPolicy(cfg, fakeAnchorReader{root: [32]byte{1}}, commitment.ProofFile{}, errors.New("open: no such file"))

	if trusted {
		t.Error("trusted = true, want false when there is no proof sidecar")
	}
	if extra != nil {
		t.Errorf("extra = %v, want nil (built-in defaults only)", extra)
	}
	if !strings.Contains(msg, "no policy commitment proof") {
		t.Errorf("logMsg = %q", msg)
	}
}

func TestVerifyPolicyVerifiedOK(t *testing.T) {
	cfg := interposeconfig.Config{ExtraProtectedPaths: []string{"/Users/g/secret"}}
	tree, err := commitment.Build([]commitment.Leaf{cfg.CommitLeaf()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(cfg.CommitLeaf().Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}
	pf := commitment.ProofFile{Entries: map[string]commitment.Proof{interposeconfig.PolicyLeafID: proof}}

	extra, trusted, msg := verifyPolicy(cfg, fakeAnchorReader{root: tree.Root()}, pf, nil)

	if !trusted {
		t.Errorf("trusted = false, want true; msg = %q", msg)
	}
	if len(extra) != 1 || extra[0] != "/Users/g/secret" {
		t.Errorf("extra = %v, want the verified config's ExtraProtectedPaths", extra)
	}
	if msg != "" {
		t.Errorf("logMsg = %q, want empty on success", msg)
	}
}

func TestVerifyPolicyTamperedConfigFallsBackToDefaults(t *testing.T) {
	original := interposeconfig.Config{ExtraProtectedPaths: []string{"/Users/g/secret"}}
	tree, err := commitment.Build([]commitment.Leaf{original.CommitLeaf()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(original.CommitLeaf().Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}
	pf := commitment.ProofFile{Entries: map[string]commitment.Proof{interposeconfig.PolicyLeafID: proof}}

	// Attacker narrowed ExtraProtectedPaths after the commitment was made.
	tampered := interposeconfig.Config{}
	extra, trusted, msg := verifyPolicy(tampered, fakeAnchorReader{root: tree.Root()}, pf, nil)

	if trusted {
		t.Error("trusted = true, want false for a tampered (narrowed) policy config")
	}
	if extra != nil {
		t.Errorf("extra = %v, want nil (built-in defaults only) for a tampered config", extra)
	}
	if !strings.Contains(msg, "verification failed") {
		t.Errorf("logMsg = %q", msg)
	}
}

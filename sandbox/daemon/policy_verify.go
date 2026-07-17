package main

import (
	"errors"
	"fmt"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
	interposeconfig "github.com/gkgoat1/scripts/interpose/config"
)

// verifyPolicy checks cfg's policy leaf against proof's sidecar entry and
// reader's anchor root, and returns the ExtraProtectedPaths to actually
// enforce plus whether they came from a verified commitment.
//
// sandboxd is short-lived and per-invocation (sandbox/run.sh starts a fresh
// daemon each time), so this is checked once at startup, not per-request —
// the opposite granularity choice from pulse's per-tick check, since the two
// tools have different lifetimes.
//
// Failure mode: fall back to the built-in default roots only (extra=nil),
// never fail-closed on the whole daemon. Config only ever broadens
// protection beyond tcc.DefaultProtectedRoots(), so this fallback can never
// be more permissive than an operator with zero custom config already gets;
// full fail-closed (deny every OPEN) would make the entire sandboxed
// process unusable over one stale policy entry. See docs/agentcommit.md.
func verifyPolicy(cfg interposeconfig.Config, reader anchor.AnchorReader, proof commitment.ProofFile, proofErr error) (extra []string, trusted bool, logMsg string) {
	root, err := reader.ReadRoot()
	switch {
	case errors.Is(err, anchor.ErrAnchorNotInstalled):
		// Not adopted yet: behave exactly as before commitment verification
		// existed — unverified, live config.
		return cfg.ExtraProtectedPaths, false, ""
	case err != nil:
		return nil, false, fmt.Sprintf("anchor unreadable (%v); enforcing built-in defaults only", err)
	}

	if proofErr != nil {
		return nil, false, fmt.Sprintf("no policy commitment proof (%v); enforcing built-in defaults only", proofErr)
	}

	leaf := cfg.CommitLeaf()
	p, ok := proof.Entries[interposeconfig.PolicyLeafID]
	if !ok || !commitment.VerifyProof(leaf, p, root) {
		return nil, false, "policy commitment verification failed; enforcing built-in defaults only"
	}
	return cfg.ExtraProtectedPaths, true, ""
}

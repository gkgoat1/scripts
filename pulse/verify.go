package main

import (
	"fmt"
	"os"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
)

// realCommitmentVerifier implements CommitmentVerifier against the real
// anchor LaunchAgent and a jobs.proof sidecar file. See docs/agentcommit.md
// for the full design.
type realCommitmentVerifier struct {
	Anchor    anchor.AnchorReader
	ProofFile string // path to the jobs.proof sidecar, re-read fresh every call
}

// Verify implements CommitmentVerifier.
//
//   - anchor.ErrAnchorNotInstalled: verification was never adopted on this
//     machine — ok=true (off), not a tampering signal.
//   - any other anchor error (plutil missing, corrupted plist): ok=false,
//     err set — every job fails, loudly, until the anchor is readable again.
//     A broken-but-present anchor must never be treated as "not adopted",
//     or an attacker could blind verification by breaking the read path.
//   - anchor readable but no proof entry for this job (a tampered existing
//     job, or a brand-new job added straight to the plaintext config
//     without being recommitted): ok=false.
//   - VerifyProof false: ok=false.
func (v realCommitmentVerifier) Verify(job pconfig.Job) (ok bool, reason string, err error) {
	root, aerr := v.Anchor.ReadRoot()
	if aerr != nil {
		if aerr == anchor.ErrAnchorNotInstalled {
			return true, "", nil
		}
		return false, "", fmt.Errorf("read anchor: %w", aerr)
	}

	data, rerr := os.ReadFile(v.ProofFile)
	if rerr != nil {
		return false, fmt.Sprintf("no commitment proof file (%v)", rerr), nil
	}
	pf, derr := commitment.DecodeProofFile(data)
	if derr != nil {
		return false, fmt.Sprintf("invalid commitment proof file (%v)", derr), nil
	}
	proof, found := pf.Entries[job.Name]
	if !found {
		return false, "no commitment proof entry for this job", nil
	}
	if !commitment.VerifyProof(job.CommitLeaf(), proof, root) {
		return false, "command does not match its committed leaf", nil
	}
	return true, "", nil
}

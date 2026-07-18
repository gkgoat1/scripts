package main

import (
	"fmt"
	"os"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
	"github.com/gkgoat1/scripts/pulse/tasks"
)

// taskCommitmentVerifier verifies v2 task leaves. Unlike legacy jobs, v2
// domains never run when the anchor was not installed: their execution
// authority depends on the committed domain as well as their command.
type taskCommitmentVerifier struct {
	Anchor    anchor.AnchorReader
	ProofFile string
}

func (v taskCommitmentVerifier) Verify(task tasks.Task) (bool, string, error) {
	root, err := v.Anchor.ReadRoot()
	if err != nil {
		if err == anchor.ErrAnchorNotInstalled {
			return false, "v2 tasks require an installed commitment anchor", nil
		}
		return false, "", fmt.Errorf("read anchor: %w", err)
	}
	data, err := os.ReadFile(v.ProofFile)
	if err != nil {
		return false, fmt.Sprintf("no commitment proof file (%v)", err), nil
	}
	pf, err := commitment.DecodeProofFile(data)
	if err != nil {
		return false, fmt.Sprintf("invalid commitment proof file (%v)", err), nil
	}
	leaf := task.CommitLeaf()
	proof, ok := pf.Entries[leaf.Key()]
	if !ok {
		return false, "no commitment proof entry for this task domain", nil
	}
	if !commitment.VerifyProof(leaf, proof, root) {
		return false, "task definition does not match its committed leaf", nil
	}
	return true, "", nil
}

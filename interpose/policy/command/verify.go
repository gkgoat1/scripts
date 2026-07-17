package command

import (
	"errors"
	"fmt"
	"os"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
)

// Verify loads the current allowlist and verifies it against the installed
// agentcommit anchor. A missing anchor is intentionally not trusted: these
// wrappers protect process-control commands, so an uncommitted policy always
// requires the local PIN override.
func Verify(path string, reader anchor.AnchorReader) (Allowlist, error) {
	list, err := Load(path)
	if err != nil {
		return nil, err
	}
	root, err := reader.ReadRoot()
	if err != nil {
		if errors.Is(err, anchor.ErrAnchorNotInstalled) {
			return list, fmt.Errorf("command allowlist is not committed")
		}
		return list, fmt.Errorf("read command-policy commitment: %w", err)
	}
	proofData, err := os.ReadFile(path + ".proof")
	if err != nil {
		return list, fmt.Errorf("read command-policy proof: %w", err)
	}
	proofFile, err := commitment.DecodeProofFile(proofData)
	if err != nil {
		return list, fmt.Errorf("decode command-policy proof: %w", err)
	}
	proof, ok := proofFile.Entries[PolicyLeafID]
	if !ok || !commitment.VerifyProof(list.CommitLeaf(), proof, root) {
		return list, fmt.Errorf("command allowlist commitment verification failed")
	}
	return list, nil
}

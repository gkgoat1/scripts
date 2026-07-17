package config

import "github.com/gkgoat1/scripts/commitment"

// PolicyLeafID is the fixed sentinel ID for the single, coarse policy leaf:
// this config is committed as one leaf covering the whole deny-list, not one
// leaf per entry, so partial tampering can't hide behind an untouched proof
// for the rest of the list.
const PolicyLeafID = "policy"

// CommitLeaf returns the commitment.Leaf covering this config's
// security-policy-relevant fields (ExtraProtectedPaths, DisableSnapshot).
// SnapshotPrefix/ToolTimeout are excluded: neither weakens an access-control
// decision if changed, so committing them would only create false-positive
// "tampered" noise on cosmetic edits. Both agentcommit (which commits it)
// and sandboxd (which verifies against it) call this same method.
func (c Config) CommitLeaf() commitment.Leaf {
	return commitment.Leaf{
		Tool: "interpose",
		ID:   PolicyLeafID,
		Kind: commitment.KindPolicy,
		Payload: commitment.EncodeKV(map[string]string{
			"extra-protected-paths": string(commitment.EncodeStringSet(c.ExtraProtectedPaths)),
			"disable-snapshot":      string(commitment.EncodeStringSet(c.DisableSnapshot)),
		}),
	}
}

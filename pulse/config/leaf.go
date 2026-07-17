package config

import "github.com/gkgoat1/scripts/commitment"

// CommitLeaf returns the commitment.Leaf covering exactly this job's
// spawnable command. Both agentcommit (which commits it) and pulse (which
// verifies against it) call this same method, so the two can never silently
// drift into computing different leaf shapes for the same job.
func (j Job) CommitLeaf() commitment.Leaf {
	return commitment.Leaf{
		Tool:    "pulse",
		ID:      j.Name,
		Kind:    commitment.KindCommand,
		Payload: commitment.EncodeKV(map[string]string{"command": j.Command}),
	}
}

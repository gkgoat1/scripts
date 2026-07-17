package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/gkgoat1/scripts/commitment"
)

// runAnchor validates root and logs it. It is the trivial ProgramArguments
// target for the agentcommit-anchor LaunchAgent: it does no ongoing work and
// always returns nil (main exits 0 immediately after calling it). Since
// installer/launchagent.sh's KeepAlive is {SuccessfulExit:false}, launchd
// only restarts on a crash — a clean exit means it fires once at load and
// stays dead, giving the anchor zero resident footprint while still being a
// real, monitored LaunchAgent write target.
func runAnchor(root string, out io.Writer) error {
	parsed, err := commitment.ParseRootHex(root)
	if err != nil {
		return fmt.Errorf("-root: %w", err)
	}
	fmt.Fprintf(out, "[anchor] agentcommit-anchor: root=%s\n", commitment.RootHex(parsed))
	return nil
}

func anchorFlags(args []string) (root string, err error) {
	fs := flag.NewFlagSet("anchor", flag.ContinueOnError)
	r := fs.String("root", "", "hex-encoded Merkle root to anchor (required)")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if *r == "" {
		return "", fmt.Errorf("-root is required")
	}
	return *r, nil
}

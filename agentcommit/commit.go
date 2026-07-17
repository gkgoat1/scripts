package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/gkgoat1/scripts/commitment"
	interposeconfig "github.com/gkgoat1/scripts/interpose/config"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
)

// runCommit gathers leaves from every registered tool (pulse's jobs, if its
// config exists; interpose's policy, always — an absent/default config is
// itself a fact worth committing), builds the tree, atomically writes each
// registrant's proof sidecar, and returns the root. Human-facing progress
// goes to errw; out receives ONLY the hex root, so this is $(...)-capturable
// by install-agentcommit-anchor.sh.
func runCommit(pulseConfigPath string, out, errw io.Writer) ([32]byte, error) {
	var pulseLeaves []commitment.Leaf

	jobs, jerr := pconfig.LoadConfig(pulseConfigPath)
	switch {
	case jerr == nil:
		for _, j := range jobs {
			pulseLeaves = append(pulseLeaves, j.CommitLeaf())
		}
		fmt.Fprintf(errw, "[commit] pulse: %d job(s) found in %s\n", len(pulseLeaves), pulseConfigPath)
	case errors.Is(jerr, os.ErrNotExist):
		fmt.Fprintf(errw, "[skip] pulse: no config found at %s\n", pulseConfigPath)
	default:
		return [32]byte{}, fmt.Errorf("pulse: %w", jerr)
	}

	policy := interposeconfig.Load().CommitLeaf()

	all := make([]commitment.Leaf, 0, len(pulseLeaves)+1)
	all = append(all, pulseLeaves...)
	all = append(all, policy)

	tree, err := commitment.Build(all)
	if err != nil {
		return [32]byte{}, err
	}
	root := tree.Root()

	if len(pulseLeaves) > 0 {
		if err := writeProofSidecar(tree, root, pulseLeaves, pulseConfigPath+".proof"); err != nil {
			return [32]byte{}, fmt.Errorf("pulse: %w", err)
		}
	}
	if err := writeProofSidecar(tree, root, []commitment.Leaf{policy}, interposeconfig.DefaultConfigPath()+".proof"); err != nil {
		return [32]byte{}, fmt.Errorf("interpose: %w", err)
	}
	fmt.Fprintf(errw, "[commit] interpose: policy committed\n")

	fmt.Fprintln(out, commitment.RootHex(root))
	return root, nil
}

// writeProofSidecar writes a ProofFile covering exactly leaves (keyed by
// Leaf.ID) to path, atomically.
func writeProofSidecar(tree *commitment.Tree, root [32]byte, leaves []commitment.Leaf, path string) error {
	entries := make(map[string]commitment.Proof, len(leaves))
	for _, l := range leaves {
		proof, err := tree.ProofFor(l.Key())
		if err != nil {
			return err
		}
		entries[l.ID] = proof
	}
	data := commitment.EncodeProofFile(root, entries)
	return atomicWriteFile(path, data)
}

// atomicWriteFile writes data to path via a temp file + rename, so a reader
// never observes a partially-written proof sidecar. Mirrors
// extclean/jsonapply.go's atomicWriteFile.
func atomicWriteFile(path string, data []byte) error {
	tmp := path + ".agentcommit.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

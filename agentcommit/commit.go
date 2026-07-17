package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gkgoat1/scripts/commitment"
	interposeconfig "github.com/gkgoat1/scripts/interpose/config"
	interposecommand "github.com/gkgoat1/scripts/interpose/policy/command"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
	sandboxconfig "github.com/gkgoat1/scripts/sandbox/config"
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
	commandPolicy, err := interposecommand.Load(interposecommand.DefaultConfigPath())
	if err != nil {
		return [32]byte{}, fmt.Errorf("interpose command policy: %w", err)
	}
	commandLeaf := commandPolicy.CommitLeaf()

	var sandboxLeaves []commitment.Leaf
	sandboxCfg, serr := sandboxconfig.Load(sandboxconfig.DefaultConfigPath())
	switch {
	case serr == nil:
		sandboxLeaves = append(sandboxLeaves, sandboxCfg.CommitLeaf())
		fmt.Fprintf(errw, "[commit] sandbox: policy committed\n")
	case errors.Is(serr, os.ErrNotExist):
		fmt.Fprintf(errw, "[skip] sandbox: no config found at %s\n", sandboxconfig.DefaultConfigPath())
	default:
		return [32]byte{}, fmt.Errorf("sandbox: %w", serr)
	}

	all := make([]commitment.Leaf, 0, len(pulseLeaves)+2+len(sandboxLeaves))
	all = append(all, pulseLeaves...)
	all = append(all, policy, commandLeaf)
	all = append(all, sandboxLeaves...)

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
	if err := writeProofSidecar(tree, root, []commitment.Leaf{commandLeaf}, interposecommand.DefaultConfigPath()+".proof"); err != nil {
		return [32]byte{}, fmt.Errorf("interpose command policy: %w", err)
	}
	fmt.Fprintf(errw, "[commit] interpose command policy: committed\n")
	if len(sandboxLeaves) > 0 {
		if err := writeProofSidecar(tree, root, sandboxLeaves, sandboxconfig.DefaultConfigPath()+".proof"); err != nil {
			return [32]byte{}, fmt.Errorf("sandbox: %w", err)
		}
	}

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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", path, err)
	}
	tmp := path + ".agentcommit.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

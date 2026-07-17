package commitment

import (
	"strings"
	"testing"
)

func TestCommitFileEncodeDecodeRoundTrip(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("b", "echo b"), cmdLeaf("c", "echo c")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := tree.Root()

	entries := map[string]Proof{}
	for _, l := range leaves {
		p, err := tree.ProofFor(l.Key())
		if err != nil {
			t.Fatalf("ProofFor: %v", err)
		}
		entries[l.ID] = p
	}

	data := EncodeProofFile(root, entries)
	pf, err := DecodeProofFile(data)
	if err != nil {
		t.Fatalf("DecodeProofFile: %v", err)
	}
	if pf.Root != RootHex(root) {
		t.Errorf("Root = %q, want %q", pf.Root, RootHex(root))
	}
	if len(pf.Entries) != len(entries) {
		t.Fatalf("Entries = %d, want %d", len(pf.Entries), len(entries))
	}
	for _, l := range leaves {
		proof, ok := pf.Entries[l.ID]
		if !ok {
			t.Fatalf("missing entry for %q", l.ID)
		}
		if !VerifyProof(l, proof, root) {
			t.Errorf("round-tripped proof for %q does not verify", l.ID)
		}
	}
}

func TestCommitFileRoundTripPreservesCarryStep(t *testing.T) {
	// Odd leaf count guarantees at least one Carry step somewhere in the tree.
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("b", "echo b"), cmdLeaf("c", "echo c")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := tree.Root()

	entries := map[string]Proof{}
	sawCarry := false
	for _, l := range leaves {
		p, err := tree.ProofFor(l.Key())
		if err != nil {
			t.Fatalf("ProofFor: %v", err)
		}
		for _, s := range p.Steps {
			if s.Carry {
				sawCarry = true
			}
		}
		entries[l.ID] = p
	}
	if !sawCarry {
		t.Fatal("test setup: expected at least one Carry step across a 3-leaf tree")
	}

	data := EncodeProofFile(root, entries)
	pf, err := DecodeProofFile(data)
	if err != nil {
		t.Fatalf("DecodeProofFile: %v", err)
	}
	for _, l := range leaves {
		if !VerifyProof(l, pf.Entries[l.ID], root) {
			t.Errorf("round-tripped proof for %q (with a Carry step) does not verify", l.ID)
		}
	}
}

func TestDecodeProofFileInvalidJSON(t *testing.T) {
	if _, err := DecodeProofFile([]byte("not json")); err == nil {
		t.Error("DecodeProofFile: want error for invalid JSON")
	}
}

func TestRootHexParseRoundTrip(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := tree.Root()
	hexStr := RootHex(root)
	parsed, err := ParseRootHex(hexStr)
	if err != nil {
		t.Fatalf("ParseRootHex: %v", err)
	}
	if parsed != root {
		t.Errorf("ParseRootHex round-trip mismatch")
	}
}

func TestParseRootHexInvalid(t *testing.T) {
	cases := []string{"", "not-hex", "deadbeef", strings.Repeat("ab", 31), strings.Repeat("ab", 33)}
	for _, c := range cases {
		if _, err := ParseRootHex(c); err == nil {
			t.Errorf("ParseRootHex(%q): want error", c)
		}
	}
}

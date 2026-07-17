package commitment

import (
	"fmt"
	"math/rand"
	"testing"
)

func cmdLeaf(id, command string) Leaf {
	return Leaf{Tool: "pulse", ID: id, Kind: KindCommand, Payload: EncodeKV(map[string]string{"command": command})}
}

func policyLeaf(id string, paths []string) Leaf {
	return Leaf{Tool: "interpose", ID: id, Kind: KindPolicy, Payload: EncodeKV(map[string]string{
		"extra-protected-paths": string(EncodeStringSet(paths)),
	})}
}

func TestBuildTreeDeterministicRegardlessOfInputOrder(t *testing.T) {
	leaves := []Leaf{
		cmdLeaf("a", "echo a"),
		cmdLeaf("b", "echo b"),
		cmdLeaf("c", "echo c"),
		policyLeaf("policy", []string{"/x", "/y"}),
	}

	tree1, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root1 := tree1.Root()

	shuffled := append([]Leaf(nil), leaves...)
	rand.New(rand.NewSource(1)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	tree2, err := Build(shuffled)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tree2.Root() != root1 {
		t.Errorf("Root differs by input order: %x vs %x", tree2.Root(), root1)
	}
}

func TestVerifyUntamperedLeafSucceeds(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("b", "echo b"), cmdLeaf("c", "echo c")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(leaves[1].Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}
	if !VerifyProof(leaves[1], proof, tree.Root()) {
		t.Error("VerifyProof: want true for untampered leaf")
	}
}

func TestVerifyTamperedCommandFailsEvenWithValidProofCopiedVerbatim(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("b", "echo b"), cmdLeaf("c", "echo c")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(leaves[1].Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}

	tampered := cmdLeaf("b", "echo MALICIOUS")
	if VerifyProof(tampered, proof, tree.Root()) {
		t.Error("VerifyProof: want false for tampered Command with a stale (valid-for-the-old-leaf) proof")
	}
}

func TestVerifyTamperedProofSiblingFails(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("b", "echo b"), cmdLeaf("c", "echo c")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(leaves[1].Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}
	if len(proof.Steps) == 0 {
		t.Fatal("expected at least one proof step")
	}
	proof.Steps[0].Hash[0] ^= 0xFF // flip a bit

	if VerifyProof(leaves[1], proof, tree.Root()) {
		t.Error("VerifyProof: want false when a proof sibling is tampered")
	}
}

func TestVerifyTamperedRootFails(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("b", "echo b")}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	proof, err := tree.ProofFor(leaves[0].Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}
	badRoot := tree.Root()
	badRoot[0] ^= 0xFF
	if VerifyProof(leaves[0], proof, badRoot) {
		t.Error("VerifyProof: want false against a wrong root")
	}
}

func TestOddLeafCountPromotionRoundTrips(t *testing.T) {
	for _, n := range []int{1, 3, 5, 7} {
		n := n
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var leaves []Leaf
			for i := 0; i < n; i++ {
				leaves = append(leaves, cmdLeaf(fmt.Sprintf("job-%d", i), fmt.Sprintf("echo %d", i)))
			}
			tree, err := Build(leaves)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			for _, l := range leaves {
				proof, err := tree.ProofFor(l.Key())
				if err != nil {
					t.Fatalf("ProofFor(%s): %v", l.Key(), err)
				}
				if !VerifyProof(l, proof, tree.Root()) {
					t.Errorf("VerifyProof(%s): want true", l.Key())
				}
			}
		})
	}
}

func TestSingleLeafTreeRootIsLeafHash(t *testing.T) {
	leaf := cmdLeaf("solo", "echo solo")
	tree, err := Build([]Leaf{leaf})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := tree.Root(), leafHash(leaf); got != want {
		t.Errorf("Root() = %x, want leafHash = %x", got, want)
	}
	proof, err := tree.ProofFor(leaf.Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}
	if len(proof.Steps) != 0 {
		t.Errorf("single-leaf proof should have zero steps, got %d", len(proof.Steps))
	}
	if !VerifyProof(leaf, proof, tree.Root()) {
		t.Error("VerifyProof: want true")
	}
}

func TestBuildDuplicateKeyRejected(t *testing.T) {
	leaves := []Leaf{cmdLeaf("a", "echo a"), cmdLeaf("a", "echo a again")}
	if _, err := Build(leaves); err == nil {
		t.Error("Build: want error for duplicate leaf key, got nil")
	}
}

func TestBuildZeroLeavesRejected(t *testing.T) {
	if _, err := Build(nil); err == nil {
		t.Error("Build: want error for zero leaves, got nil")
	}
}

// TestProof_SingleLeafVerifiesAgainstFullRoot_MixedKinds is the direct
// security-property test: a tree spans two leaf kinds (pulse commands +
// one interpose policy leaf); a single leaf's proof, extracted and used in
// isolation (simulating "lives in the unprotected sidecar file"), verifies
// against the full-set root with no access to the other leaves' content.
func TestProof_SingleLeafVerifiesAgainstFullRoot_MixedKinds(t *testing.T) {
	leaves := []Leaf{
		cmdLeaf("llmtrim-restart", "killall -9 llmtrim; llmtrim stop && llmtrim start"),
		cmdLeaf("other-job", "echo other"),
		cmdLeaf("third-job", "echo third"),
		policyLeaf("policy", []string{"/Users/g/secret", "/Users/g/also-secret"}),
	}
	tree, err := Build(leaves)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	root := tree.Root()

	policy := leaves[3]
	proof, err := tree.ProofFor(policy.Key())
	if err != nil {
		t.Fatalf("ProofFor: %v", err)
	}

	// Verify using ONLY policy + proof + root — no access to tree or the
	// other three leaves.
	if !VerifyProof(policy, proof, root) {
		t.Error("VerifyProof: want true for the policy leaf against the full-set root")
	}

	// Tampering the policy leaf's content (dropping a protected path)
	// invalidates it against the same, unmoved root.
	tamperedPolicy := policyLeaf("policy", []string{"/Users/g/secret"})
	if VerifyProof(tamperedPolicy, proof, root) {
		t.Error("VerifyProof: want false when the policy leaf's protected-path set has been narrowed")
	}
}

func TestBuildTreeIndependentAcrossToolsAndKinds(t *testing.T) {
	// Same Tool+ID but different Kind must not collide.
	a := Leaf{Tool: "x", ID: "y", Kind: KindCommand, Payload: []byte("p1")}
	b := Leaf{Tool: "x", ID: "y", Kind: KindPolicy, Payload: []byte("p2")}
	tree, err := Build([]Leaf{a, b})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(tree.levels[0]) != 2 {
		t.Fatalf("expected 2 distinct leaves, got %d", len(tree.levels[0]))
	}
}

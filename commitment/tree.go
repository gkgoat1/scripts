// Package commitment builds a Merkle tree over "things that matter" across
// this repo's config-driven tools (spawnable commands, security policy) and
// verifies individual leaves against a previously-committed root. See
// docs/agentcommit.md for the full design and threat model.
package commitment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

// LeafKind identifies what shape of "thing that matters" a Leaf commits to.
type LeafKind string

const (
	// KindCommand is a spawnable shell command (e.g. one pulse job).
	KindCommand LeafKind = "command"
	// KindPolicy is a security-policy config snapshot (e.g. the interpose/
	// sandbox shared protected-path deny-list), committed as one coarse
	// leaf per tool rather than one leaf per entry, so partial tampering
	// can't hide behind an untouched proof for the rest of the list.
	KindPolicy LeafKind = "policy"
)

// Leaf identifies one committed item.
type Leaf struct {
	Tool    string // registering tool, e.g. "pulse", "interpose"
	ID      string // stable identifier within that tool, e.g. job name, or "policy"
	Kind    LeafKind
	Payload []byte // canonical, deterministic encoding of the fields that matter
}

// Key is the leaf's sort/lookup key.
func (l Leaf) Key() string {
	return l.Tool + "\x00" + string(l.Kind) + "\x00" + l.ID
}

func writeLenPrefixed(w io.Writer, b []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	w.Write(lenBuf[:]) //nolint:errcheck // io.Writer impls used here (bytes.Buffer, hash.Hash) never error
	w.Write(b)         //nolint:errcheck
}

func leafHash(l Leaf) [32]byte {
	h := sha256.New()
	h.Write([]byte{0x00}) // leaf domain tag
	writeLenPrefixed(h, []byte(l.Tool))
	writeLenPrefixed(h, []byte(l.Kind))
	writeLenPrefixed(h, []byte(l.ID))
	writeLenPrefixed(h, l.Payload)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func nodeHash(left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{0x01}) // internal-node domain tag
	h.Write(left[:])
	h.Write(right[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// ProofStep is one level of a Merkle inclusion proof. Carry=true means the
// node being proved had no sibling at this level (odd node count) and was
// promoted unchanged — never duplicated, which avoids the Merkle-tree
// malleability class of bug behind CVE-2012-2459.
type ProofStep struct {
	Hash  [32]byte
	Left  bool // sibling is the left operand (current node combines as nodeHash(sibling, current))
	Carry bool // no sibling at this level; current node promotes unchanged
}

// Proof is an inclusion proof for one leaf against a Tree's Root.
type Proof struct {
	Steps []ProofStep `json:"steps"`
}

// Tree is a built, immutable Merkle tree over a leaf set.
type Tree struct {
	levels [][][32]byte   // levels[0] = sorted leaf hashes; last level = [root]
	index  map[string]int // Leaf.Key() -> position in levels[0]
}

// Build constructs a Tree over leaves. Leaves are sorted by Key() first, so
// the resulting Root is independent of input order. Rejects an empty leaf
// set and duplicate keys.
func Build(leaves []Leaf) (*Tree, error) {
	if len(leaves) == 0 {
		return nil, fmt.Errorf("commitment: cannot build a tree over zero leaves")
	}

	sorted := make([]Leaf, len(leaves))
	copy(sorted, leaves)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key() < sorted[j].Key() })

	index := make(map[string]int, len(sorted))
	level0 := make([][32]byte, len(sorted))
	for i, l := range sorted {
		key := l.Key()
		if _, dup := index[key]; dup {
			return nil, fmt.Errorf("commitment: duplicate leaf key %q", key)
		}
		index[key] = i
		level0[i] = leafHash(l)
	}

	levels := [][][32]byte{level0}
	cur := level0
	for len(cur) > 1 {
		next := make([][32]byte, 0, (len(cur)+1)/2)
		i := 0
		for i < len(cur) {
			if i+1 < len(cur) {
				next = append(next, nodeHash(cur[i], cur[i+1]))
				i += 2
			} else {
				next = append(next, cur[i]) // carry up unchanged, not duplicated
				i++
			}
		}
		levels = append(levels, next)
		cur = next
	}

	return &Tree{levels: levels, index: index}, nil
}

// Root returns the tree's root hash.
func (t *Tree) Root() [32]byte {
	last := t.levels[len(t.levels)-1]
	return last[0]
}

// ProofFor returns an inclusion proof for the leaf with the given key.
func (t *Tree) ProofFor(key string) (Proof, error) {
	idx, ok := t.index[key]
	if !ok {
		return Proof{}, fmt.Errorf("commitment: no such leaf %q", key)
	}

	var steps []ProofStep
	for lvl := 0; lvl < len(t.levels)-1; lvl++ {
		level := t.levels[lvl]
		switch {
		case idx%2 == 1:
			steps = append(steps, ProofStep{Hash: level[idx-1], Left: true})
		case idx+1 < len(level):
			steps = append(steps, ProofStep{Hash: level[idx+1], Left: false})
		default:
			steps = append(steps, ProofStep{Carry: true})
		}
		idx /= 2
	}
	return Proof{Steps: steps}, nil
}

// VerifyProof reports whether leaf, combined with proof, hashes up to root.
// It always recomputes leaf's hash fresh from the given Leaf value — never
// from anything the caller might have cached — so a tampered Leaf (even one
// paired with an otherwise-valid, untouched proof) fails verification.
func VerifyProof(leaf Leaf, proof Proof, root [32]byte) bool {
	cur := leafHash(leaf)
	for _, s := range proof.Steps {
		if s.Carry {
			continue
		}
		if s.Left {
			cur = nodeHash(s.Hash, cur)
		} else {
			cur = nodeHash(cur, s.Hash)
		}
	}
	return cur == root
}

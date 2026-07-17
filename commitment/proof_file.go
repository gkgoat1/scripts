package commitment

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// proofStepJSON is ProofStep's wire shape: the [32]byte hash is hex, not the
// verbose 32-element JSON number array encoding/json would otherwise produce
// for a fixed-size byte array (only []byte slices get json's base64
// special-casing; [32]byte arrays don't).
type proofStepJSON struct {
	Hash  string `json:"hash,omitempty"`
	Left  bool   `json:"left,omitempty"`
	Carry bool   `json:"carry,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (s ProofStep) MarshalJSON() ([]byte, error) {
	w := proofStepJSON{Left: s.Left, Carry: s.Carry}
	if !s.Carry {
		w.Hash = hex.EncodeToString(s.Hash[:])
	}
	return json.Marshal(w)
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *ProofStep) UnmarshalJSON(data []byte) error {
	var w proofStepJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	s.Left = w.Left
	s.Carry = w.Carry
	if w.Carry {
		return nil
	}
	b, err := hex.DecodeString(w.Hash)
	if err != nil {
		return fmt.Errorf("commitment: invalid proof step hash %q: %w", w.Hash, err)
	}
	if len(b) != 32 {
		return fmt.Errorf("commitment: proof step hash must be 32 bytes, got %d", len(b))
	}
	copy(s.Hash[:], b)
	return nil
}

// ProofFile is the sidecar file format written next to a config file,
// carrying per-leaf inclusion proofs. Root is included for
// debugging/inspection only — it is NEVER trusted by a verifier; the
// authoritative root comes from the anchor (see commitment/anchor).
type ProofFile struct {
	Root    string           `json:"root"`
	Entries map[string]Proof `json:"entries"`
}

// EncodeProofFile serializes root and entries as a ProofFile.
func EncodeProofFile(root [32]byte, entries map[string]Proof) []byte {
	pf := ProofFile{Root: RootHex(root), Entries: entries}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("commitment: encode proof file: %v", err)) // unreachable: ProofFile always marshals
	}
	return data
}

// DecodeProofFile parses a ProofFile.
func DecodeProofFile(data []byte) (ProofFile, error) {
	var pf ProofFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return ProofFile{}, fmt.Errorf("commitment: decode proof file: %w", err)
	}
	return pf, nil
}

// RootHex hex-encodes a root hash.
func RootHex(root [32]byte) string { return hex.EncodeToString(root[:]) }

// ParseRootHex decodes a hex-encoded root hash.
func ParseRootHex(s string) ([32]byte, error) {
	var root [32]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return root, fmt.Errorf("commitment: invalid root hex %q: %w", s, err)
	}
	if len(b) != 32 {
		return root, fmt.Errorf("commitment: root must be 32 bytes, got %d", len(b))
	}
	copy(root[:], b)
	return root, nil
}

package commitment

import (
	"bytes"
	"sort"
)

// EncodeKV deterministically encodes fields as a Leaf Payload: keys sorted,
// each key/value length-prefixed so no ambiguous concatenation is possible
// (e.g. Tool="ab"+ID="c" can never hash the same as Tool="a"+ID="bc").
func EncodeKV(fields map[string]string) []byte {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		writeLenPrefixed(&buf, []byte(k))
		writeLenPrefixed(&buf, []byte(fields[k]))
	}
	return buf.Bytes()
}

// EncodeStringSet deterministically encodes a set of strings: deduplicated,
// sorted, each item length-prefixed. Intended to be embedded as a value
// inside EncodeKV (e.g. a policy leaf's deny-list), not as a Payload alone.
func EncodeStringSet(items []string) []byte {
	seen := make(map[string]bool, len(items))
	uniq := make([]string, 0, len(items))
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			uniq = append(uniq, it)
		}
	}
	sort.Strings(uniq)

	var buf bytes.Buffer
	for _, it := range uniq {
		writeLenPrefixed(&buf, []byte(it))
	}
	return buf.Bytes()
}

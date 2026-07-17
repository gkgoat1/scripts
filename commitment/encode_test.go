package commitment

import (
	"bytes"
	"testing"
)

func TestEncodeKVDeterministicRegardlessOfMapOrder(t *testing.T) {
	a := EncodeKV(map[string]string{"b": "2", "a": "1", "c": "3"})
	b := EncodeKV(map[string]string{"c": "3", "a": "1", "b": "2"})
	if !bytes.Equal(a, b) {
		t.Error("EncodeKV should be independent of map iteration order")
	}
}

func TestEncodeKVAvoidsAmbiguousConcatenation(t *testing.T) {
	// Without length-prefixing, {"a":"b", "c":"d"} could concatenate the
	// same as {"ab":"", "c":"d"} or similar. Length-prefixing must prevent
	// any such collision between differently-shaped inputs.
	a := EncodeKV(map[string]string{"tool": "ab", "id": "c"})
	b := EncodeKV(map[string]string{"tool": "a", "id": "bc"})
	if bytes.Equal(a, b) {
		t.Error("EncodeKV must not collide across a Tool/ID boundary shift")
	}
}

func TestEncodeStringSetDedupsAndSorts(t *testing.T) {
	a := EncodeStringSet([]string{"z", "a", "z", "m"})
	b := EncodeStringSet([]string{"m", "a", "z"})
	if !bytes.Equal(a, b) {
		t.Error("EncodeStringSet should dedup and sort regardless of input order/repeats")
	}
}

func TestEncodeStringSetDiffersOnContent(t *testing.T) {
	a := EncodeStringSet([]string{"/a", "/b"})
	b := EncodeStringSet([]string{"/a"})
	if bytes.Equal(a, b) {
		t.Error("EncodeStringSet must differ when an entry is removed")
	}
}

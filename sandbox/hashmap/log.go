package hashmap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Log is the descriptive operator registry. It records map digest to map but
// does not grant authorization; callers always recompute a candidate digest.
type Log struct {
	Entries map[string]Map `json:"entries"`
	Version int            `json:"version"`
}

func LoadLog(path string) (Log, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Log{Version: Version, Entries: map[string]Map{}}, nil
	}
	if err != nil {
		return Log{}, err
	}
	var l Log
	if err := json.Unmarshal(b, &l); err != nil {
		return Log{}, err
	}
	if l.Version != Version || l.Entries == nil {
		return Log{}, fmt.Errorf("invalid hash-map log")
	}
	for d, m := range l.Entries {
		got, err := m.Digest()
		if err != nil || got != d {
			return Log{}, fmt.Errorf("invalid hash-map log entry %q", d)
		}
	}
	return l, nil
}

// Record atomically inserts a map, rejecting a same-digest different-map
// collision rather than treating the log as authority.
func Record(path string, m Map) (string, error) {
	d, err := m.Digest()
	if err != nil {
		return "", err
	}
	l, err := LoadLog(path)
	if err != nil {
		return "", err
	}
	if old, ok := l.Entries[d]; ok && !old.Equal(m) {
		return "", fmt.Errorf("conflicting map for digest %s", d)
	}
	l.Entries[d] = m
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return d, nil
}

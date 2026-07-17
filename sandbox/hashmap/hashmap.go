// Package hashmap implements the canonical full-file identity map used by the
// sandbox daemon. A map is deliberately independent of platform code signing:
// every entry is a SHA-256 of the complete regular file.
package hashmap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const Version = 1

// Map is the versioned process-code identity. Files maps canonical absolute
// paths to lower-case, complete-file SHA-256 values.
type Map struct {
	Files   map[string]string `json:"files"`
	Version int               `json:"version"`
}

// CanonicalPath resolves a path to its absolute, symlink-free spelling. A
// missing path is an error: identities must not contain future path aliases.
func CanonicalPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("resolved path is not absolute: %q", resolved)
	}
	return filepath.Clean(resolved), nil
}

// FileHash returns the full-file SHA-256 of a stable regular file. It checks
// the descriptor's metadata before and after reading so a concurrently changed
// input fails closed instead of being granted an ambiguous identity.
func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after) {
		return "", fmt.Errorf("%q changed while hashing", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AddPath returns a copy of m with the path's current full-file hash inserted.
func (m Map) AddPath(path string) (Map, error) {
	path, err := CanonicalPath(path)
	if err != nil {
		return Map{}, err
	}
	h, err := FileHash(path)
	if err != nil {
		return Map{}, err
	}
	out := Map{Version: Version, Files: make(map[string]string, len(m.Files)+1)}
	for k, v := range m.Files {
		out.Files[k] = v
	}
	out.Files[path] = h
	return out, out.Validate()
}

// Validate verifies canonical paths and SHA-256 formatting before a map is
// authorized or serialized.
func (m Map) Validate() error {
	if m.Version != Version {
		return fmt.Errorf("unsupported hash-map version %d", m.Version)
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("hash map has no files")
	}
	for p, h := range m.Files {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("non-canonical map path %q", p)
		}
		if len(h) != 64 || strings.ToLower(h) != h {
			return fmt.Errorf("invalid full-file SHA-256 for %q", p)
		}
		if _, err := hex.DecodeString(h); err != nil {
			return fmt.Errorf("invalid full-file SHA-256 for %q: %w", p, err)
		}
	}
	return nil
}

// CanonicalJSON returns a deterministic compact JSON representation. The
// standard encoder sorts string map keys; Marshal also omits no fields here,
// and its output is asserted by tests in this package.
func (m Map) CanonicalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// Digest returns the domain-separated SHA-256 identity of the complete map.
func (m Map) Digest() (string, error) {
	b, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("sandbox-hash-map-v1\x00"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Equal compares canonical map encodings, useful when rejecting log
// collisions rather than trusting a digest string alone.
func (m Map) Equal(other Map) bool {
	a, ea := m.CanonicalJSON()
	b, eb := other.CanonicalJSON()
	return ea == nil && eb == nil && bytes.Equal(a, b)
}

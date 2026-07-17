// Package config owns the strict, logical-home-scoped sandbox policy and its
// Merkle leaf. Config fields are deliberately small: authorization is by map
// digest, never by an ambient environment variable.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gkgoat1/scripts/commitment"
)

const (
	Version      = 1
	PolicyLeafID = "policy"
)

type HashUpdate struct {
	From        string   `json:"from"`
	Extensions  []string `json:"extensions"`
	AllowResult []string `json:"allowResult"`
}

type Config struct {
	Version          int                 `json:"version"`
	EnvironmentAllow map[string][]string `json:"environmentAllow"`
	HashUpdates      []HashUpdate        `json:"hashUpdates"`
}

type Layout struct {
	Home, Library, Tmp, ConfigDir, ConfigPath, ProofPath, HashMapLogPath, CacheDir, TransientRoot, AnchorPath string
}

// DefaultConfigPath preserves a convenient default for operator tooling; the
// daemon itself receives an explicit layout derived from --home.
func DefaultConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "sandbox", "config.json")
}

// NewLayout derives every sandbox state path from an explicit logical home.
func NewLayout(home string) (Layout, error) {
	if home == "" || !filepath.IsAbs(home) {
		return Layout{}, fmt.Errorf("sandbox home must be an absolute path")
	}
	home, err := filepath.EvalSymlinks(filepath.Clean(home))
	if err != nil {
		return Layout{}, fmt.Errorf("resolve sandbox home: %w", err)
	}
	st, err := os.Stat(home)
	if err != nil {
		return Layout{}, err
	}
	if !st.IsDir() {
		return Layout{}, fmt.Errorf("sandbox home is not a directory")
	}
	library := filepath.Join(home, "Library")
	configDir := filepath.Join(library, "Application Support", "sandbox")
	return Layout{Home: home, Library: library, Tmp: filepath.Join(home, "tmp"), ConfigDir: configDir,
		ConfigPath: filepath.Join(configDir, "config.json"), ProofPath: filepath.Join(configDir, "config.json.proof"),
		HashMapLogPath: filepath.Join(configDir, "hash-map-log.json"), CacheDir: filepath.Join(library, "Caches", "sandbox"),
		TransientRoot: filepath.Join(home, "tmp", "sandbox"), AnchorPath: filepath.Join(library, "LaunchAgents", "com.gkgoat.scripts.agentcommit-anchor.plist")}, nil
}

func validDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (c Config) Validate() error {
	if c.Version != Version {
		return fmt.Errorf("unsupported sandbox config version %d", c.Version)
	}
	for name, ds := range c.EnvironmentAllow {
		if name == "" || len(ds) == 0 {
			return fmt.Errorf("invalid environment allow rule %q", name)
		}
		for _, d := range ds {
			if !validDigest(d) {
				return fmt.Errorf("invalid map digest for %s", name)
			}
		}
	}
	for _, r := range c.HashUpdates {
		if !validDigest(r.From) || len(r.Extensions) == 0 || len(r.AllowResult) == 0 {
			return fmt.Errorf("invalid hash update rule")
		}
		for _, ext := range r.Extensions {
			if !strings.HasPrefix(ext, ".") {
				return fmt.Errorf("hash-update extension %q lacks dot", ext)
			}
		}
		for _, d := range r.AllowResult {
			if !validDigest(d) {
				return fmt.Errorf("invalid update result digest")
			}
		}
	}
	return nil
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, err
	}
	if dec.More() {
		return Config{}, fmt.Errorf("trailing sandbox config data")
	}
	return c, c.Validate()
}

// CommitLeaf commits every authorization-changing field using canonical JSON.
func (c Config) CommitLeaf() commitment.Leaf {
	payload, err := canonicalJSON(c)
	if err != nil {
		panic(err)
	} // Config is validated before it becomes trusted.
	return commitment.Leaf{Tool: "sandbox", ID: PolicyLeafID, Kind: commitment.KindPolicy, Payload: payload}
}

func canonicalJSON(c Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	for k, ds := range c.EnvironmentAllow {
		sort.Strings(ds)
		c.EnvironmentAllow[k] = ds
	}
	for i := range c.HashUpdates {
		sort.Strings(c.HashUpdates[i].Extensions)
		sort.Strings(c.HashUpdates[i].AllowResult)
	}
	return json.Marshal(c)
}

func (c Config) EnvAllowed(name, digest string) bool {
	ds, ok := c.EnvironmentAllow[name]
	if !ok {
		return true
	}
	for _, d := range ds {
		if d == digest {
			return true
		}
	}
	return false
}

func (c Config) AllowsUpdate(from, ext, result string) bool {
	for _, r := range c.HashUpdates {
		if r.From != from {
			continue
		}
		match := false
		for _, e := range r.Extensions {
			if strings.EqualFold(e, ext) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		for _, d := range r.AllowResult {
			if d == result {
				return true
			}
		}
	}
	return false
}

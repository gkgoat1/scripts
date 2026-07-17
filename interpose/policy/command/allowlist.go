// Package command verifies a committed allowlist before dangerous command
// interposers delegate to their real binaries.
package command

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gkgoat1/scripts/commitment"
)

const (
	// PolicyLeafID is the fixed ID for this command-policy commitment leaf.
	PolicyLeafID = "allowlist"
	// ConfigEnv overrides the default committed allowlist path. It is intended
	// for test fixtures and explicit alternate deployments, never a fallback.
	ConfigEnv = "INTERPOSE_COMMAND_ALLOWLIST"
)

//go:embed allowlist.json
var defaultAllowlist []byte

// Allowlist maps a command name to permitted argument vectors. The literal
// {pid} matches one non-negative decimal PID; every other value is exact.
type Allowlist map[string][][]string

// DefaultConfigPath returns the installed/user-controlled allowlist path.
func DefaultConfigPath() string {
	if path := os.Getenv(ConfigEnv); path != "" {
		return path
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "interpose", "command-allowlist.json")
}

// Load reads the committed allowlist. When the user-level file is absent, the
// repository-embedded default is used, so a secure empty policy works without
// creating a writable config file.
func Load(path string) (Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read command allowlist %s: %w", path, err)
		}
		data = defaultAllowlist
	}
	var list Allowlist
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decode command allowlist %s: %w", path, err)
	}
	return list, nil
}

// CommitLeaf returns the commitment leaf covering the entire allowlist.
func (a Allowlist) CommitLeaf() commitment.Leaf {
	return commitment.Leaf{
		Tool:    "interpose-command",
		ID:      PolicyLeafID,
		Kind:    commitment.KindPolicy,
		Payload: canonicalJSON(a),
	}
}

func canonicalJSON(a Allowlist) []byte {
	data, err := json.Marshal(a)
	if err != nil {
		panic(fmt.Sprintf("command allowlist: marshal: %v", err))
	}
	return data
}

// Allows reports whether args precisely matches one allowlisted argument
// vector. A {pid} position accepts only a decimal process ID, including 0.
func (a Allowlist) Allows(name string, args []string) bool {
	for _, rule := range a[name] {
		if len(rule) != len(args) {
			continue
		}
		matched := true
		for i, want := range rule {
			if want == "{pid}" {
				if !isPID(args[i]) {
					matched = false
					break
				}
				continue
			}
			if args[i] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func isPID(value string) bool {
	return value != "" && strings.Trim(value, "0123456789") == ""
}

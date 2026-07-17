// Package anchor reads the Merkle root committed into the
// agentcommit-anchor LaunchAgent's plist — the one write location BlockBlock
// /LuLu already watch, per docs/agentcommit.md's design. It never writes the
// plist itself; that's installer/launchagent.sh's job (reused unmodified,
// see agentcommit's install script).
package anchor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gkgoat1/scripts/commitment"
)

// Label is the anchor LaunchAgent's identifier.
const Label = "com.gkgoat.scripts.agentcommit-anchor"

// PlistPath returns the anchor LaunchAgent's plist path.
func PlistPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", Label+".plist")
}

// ErrAnchorNotInstalled means the anchor plist doesn't exist: commitment
// verification was never adopted on this machine, so callers should treat
// this as "verification disabled," not as a tampering signal.
var ErrAnchorNotInstalled = errors.New("agentcommit: anchor LaunchAgent not installed")

// PlistToJSON converts a plist file to the JSON encoding plutil would
// produce. Injectable so tests never require a real plist or real plutil.
type PlistToJSON interface {
	Convert(path string) ([]byte, error)
}

// realPlistToJSON shells out to macOS's built-in plutil — no new Go
// dependency, mirroring extclean's python3/tomllib bridge for Codex's TOML.
type realPlistToJSON struct{}

// NewRealPlistToJSON returns the real, plutil-backed PlistToJSON.
func NewRealPlistToJSON() PlistToJSON { return realPlistToJSON{} }

func (realPlistToJSON) Convert(path string) ([]byte, error) {
	return exec.Command("plutil", "-convert", "json", path, "-o", "-").Output()
}

// AnchorReader reads the currently-committed root.
type AnchorReader interface {
	ReadRoot() ([32]byte, error)
}

// PlistAnchorReader is the real AnchorReader implementation.
type PlistAnchorReader struct {
	Converter PlistToJSON
	// Path overrides PlistPath(), for tests.
	Path string
}

type anchorPlistDoc struct {
	ProgramArguments []string `json:"ProgramArguments"`
}

// ReadRoot implements AnchorReader.
//
// Only "the plist file doesn't exist" maps to ErrAnchorNotInstalled. Every
// other failure (plutil missing, corrupted plist, malformed JSON, missing
// -root argument, bad hex) is returned as a distinct, opaque error —
// deliberately not collapsed into ErrAnchorNotInstalled, so that breaking
// the anchor read path (e.g. deleting plutil) can never look identical to
// "commitment verification was never turned on."
func (r PlistAnchorReader) ReadRoot() ([32]byte, error) {
	path := r.Path
	if path == "" {
		path = PlistPath()
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return [32]byte{}, ErrAnchorNotInstalled
		}
		return [32]byte{}, fmt.Errorf("stat anchor plist %s: %w", path, err)
	}

	data, err := r.Converter.Convert(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("convert anchor plist %s: %w", path, err)
	}

	var doc anchorPlistDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return [32]byte{}, fmt.Errorf("decode anchor plist %s: %w", path, err)
	}

	for i, a := range doc.ProgramArguments {
		if a == "-root" && i+1 < len(doc.ProgramArguments) {
			root, err := commitment.ParseRootHex(doc.ProgramArguments[i+1])
			if err != nil {
				return [32]byte{}, fmt.Errorf("anchor plist %s: %w", path, err)
			}
			return root, nil
		}
	}
	return [32]byte{}, fmt.Errorf("anchor plist %s: no -root argument in ProgramArguments", path)
}

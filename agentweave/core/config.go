package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config controls local-only scanning. It deliberately has no remote endpoint
// or model credential fields.
type Config struct {
	ArtifactRoots []string `json:"artifact_roots,omitempty"`
	DenyGlobs     []string `json:"deny_globs,omitempty"`
	PollSeconds   int      `json:"poll_seconds,omitempty"`
}

func DefaultDataDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "AgentWeave")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "agentweave")
	}
	return filepath.Join(home, ".local", "share", "agentweave")
}

func DefaultConfigPath(home string) string {
	return filepath.Join(DefaultDataDir(home), "config.json")
}

func LoadConfig(path string) (Config, error) {
	var config Config
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read AgentWeave config: %w", err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("decode AgentWeave config: %w", err)
	}
	if config.PollSeconds <= 0 {
		config.PollSeconds = 30
	}
	for i, root := range config.ArtifactRoots {
		config.ArtifactRoots[i] = CanonicalWorkspace(root)
	}
	return config, nil
}

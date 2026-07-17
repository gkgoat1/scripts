package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config holds optional interposer overrides.
type Config struct {
	ExtraProtectedPaths []string
	DisableSnapshot     []string
	SnapshotPrefix      string
	ToolTimeout         string // duration string, e.g. "30s"
}

var loaded *Config

// DefaultConfigPath returns ~/.config/interpose/config.
func DefaultConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "interpose", "config")
}

// Load reads ~/.config/interpose/config (simple key: value lines).
func Load() Config {
	if loaded != nil {
		return *loaded
	}
	cfg := Config{
		SnapshotPrefix: "interpose/snapshot",
	}
	path := DefaultConfigPath()
	f, err := os.Open(path)
	if err != nil {
		loaded = &cfg
		return cfg
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "extra-protected-path":
			cfg.ExtraProtectedPaths = append(cfg.ExtraProtectedPaths, val)
		case "disable-snapshot":
			cfg.DisableSnapshot = append(cfg.DisableSnapshot, val)
		case "snapshot-prefix":
			if val != "" {
				cfg.SnapshotPrefix = val
			}
		case "tool-timeout":
			cfg.ToolTimeout = val
		}
	}
	loaded = &cfg
	return cfg
}

// Reset clears the cached config (for tests).
func Reset() {
	loaded = nil
}

// SnapshotsDisabled reports whether repoRoot matches a disable-snapshot prefix.
func SnapshotsDisabled(repoRoot string) bool {
	cfg := Load()
	for _, prefix := range cfg.DisableSnapshot {
		if strings.HasPrefix(repoRoot, prefix) {
			return true
		}
	}
	return false
}

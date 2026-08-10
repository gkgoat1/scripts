// Package local centralizes AgentWeave's on-machine paths and service setup.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gkgoat1/scripts/agentweave/core"
	"github.com/gkgoat1/scripts/agentweave/daemon"
)

type Paths struct {
	Home    string
	DataDir string
	DB      string
	Socket  string
	PID     string
	Log     string
}

func Resolve(home, dataDir string) (Paths, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
	}
	if dataDir == "" {
		dataDir = os.Getenv("AGENTWEAVE_DATA_DIR")
	}
	if dataDir == "" {
		dataDir = core.DefaultDataDir(home)
	}
	dataDir = filepath.Clean(dataDir)
	return Paths{
		Home: home, DataDir: dataDir,
		DB: filepath.Join(dataDir, "index.sqlite"), Socket: filepath.Join(dataDir, "agentweave.sock"),
		PID: filepath.Join(dataDir, "agentweaved.pid"), Log: filepath.Join(dataDir, "agentweaved.log"),
	}, nil
}

func OpenService(paths Paths) (*core.Index, *daemon.Service, core.Config, error) {
	configPath := filepath.Join(paths.DataDir, "config.json")
	// Config contains only local source paths, but it still lives in the
	// private data directory and is kept owner-readable only if present.
	_ = os.Chmod(configPath, 0o600)
	config, err := core.LoadConfig(configPath)
	if err != nil {
		return nil, nil, config, err
	}
	index, err := core.Open(paths.DB)
	if err != nil {
		return nil, nil, config, err
	}
	enabled := config.WorkflowsEnabled()
	service := &daemon.Service{Index: index, Options: core.ScanOptions{Home: paths.Home, ArtifactRoots: config.ArtifactRoots, DenyGlobs: config.DenyGlobs, WorkflowIndexing: &enabled}}
	return index, service, config, nil
}

func SyncOnce(ctx context.Context, paths Paths) ([]core.SourceStatus, error) {
	index, service, _, err := OpenService(paths)
	if err != nil {
		return nil, err
	}
	defer index.Close()
	return service.Sync(ctx)
}

func EnsureDataDir(paths Paths) error {
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		return fmt.Errorf("create AgentWeave data directory: %w", err)
	}
	return os.Chmod(paths.DataDir, 0o700)
}

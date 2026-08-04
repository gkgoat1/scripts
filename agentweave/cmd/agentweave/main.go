package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gkgoat1/scripts/agentweave/core"
	"github.com/gkgoat1/scripts/agentweave/daemon"
	"github.com/gkgoat1/scripts/agentweave/local"
	"github.com/gkgoat1/scripts/agentweave/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	flags := flag.NewFlagSet("agentweave "+command, flag.ExitOnError)
	home := flags.String("home", "", "home directory to scan (tests/debugging)")
	dataDir := flags.String("data-dir", "", "AgentWeave data directory")
	flags.Parse(os.Args[2:])
	paths, err := local.Resolve(*home, *dataDir)
	fatalIf(err)
	ctx := context.Background()
	switch command {
	case "start":
		start(ctx, paths)
	case "stop":
		stop(paths)
	case "sync":
		sync(ctx, paths)
	case "status":
		status(ctx, paths)
	case "doctor":
		doctor(ctx, paths)
	case "mcp":
		fatalIf(mcpserver.New(daemon.Client{Socket: paths.Socket}, version).Run(ctx, &mcp.StdioTransport{}))
	case "config":
		if flags.NArg() != 1 {
			fatalIf(fmt.Errorf("usage: agentweave config <claude|cursor|codex|antigravity|pi>"))
		}
		fmt.Print(configSnippet(flags.Arg(0)))
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func start(ctx context.Context, paths local.Paths) {
	client := daemon.Client{Socket: paths.Socket}
	if _, err := client.Status(ctx); err == nil {
		fmt.Println("AgentWeave daemon is already running")
		return
	}
	fatalIf(local.EnsureDataDir(paths))
	binary, err := daemonBinary()
	fatalIf(err)
	log, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	fatalIf(err)
	defer log.Close()
	_ = os.Chmod(paths.Log, 0o600)
	command := exec.Command(binary, "-data-dir", paths.DataDir)
	command.Stdout, command.Stderr = log, log
	fatalIf(command.Start())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Status(ctx); err == nil {
			fmt.Println("AgentWeave daemon started")
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	fatalIf(fmt.Errorf("agentweaved started but did not become ready; see %s", paths.Log))
}

func daemonBinary() (string, error) {
	if current, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(current), "agentweaved")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if binary, err := exec.LookPath("agentweaved"); err == nil {
		return binary, nil
	}
	return "", errors.New("agentweaved is not on PATH; build/install both AgentWeave commands first")
}

func stop(paths local.Paths) {
	data, err := os.ReadFile(paths.PID)
	if err != nil {
		fatalIf(fmt.Errorf("read daemon pid: %w", err))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	fatalIf(err)
	process, err := os.FindProcess(pid)
	fatalIf(err)
	fatalIf(process.Signal(syscall.SIGTERM))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.Socket); os.IsNotExist(err) {
			fmt.Println("AgentWeave daemon stopped")
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	fatalIf(fmt.Errorf("daemon did not stop in time"))
}

func sync(ctx context.Context, paths local.Paths) {
	var statuses []core.SourceStatus
	err := daemon.Client{Socket: paths.Socket}.Call(ctx, "sync", struct{}{}, &statuses)
	if err != nil {
		statuses, err = local.SyncOnce(ctx, paths)
	}
	fatalIf(err)
	printJSON(statuses)
}

func status(ctx context.Context, paths local.Paths) {
	statuses, err := (daemon.Client{Socket: paths.Socket}).Status(ctx)
	if err != nil {
		index, openErr := core.Open(paths.DB)
		fatalIf(openErr)
		defer index.Close()
		statuses, err = index.Status(ctx)
	}
	fatalIf(err)
	printJSON(statuses)
}

func doctor(ctx context.Context, paths local.Paths) {
	result := map[string]any{"data_dir": paths.DataDir, "socket": paths.Socket, "checks": map[string]any{}}
	checks := result["checks"].(map[string]any)
	if info, err := os.Stat(paths.DataDir); err == nil {
		checks["data_dir_mode"] = info.Mode().Perm().String()
	} else {
		checks["data_dir"] = err.Error()
	}
	if info, err := os.Stat(paths.Socket); err == nil {
		checks["socket_mode"] = info.Mode().Perm().String()
	} else {
		checks["daemon"] = err.Error()
	}
	config, err := core.LoadConfig(filepath.Join(paths.DataDir, "config.json"))
	if err != nil {
		checks["config"] = err.Error()
	} else {
		checks["artifact_roots"] = config.ArtifactRoots
	}
	if statuses, err := (daemon.Client{Socket: paths.Socket}).Status(ctx); err == nil {
		checks["sources"] = statuses
	}
	printJSON(result)
}

func configSnippet(client string) string {
	switch client {
	case "claude", "cursor", "antigravity":
		return "{\n  \"mcpServers\": {\n    \"agentweave\": {\n      \"command\": \"agentweave\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}\n"
	case "codex":
		return "[mcp_servers.agentweave]\ncommand = \"agentweave\"\nargs = [\"mcp\"]\n"
	case "pi":
		return "pi install npm:agentweave-pi-mcp\n# Then start agentweaved; the extension launches its own stdio MCP client per tool call.\n"
	default:
		fatalIf(fmt.Errorf("unknown client %q", client))
		return ""
	}
}

func printJSON(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }

func usage() {
	fmt.Fprintln(os.Stderr, "usage: agentweave <start|stop|sync|status|doctor|mcp|config|version> [flags]")
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentweave:", err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gkgoat1/scripts/agentweave/daemon"
	"github.com/gkgoat1/scripts/agentweave/local"
)

func main() {
	flags := flag.NewFlagSet("agentweaved", flag.ExitOnError)
	home := flags.String("home", "", "home directory to scan (tests/debugging)")
	dataDir := flags.String("data-dir", "", "AgentWeave data directory")
	poll := flags.Int("poll-seconds", 0, "rescan interval in seconds")
	once := flags.Bool("once", false, "sync once and exit")
	flags.Parse(os.Args[1:])
	paths, err := local.Resolve(*home, *dataDir)
	fatalIf(err)
	fatalIf(local.EnsureDataDir(paths))
	index, service, config, err := local.OpenService(paths)
	fatalIf(err)
	defer index.Close()
	if *once {
		statuses, err := service.Sync(context.Background())
		fatalIf(err)
		_ = json.NewEncoder(os.Stdout).Encode(statuses)
		return
	}
	interval := config.PollSeconds
	if *poll > 0 {
		interval = *poll
	}
	server, err := daemon.Listen(service, paths.Socket)
	fatalIf(err)
	defer server.Close()
	fatalIf(os.WriteFile(paths.PID, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600))
	_ = os.Chmod(paths.PID, 0o600)
	defer os.Remove(paths.PID)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fatalIf(server.ServeWithPoll(ctx, timeSeconds(interval)))
}

func timeSeconds(value int) time.Duration {
	if value <= 0 {
		value = 30
	}
	return time.Duration(value) * time.Second
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentweaved:", err)
		os.Exit(1)
	}
}

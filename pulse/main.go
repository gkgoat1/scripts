// Command pulse runs a set of named, independently-scheduled shell commands
// on their own intervals, with an optional per-job 1-minute load-average
// ceiling that skips a firing rather than adding load to an already-busy
// machine. See docs/pulse.md for the config file format.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", defaultConfigPath(), "path to job config file")
	once := flag.Bool("once", false, "fire every job once immediately and exit (ignores intervals; still honors the load gate)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: pulse [-config path] [-once]\n\n")
		fmt.Fprintf(os.Stderr, "Runs the jobs defined in the config file on their configured intervals\n")
		fmt.Fprintf(os.Stderr, "until interrupted (SIGINT/SIGTERM). See docs/pulse.md.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	jobs, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		os.Exit(2)
	}

	sched := NewScheduler(realCommandRunner{}, sysctlLoadChecker{}, newRealTicker, os.Stdout, os.Stderr)

	if *once {
		for _, j := range jobs {
			sched.fire(j)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Printf("[start] pulse: %d job(s) loaded from %s\n", len(jobs), *configPath)
	sched.Run(ctx, jobs)
	fmt.Println("[stop] pulse: shutdown complete")
}

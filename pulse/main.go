// v2 tasks is a strict, domain-separated task config.  V2 `service` and
// `rapid-service` tasks run under the daemon; their full execution and restart
// policy is committed through agentcommit before Pulse will start them.
// `rapid-service` is the explicit migration path for a legitimate immediate
// respawn loop: unlike normal services, it restarts after every exit (including
// success) with no delay. See docs/pulse-task-domains-plan.md.
//
// Command pulse runs a set of named, independently-scheduled shell commands
// on their own intervals, with an optional per-job 1-minute load-average
// ceiling that skips a firing rather than adding load to an already-busy
// machine. See docs/pulse.md for the config file format.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gkgoat1/scripts/commitment/anchor"
	"github.com/gkgoat1/scripts/internal/proxypass"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
	"github.com/gkgoat1/scripts/pulse/tasks"
)

func main() {
	configPath := flag.String("config", pconfig.DefaultConfigPath(), "path to job config file")
	once := flag.Bool("once", false, "fire every job once immediately and exit (ignores intervals; still honors the load gate)")
	taskConfig := flag.String("tasks-config", tasks.DefaultConfigPath(), "path to Pulse v2 task config")
	proxy := flag.Bool("proxy", false, "start a loopback passthrough proxy and inject it into each job")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: pulse [-config path] [-tasks-config path] [-once]\n\n")
		fmt.Fprintf(os.Stderr, "Runs the jobs defined in the config file on their configured intervals\n")
		fmt.Fprintf(os.Stderr, "until interrupted (SIGINT/SIGTERM). See docs/pulse.md.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	jobs, legacyErr := pconfig.LoadConfig(*configPath)
	if legacyErr != nil && !errors.Is(legacyErr, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "[error] %v\n", legacyErr)
		os.Exit(2)
	}
	if errors.Is(legacyErr, os.ErrNotExist) {
		if *configPath != pconfig.DefaultConfigPath() {
			fmt.Fprintf(os.Stderr, "[error] %v\n", legacyErr)
			os.Exit(2)
		}
		jobs = nil
	}

	var v2Tasks []tasks.Task
	if loaded, taskErr := tasks.LoadConfig(*taskConfig); taskErr == nil {
		v2Tasks = loaded
	} else if !errors.Is(taskErr, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "[error] %v\n", taskErr)
		os.Exit(2)
	}

	if len(jobs) == 0 && len(v2Tasks) == 0 {
		fmt.Fprintln(os.Stderr, "[error] pulse: no legacy jobs or v2 tasks defined")
		os.Exit(2)
	}
	if *once && len(jobs) == 0 {
		fmt.Fprintln(os.Stderr, "[error] pulse: -once only fires legacy scheduled jobs; no legacy jobs are configured")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var proxyURL string
	if *proxy {
		px, err := proxypass.Start(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] pulse: start proxy: %v\n", err)
			os.Exit(1)
		}
		proxyURL = px.URLString()
		fmt.Printf("[proxy] pulse: child proxy on %s\n", proxyURL)
	}

	sched := NewScheduler(realCommandRunner{proxyURL: proxyURL}, sysctlLoadChecker{}, newRealTicker, os.Stdout, os.Stderr)
	sched.Verifier = realCommitmentVerifier{
		Anchor:    anchor.PlistAnchorReader{Converter: anchor.NewRealPlistToJSON()},
		ProofFile: *configPath + ".proof",
	}

	// V2 services are intentionally daemon-owned. Their verifier fails closed
	// if the v2 anchor/proof has not been adopted.
	var serviceWG sync.WaitGroup
	if len(v2Tasks) > 0 && !*once {
		supervisor := &TaskSupervisor{Runner: realCommandRunner{proxyURL: proxyURL}, Verifier: taskCommitmentVerifier{
			Anchor: anchor.PlistAnchorReader{Converter: anchor.NewRealPlistToJSON()}, ProofFile: *taskConfig + ".proof",
		}, Out: os.Stdout, Err: os.Stderr}
		serviceWG.Add(1)
		go func() { defer serviceWG.Done(); supervisor.Run(ctx, v2Tasks) }()
	}

	if *once {
		for _, j := range jobs {
			sched.fire(j)
		}
		fmt.Println("[stop] pulse: shutdown complete")
		return
	}

	fmt.Printf("[start] pulse: %d legacy job(s), %d v2 task(s) loaded\n", len(jobs), len(v2Tasks))
	if len(jobs) > 0 {
		sched.Run(ctx, jobs)
	} else {
		<-ctx.Done()
	}
	serviceWG.Wait()
	fmt.Println("[stop] pulse: shutdown complete")
}

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

	"github.com/gkgoat1/scripts/commitment/anchor"
	"github.com/gkgoat1/scripts/internal/proxypass"
	pconfig "github.com/gkgoat1/scripts/pulse/config"
)

func main() {
	configPath := flag.String("config", pconfig.DefaultConfigPath(), "path to job config file")
	once := flag.Bool("once", false, "fire every job once immediately and exit (ignores intervals; still honors the load gate)")
	proxy := flag.Bool("proxy", false, "start a loopback passthrough proxy and inject it into each job")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: pulse [-config path] [-once]\n\n")
		fmt.Fprintf(os.Stderr, "Runs the jobs defined in the config file on their configured intervals\n")
		fmt.Fprintf(os.Stderr, "until interrupted (SIGINT/SIGTERM). See docs/pulse.md.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	jobs, err := pconfig.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
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

	if *once {
		for _, j := range jobs {
			sched.fire(j)
		}
		fmt.Println("[stop] pulse: shutdown complete")
		return
	}

	fmt.Printf("[start] pulse: %d job(s) loaded from %s\n", len(jobs), *configPath)
	sched.Run(ctx, jobs)
	fmt.Println("[stop] pulse: shutdown complete")
}

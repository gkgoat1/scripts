// Command agentcommit maintains a Merkle commitment over spawnable commands
// (pulse) and security policy (interpose/sandbox) so tampering with either
// is either caught locally by the owning tool, or trips the same
// BlockBlock/LuLu persistence-monitoring alert an operator already watches.
// See docs/agentcommit.md.
package main

import (
	"flag"
	"fmt"
	"os"

	pconfig "github.com/gkgoat1/scripts/pulse/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "commit":
		runCommitCLI(os.Args[2:])
	case "anchor":
		runAnchorCLI(os.Args[2:])
	case "-h", "-help", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "agentcommit: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: agentcommit <commit|anchor> [flags]\n\n")
	fmt.Fprintf(os.Stderr, "  commit [-pulse-config path]   recompute the commitment tree, write proof sidecars, print the hex root\n")
	fmt.Fprintf(os.Stderr, "  anchor -root <hex>            the anchor LaunchAgent's ProgramArguments target; validates and exits\n\n")
	fmt.Fprintf(os.Stderr, "See docs/agentcommit.md.\n")
}

func runCommitCLI(args []string) {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	pulseConfigPath := fs.String("pulse-config", pconfig.DefaultConfigPath(), "path to pulse's job config")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if _, err := runCommit(*pulseConfigPath, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		os.Exit(1)
	}
}

func runAnchorCLI(args []string) {
	root, err := anchorFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		os.Exit(2)
	}
	if err := runAnchor(root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		os.Exit(1)
	}
}

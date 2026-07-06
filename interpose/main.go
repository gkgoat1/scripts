package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/wrappers"
)

func main() {
	name := filepath.Base(os.Args[0])
	args := os.Args[1:]

	var w core.Wrapper
	switch name {
	case "git":
		w = wrappers.Git{}
	case "find":
		w = wrappers.Find{}
	case "grep":
		w = wrappers.Grep{}
	case "interpose":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "usage: interpose <git|find|grep> [args...]")
			os.Exit(2)
		}
		name = args[0]
		args = args[1:]
		switch name {
		case "git":
			w = wrappers.Git{}
		case "find":
			w = wrappers.Find{}
		case "grep":
			w = wrappers.Grep{}
		default:
			fmt.Fprintf(os.Stderr, "interpose: unknown command %q\n", name)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "interpose: unknown invocation name %q\n", name)
		os.Exit(2)
	}

	core.Execute(w, args)
}

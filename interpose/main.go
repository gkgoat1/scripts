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

	if name == "interpose" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "usage: interpose <git|find|grep|kill|pkill|killall|osascript> [args...]")
			os.Exit(2)
		}
		name, args = args[0], args[1:]
	}

	w, ok := wrapperFor(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "interpose: unknown command %q\n", name)
		os.Exit(2)
	}
	core.Execute(w, args)
}

func wrapperFor(name string) (core.Wrapper, bool) {
	switch name {
	case "git":
		return wrappers.Git{}, true
	case "find":
		return wrappers.Find{}, true
	case "grep":
		return wrappers.Grep{}, true
	case "kill", "pkill", "killall", "osascript":
		return wrappers.ProtectedCommand{CommandName: name}, true
	default:
		return nil, false
	}
}

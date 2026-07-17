package wrappers

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
)

// Grep wraps grep with TCC path filtering.
type Grep struct{}

func (Grep) Name() string { return "grep" }

func (Grep) Transform(ctx *core.Context, args []string) ([]string, error) {
	out, bypass := core.StripNoInterpose(args)
	if bypass {
		return out, nil
	}
	return transformGrep(ctx, out)
}

func (Grep) Before(_ *core.Context) error { return nil }

func (Grep) After(_ *core.Context, _ error) error { return nil }

func transformGrep(ctx *core.Context, args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	recursive := core.HasFlag(args, "-r") || core.HasFlag(args, "-R") ||
		core.HasFlag(args, "--recursive")
	pathStart := grepPathStart(args)
	if pathStart >= len(args) {
		if recursive {
			return injectGrepExcludes(ctx, args), nil
		}
		return args, nil
	}

	var kept []string
	var dropped []string
	kept = append(kept, args[:pathStart]...)
	for _, p := range args[pathStart:] {
		if ctx.Policy.IsProtected(p) || wouldTraverseGrepProtected(ctx, p) {
			dropped = append(dropped, p)
			continue
		}
		kept = append(kept, p)
	}
	for _, p := range dropped {
		fmt.Fprintf(ctx.Ops.Stderr(), "[interpose] grep: skipping protected path %q\n", p)
	}
	if recursive && (len(dropped) > 0 || pathStart == len(args)) {
		kept = injectGrepExcludes(ctx, kept)
	}
	if len(kept) == pathStart && len(dropped) > 0 {
		return nil, fmt.Errorf("all path operands are TCC-protected")
	}
	return kept, nil
}

func grepPathStart(args []string) int {
	skipNext := false
	pos := 0
	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			if needsValue(a) || (len(a) == 2 && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-")) {
				// -e pattern style combined flags handled below
			}
			if needsValue(a) {
				skipNext = true
			}
			continue
		}
		pos++
		if pos == 1 {
			continue // pattern
		}
		return i
	}
	return len(args)
}

func needsValue(flag string) bool {
	switch flag {
	case "-e", "--regexp", "-f", "--file", "-m", "--max-count", "-A", "--after-context",
		"-B", "--before-context", "-C", "--context", "--color", "--label", "--include",
		"--exclude", "--exclude-dir", "--group-separator", "--binary-files":
		return true
	default:
		return false
	}
}

func injectGrepExcludes(ctx *core.Context, args []string) []string {
	pathStart := grepPathStart(args)
	prefix := append([]string{}, args[:pathStart]...)
	suffix := append([]string{}, args[pathStart:]...)
	for _, root := range ctx.Policy.ExtraProtectedPaths {
		base := filepath.Base(root)
		if base != "" && base != "." {
			prefix = append(prefix, "--exclude-dir="+base)
		}
	}
	return append(prefix, suffix...)
}

func wouldTraverseGrepProtected(ctx *core.Context, root string) bool {
	norm, err := tcc.NormalizePath(root)
	if err != nil {
		return false
	}
	return norm == "/" || norm == ctx.Dir || ctx.Policy.IsProtected(norm)
}

// TransformGrep exposes grep rewriting for tests.
func TransformGrep(args []string) ([]string, error) {
	return transformGrep(core.NewContext("grep", nil, ""), args)
}

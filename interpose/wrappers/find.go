package wrappers

import (
	"path/filepath"
	"strings"

	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
)

// Find wraps find with TCC prune injection.
type Find struct{}

func (Find) Name() string { return "find" }

func (Find) Transform(ctx *core.Context, args []string) ([]string, error) {
	out, bypass := core.StripNoInterpose(args)
	if bypass {
		return out, nil
	}
	return transformFind(ctx, out)
}

func (Find) Before(_ *core.Context) error { return nil }

func (Find) After(_ *core.Context, _ error) error { return nil }

func transformFind(ctx *core.Context, args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	startIdx := findStartIndex(args)
	if startIdx >= len(args) {
		return args, nil
	}
	root := args[startIdx]
	if wouldTraverseProtected(ctx, root) {
		prunes := buildFindPrunes(ctx.Policy.ExtraProtectedPaths)
		if len(prunes) == 0 {
			return args, nil
		}
		rest := args[startIdx+1:]
		// find ROOT \( prune \) -o REST
		out := append([]string{}, args[:startIdx]...)
		out = append(out, root)
		out = append(out, prunes...)
		out = append(out, "-o")
		out = append(out, rest...)
		return out, nil
	}
	return args, nil
}

func findStartIndex(args []string) int {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return i
	}
	return len(args)
}

func buildFindPrunes(roots []string) []string {
	var parts []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if len(parts) > 1 {
			parts = append(parts, "-o")
		}
		parts = append(parts, "-path", root, "-prune")
	}
	if len(parts) == 0 {
		return nil
	}
	return append(append([]string{"\\("}, parts...), "\\)")
}

func wouldTraverseProtected(ctx *core.Context, root string) bool {
	norm, err := tcc.NormalizePath(root)
	if err != nil {
		return false
	}
	if norm == "/" || norm == ctx.Dir {
		return true
	}
	if ctx.Policy.IsProtected(norm) {
		return true
	}
	for _, protected := range ctx.Policy.ExtraProtectedPaths {
		protected = filepath.Clean(protected)
		if strings.HasPrefix(protected, norm+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// TransformFind exposes find rewriting for tests.
func TransformFind(args []string) ([]string, error) {
	return transformFind(core.NewContext("find", nil, ""), args)
}

// FindWouldRewrite reports whether a protected root scan would be rewritten.
func FindWouldRewrite(args []string) bool {
	out, err := TransformFind(args)
	return err == nil && len(out) != len(args)
}

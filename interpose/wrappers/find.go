package wrappers

import (
	"strings"

	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
)

// Find wraps find with TCC prune injection.
type Find struct{}

func (Find) Name() string { return "find" }

func (Find) Transform(_ *core.Context, args []string) ([]string, error) {
	out, bypass := core.StripNoInterpose(args)
	if bypass {
		return out, nil
	}
	return transformFind(out)
}

func (Find) Before(_ *core.Context) error { return nil }

func (Find) After(_ *core.Context, _ error) error { return nil }

func transformFind(args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	startIdx := findStartIndex(args)
	if startIdx >= len(args) {
		return args, nil
	}
	root := args[startIdx]
	if tcc.WouldTraverseProtected(root) {
		prunes := buildFindPrunes()
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

func buildFindPrunes() []string {
	var roots []string
	for _, root := range tcc.ProtectedRoots() {
		if root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	var parts []string
	parts = append(parts, "\\(")
	for i, root := range roots {
		if i > 0 {
			parts = append(parts, "-o")
		}
		parts = append(parts, "-path", root, "-prune")
	}
	parts = append(parts, "\\)")
	return parts
}

// TransformFind exposes find rewriting for tests.
func TransformFind(args []string) ([]string, error) { return transformFind(args) }

// FindWouldRewrite reports whether a protected root scan would be rewritten.
func FindWouldRewrite(args []string) bool {
	out, err := transformFind(args)
	return err == nil && len(out) != len(args)
}

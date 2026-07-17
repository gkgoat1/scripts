package core

import (
	"github.com/gkgoat1/scripts/commitment/anchor"
	"github.com/gkgoat1/scripts/interpose/config"
	commandpolicy "github.com/gkgoat1/scripts/interpose/policy/command"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
)

// HostPolicyView captures legacy host interposer policy once at the CLI
// boundary. Sandbox protocol callers must supply their committed policy view
// instead of calling this function.
func HostPolicyView() PolicyView {
	cfg := config.Load()
	roots, err := tcc.DefaultProtectedRoots()
	if err != nil {
		roots = nil
	}
	roots = append(roots, cfg.ExtraProtectedPaths...)
	list, err := commandpolicy.Verify(commandpolicy.DefaultConfigPath(), anchor.PlistAnchorReader{Converter: anchor.NewRealPlistToJSON()})
	if err != nil {
		list = nil
	}
	return PolicyView{
		ExtraProtectedPaths: roots,
		DisableSnapshot:     append([]string(nil), cfg.DisableSnapshot...),
		SnapshotPrefix:      cfg.SnapshotPrefix,
		CommandAllowlist:    list,
	}
}

// IsProtected returns whether a canonical path is protected by this invocation
// policy. The caller owns path normalization because it knows the guest cwd.
func (p PolicyView) IsProtected(path string) bool {
	return tcc.MatchesRoots(path, p.ExtraProtectedPaths)
}

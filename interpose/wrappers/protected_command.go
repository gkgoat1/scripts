package wrappers

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/gkgoat1/scripts/interpose/core"
)

// ProtectedCommand blocks unallowlisted process-control and AppleScript
// invocations unless a person confirms the operation at a terminal. The
// allowlist is selected by the invocation boundary; sandbox callers supply a
// committed PolicyView instead of loading host configuration here.
type ProtectedCommand struct{ CommandName string }

func (p ProtectedCommand) Name() string { return p.CommandName }
func (ProtectedCommand) Transform(_ *core.Context, args []string) ([]string, error) {
	// Do not honor --no-interpose here: bypassing a destructive-command guard
	// would defeat the purpose of installing it.
	return args, nil
}
func (p ProtectedCommand) Before(ctx *core.Context) error {
	if allows(ctx.Policy.CommandAllowlist, p.CommandName, ctx.Args) {
		return nil
	}
	fmt.Fprintf(ctx.Ops.Stderr(), "[interpose] %s: arguments are not allowlisted\n", p.CommandName)
	return core.ConfirmPIN(ctx, "Type this 6-digit confirmation PIN to continue")
}
func (ProtectedCommand) After(_ *core.Context, _ error) error { return nil }

func newConfirmationPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func readPIN(reader interface{ ReadString(byte) (string, error) }) (string, error) {
	value, err := reader.ReadString('\n')
	return strings.TrimRight(value, "\r\n"), err
}

func allows(list map[string][][]string, name string, args []string) bool {
	for _, rule := range list[name] {
		if len(rule) != len(args) {
			continue
		}
		match := true
		for i, want := range rule {
			if want == "{pid}" {
				if !isPID(args[i]) {
					match = false
					break
				}
			} else if want != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
func isPID(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

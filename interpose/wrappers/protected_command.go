package wrappers

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/gkgoat1/scripts/commitment/anchor"
	"github.com/gkgoat1/scripts/interpose/core"
	commandpolicy "github.com/gkgoat1/scripts/interpose/policy/command"
)

// ProtectedCommand blocks unallowlisted process-control and AppleScript
// invocations unless a person confirms the operation twice at a terminal.
type ProtectedCommand struct {
	CommandName string
}

func (p ProtectedCommand) Name() string { return p.CommandName }

func (ProtectedCommand) Transform(_ *core.Context, args []string) ([]string, error) {
	// Do not honor --no-interpose here: bypassing a destructive-command guard
	// would defeat the purpose of installing it.
	return args, nil
}

func (p ProtectedCommand) Before(ctx *core.Context) error {
	list, err := commandpolicy.Verify(commandpolicy.DefaultConfigPath(), anchor.PlistAnchorReader{
		Converter: anchor.NewRealPlistToJSON(),
	})
	if err == nil && list.Allows(p.CommandName, ctx.Args) {
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[interpose] %s: allowlist unavailable or uncommitted: %v\n", p.CommandName, err)
	} else {
		fmt.Fprintf(os.Stderr, "[interpose] %s: arguments are not allowlisted\n", p.CommandName)
	}
	return confirmSixCharacterPIN(os.Stderr)
}

func (ProtectedCommand) After(_ *core.Context, _ error) error { return nil }

// confirmSixCharacterPIN emits a fresh random challenge rather than accepting
// a user-selected value. This prevents a program that endlessly writes one
// fixed string to the terminal from satisfying the confirmation. It is a
// lightweight human-presence check, not a CAPTCHA or a cryptographic
// authentication mechanism. Reading from /dev/tty prevents a non-interactive
// program from satisfying the prompt through a stdin pipe.
func confirmSixCharacterPIN(out io.Writer) error {
	pin, err := newConfirmationPIN()
	if err != nil {
		return fmt.Errorf("operation denied: generate confirmation PIN: %w", err)
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("operation denied: cannot request PIN without a controlling terminal")
	}
	defer tty.Close()

	reader := bufio.NewReader(tty)
	fmt.Fprintf(out, "Type this 6-digit confirmation PIN to continue: %s\nPIN: ", pin)
	entered, err := readPIN(reader)
	if err != nil {
		return fmt.Errorf("operation denied: read confirmation PIN: %w", err)
	}
	if entered != pin {
		return fmt.Errorf("operation denied: confirmation PIN did not match")
	}
	return nil
}

// newConfirmationPIN returns a uniformly random six-digit challenge. Leading
// zeroes are retained so every prompt has the same fixed width.
func newConfirmationPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func readPIN(reader *bufio.Reader) (string, error) {
	value, err := reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return "", err
	}
	return strings.TrimRight(value, "\r\n"), nil
}

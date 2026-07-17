package wrappers

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

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

// confirmSixCharacterPIN is deliberately a human-presence confirmation, not
// a stored-password check: the user chooses a six-character value, then must
// type exactly the same value again. Reading from /dev/tty prevents a
// non-interactive program from satisfying the prompt through a stdin pipe.
func confirmSixCharacterPIN(out io.Writer) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("operation denied: cannot request PIN without a controlling terminal")
	}
	defer tty.Close()

	reader := bufio.NewReader(tty)
	fmt.Fprint(out, "Enter a 6-character confirmation PIN to continue: ")
	first, err := readPIN(reader)
	if err != nil {
		return fmt.Errorf("operation denied: read confirmation PIN: %w", err)
	}
	fmt.Fprint(out, "Repeat the same 6-character PIN: ")
	second, err := readPIN(reader)
	if err != nil {
		return fmt.Errorf("operation denied: read confirmation PIN: %w", err)
	}
	if utf8.RuneCountInString(first) != 6 || first != second {
		return fmt.Errorf("operation denied: PINs must match and contain exactly 6 characters")
	}
	return nil
}

func readPIN(reader *bufio.Reader) (string, error) {
	value, err := reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return "", err
	}
	return strings.TrimRight(value, "\r\n"), nil
}

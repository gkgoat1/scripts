package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Context carries invocation state and the operations through which a wrapper
// performs external effects. Wrappers must not invoke host process APIs
// directly: a sandbox operation backend can perform the same bounded action in
// a guest instead.
type Context struct {
	Name       string
	Args       []string
	RealBinary string
	Dir        string
	Env        []string
	Ops        Operations
	Policy     PolicyView
}

// PolicyView contains policy already selected and verified by the invocation
// boundary. Host callers may construct it from legacy host config; sandbox
// callers must construct it from committed sandbox policy.
type PolicyView struct {
	ExtraProtectedPaths []string
	DisableSnapshot     []string
	SnapshotPrefix      string
	CommandAllowlist    map[string][][]string
}

// SnapshotsDisabled reports whether repo is under a configured disabled prefix.
func (p PolicyView) SnapshotsDisabled(repo string) bool {
	for _, prefix := range p.DisableSnapshot {
		if prefix != "" && len(repo) >= len(prefix) && repo[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// Command is an executable-path command request. Path is always explicit; the
// operations implementation must never search PATH or invoke a shell.
type Command struct {
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Result describes a completed command. A non-zero child exit is represented
// by ExitCode rather than an infrastructure error.
type Result struct{ ExitCode int }

// Operations is the sole external-effects boundary for wrappers. The current
// HostOperations implementation preserves normal CLI operation. A sandbox
// RemoteGuestOperations implementation will fulfill the same typed requests
// through the macOS exec protocol without making sandboxd a command executor.
type Operations interface {
	Run(ctx context.Context, command Command) (Result, error)
	ReadFile(ctx context.Context, path string) ([]byte, error)
	ConfirmPIN(ctx context.Context, prompt string) error
	Stderr() io.Writer
}

// HostOperations executes explicitly addressed programs in the invoking host
// process realm. It is constructed only by the CLI-facing NewContext factory.
type HostOperations struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

func NewHostOperations(stdin io.Reader, stdout, stderr io.Writer) *HostOperations {
	return &HostOperations{stdin: stdin, stdout: stdout, stderr: stderr}
}
func (o *HostOperations) Stderr() io.Writer { return o.stderr }
func (o *HostOperations) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (o *HostOperations) ConfirmPIN(_ context.Context, prompt string) error {
	pin, err := newConfirmationPIN()
	if err != nil {
		return fmt.Errorf("operation denied: generate confirmation PIN: %w", err)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("operation denied: cannot request PIN without a controlling terminal")
	}
	defer tty.Close()
	fmt.Fprintf(o.stderr, "%s: %s\nPIN: ", prompt, pin)
	value, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil {
		return fmt.Errorf("operation denied: read confirmation PIN: %w", err)
	}
	if strings.TrimRight(value, "\r\n") != pin {
		return fmt.Errorf("operation denied: confirmation PIN did not match")
	}
	return nil
}

func newConfirmationPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (o *HostOperations) Run(ctx context.Context, spec Command) (Result, error) {
	if spec.Path == "" {
		return Result{}, fmt.Errorf("approved command has no executable path")
	}
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = spec.Stdin, spec.Stdout, spec.Stderr
	if cmd.Stdin == nil {
		cmd.Stdin = o.stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = o.stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = o.stderr
	}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if status, ok := ee.Sys().(syscall.WaitStatus); ok {
				return Result{ExitCode: status.ExitStatus()}, nil
			}
			return Result{ExitCode: 1}, nil
		}
		return Result{ExitCode: 1}, fmt.Errorf("exec %s: %w", spec.Path, err)
	}
	return Result{}, nil
}

// NewContext builds the compatibility host context used by the installed
// interposer binary. Sandbox callers must supply their own Operations instead
// of relying on this factory's process-derived values.
func NewContext(name string, args []string, realBinary string) *Context {
	dir, _ := os.Getwd()
	return &Context{
		Name: name, Args: append([]string(nil), args...), RealBinary: realBinary,
		Dir: dir, Env: append([]string(nil), os.Environ()...),
		Ops: NewHostOperations(os.Stdin, os.Stdout, os.Stderr), Policy: HostPolicyView(),
	}
}

func ReadFile(ctx *Context, path string) ([]byte, error) {
	if ctx == nil || ctx.Ops == nil {
		return nil, fmt.Errorf("interpose context has no operations")
	}
	return ctx.Ops.ReadFile(context.Background(), path)
}

func ConfirmPIN(ctx *Context, prompt string) error {
	if ctx == nil || ctx.Ops == nil {
		return fmt.Errorf("interpose context has no operations")
	}
	return ctx.Ops.ConfirmPIN(context.Background(), prompt)
}

// RunCommand dispatches a typed command through the active operation realm.
func RunCommand(ctx *Context, spec Command) (Result, error) {
	if ctx == nil || ctx.Ops == nil {
		return Result{}, fmt.Errorf("interpose context has no operations")
	}
	return ctx.Ops.Run(context.Background(), spec)
}

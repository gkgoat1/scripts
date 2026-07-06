package core

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Wrapper mutates argv and runs hooks around the real binary.
type Wrapper interface {
	Name() string
	Transform(ctx *Context, args []string) ([]string, error)
	Before(ctx *Context) error
	After(ctx *Context, runErr error) error
}

// Passthrough is a no-op Wrapper.
type Passthrough struct {
	CommandName string
}

func (p Passthrough) Name() string { return p.CommandName }

func (Passthrough) Transform(_ *Context, args []string) ([]string, error) {
	return args, nil
}

func (Passthrough) Before(_ *Context) error { return nil }

func (Passthrough) After(_ *Context, _ error) error { return nil }

// Run executes realBinary with args, forwarding stdio and returning exit code.
func Run(ctx *Context, args []string) (int, error) {
	cmd := exec.Command(ctx.RealBinary, args...)
	cmd.Dir = ctx.Dir
	cmd.Env = ctx.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if status, ok := ee.Sys().(syscall.WaitStatus); ok {
				return status.ExitStatus(), nil
			}
			return 1, nil
		}
		return 1, fmt.Errorf("exec %s: %w", ctx.RealBinary, err)
	}
	return 0, nil
}

// Execute resolves the real binary, runs wrapper hooks, and exits with the child code.
func Execute(w Wrapper, args []string) {
	real, err := ResolveRealBinary(w.Name())
	if err != nil {
		fmt.Fprintf(os.Stderr, "interpose: %v\n", err)
		os.Exit(127)
	}

	ctx := NewContext(w.Name(), args, real)
	transformed, err := w.Transform(ctx, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "interpose: %v\n", err)
		os.Exit(2)
	}
	ctx.Args = transformed

	if err := w.Before(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "interpose: %v\n", err)
		os.Exit(2)
	}

	code, err := Run(ctx, transformed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "interpose: %v\n", err)
		os.Exit(1)
	}
	_ = w.After(ctx, nil)
	os.Exit(code)
}

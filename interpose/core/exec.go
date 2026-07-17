package core

import (
	"fmt"
	"os"
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

// Run executes realBinary with args through the invocation's operation realm.
func Run(ctx *Context, args []string) (int, error) {
	result, err := RunCommand(ctx, Command{
		Path: ctx.RealBinary,
		Args: append([]string(nil), args...),
		Dir:  ctx.Dir,
		Env:  append([]string(nil), ctx.Env...),
	})
	return result.ExitCode, err
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

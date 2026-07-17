package core_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/interpose/core"
)

type recordingOps struct {
	command core.Command
}

func (o *recordingOps) Run(_ context.Context, command core.Command) (core.Result, error) {
	o.command = command
	return core.Result{ExitCode: 17}, nil
}
func (*recordingOps) ReadFile(context.Context, string) ([]byte, error) { return nil, nil }
func (*recordingOps) Stderr() io.Writer                                { return io.Discard }

func TestRunUsesContextOperations(t *testing.T) {
	ops := &recordingOps{}
	ctx := &core.Context{
		RealBinary: "/approved/tool",
		Dir:        "/guest/work",
		Env:        []string{"SAFE=1"},
		Ops:        ops,
	}
	code, err := core.Run(ctx, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	if code != 17 {
		t.Fatalf("exit code = %d, want 17", code)
	}
	if ops.command.Path != "/approved/tool" || ops.command.Dir != "/guest/work" || strings.Join(ops.command.Args, " ") != "one two" {
		t.Fatalf("operation command = %#v", ops.command)
	}
	if strings.Join(ops.command.Env, " ") != "SAFE=1" {
		t.Fatalf("operation environment = %#v", ops.command.Env)
	}
}

func TestRunWithoutOperationsFails(t *testing.T) {
	_, err := core.Run(&core.Context{RealBinary: "/approved/tool"}, nil)
	if err == nil {
		t.Fatal("Run accepted a context with no operations")
	}
}

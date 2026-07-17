package wrappers_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/wrappers"
)

type recordingGuestOps struct {
	commands []core.Command
}

func (o *recordingGuestOps) Run(_ context.Context, command core.Command) (core.Result, error) {
	o.commands = append(o.commands, command)
	joined := strings.Join(command.Args, " ")
	switch {
	case strings.Contains(joined, "--show-toplevel"):
		_, _ = io.WriteString(command.Stdout, "/guest/repo\n")
	case strings.Contains(joined, "--abbrev-ref"):
		_, _ = io.WriteString(command.Stdout, "main\n")
	case strings.Contains(joined, "--short"):
		_, _ = io.WriteString(command.Stdout, "deadbee\n")
	}
	return core.Result{}, nil
}
func (*recordingGuestOps) ReadFile(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("unexpected guest file read")
}
func (*recordingGuestOps) ConfirmPIN(context.Context, string) error { return nil }
func (*recordingGuestOps) Stderr() io.Writer                        { return io.Discard }

func TestGitSnapshotUsesContextOperations(t *testing.T) {
	ops := &recordingGuestOps{}
	ctx := &core.Context{
		Name:       "git",
		Args:       []string{"reset", "--hard"},
		RealBinary: "/guest/usr/bin/git",
		Dir:        "/guest/repo",
		Env:        []string{"HOME=/guest/home"},
		Ops:        ops,
		Policy:     core.PolicyView{SnapshotPrefix: "interpose/snapshot"},
	}
	if err := (wrappers.Git{}).Before(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ops.commands) != 4 {
		t.Fatalf("guest operation count = %d, want 4: %#v", len(ops.commands), ops.commands)
	}
	for _, command := range ops.commands {
		if command.Path != "/guest/usr/bin/git" || command.Dir != "/guest/repo" {
			t.Fatalf("host path/cwd leaked into guest request: %#v", command)
		}
	}
	if got := strings.Join(ops.commands[3].Args, " "); !strings.Contains(got, "branch interpose/snapshot/") {
		t.Fatalf("snapshot command = %q", got)
	}
}

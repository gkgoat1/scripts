package wrappers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gkgoat1/scripts/internal/restoreconflict"
	"github.com/gkgoat1/scripts/interpose/core"
)

// Git wraps git with snapshot branches before destructive operations and
// restores files containing conflict markers before mutating commands.
type Git struct{}

func (Git) Name() string { return "git" }

func (Git) Transform(ctx *core.Context, args []string) ([]string, error) {
	out, _ := core.StripNoInterpose(args)
	return out, nil
}

func (Git) Before(ctx *core.Context) error {
	if core.HasFlag(ctx.Args, "--no-interpose") {
		return nil
	}
	needsRestore := gitRestoreTrigger(ctx.Args)
	needsSnapshot := gitDestructive(ctx.Args)
	if !needsRestore && !needsSnapshot {
		return nil
	}
	repo, err := gitRepoRoot(ctx, ctx.Args)
	if err != nil || repo == "" {
		return nil
	}
	if ctx.Policy.SnapshotsDisabled(repo) {
		return nil
	}
	if needsRestore {
		if rerr := restoreConflicts(ctx, repo); rerr != nil {
			fmt.Fprintf(ctx.Ops.Stderr(), "[interpose] restore conflict warning: %v\n", rerr)
		}
	}
	if needsSnapshot {
		return gitSnapshot(ctx, repo)
	}
	return nil
}

func (Git) After(_ *core.Context, _ error) error { return nil }

// GitRestoreTrigger reports whether the git subcommand should trigger
// automatic conflict restoration.
func GitRestoreTrigger(args []string) bool { return gitRestoreTrigger(args) }

func gitRestoreTrigger(args []string) bool {
	sub := core.Subcommand(args)
	switch sub {
	case "add", "commit", "merge", "rebase", "cherry-pick", "pull":
		return true
	}
	return false
}

func gitDestructive(args []string) bool {
	sub := core.Subcommand(args)
	switch sub {
	case "reset":
		return true
	case "checkout", "switch":
		return core.HasFlag(args, "-f") || core.HasFlag(args, "--force")
	case "restore":
		return core.HasFlag(args, "--source") || core.HasFlag(args, "--staged") ||
			(core.HasFlag(args, "--worktree") && len(pathspecArgs(args, "restore")) > 0)
	case "push":
		return core.HasFlag(args, "--force") || core.HasFlag(args, "-f") ||
			core.HasFlag(args, "--force-with-lease")
	case "branch":
		return core.HasFlag(args, "-D")
	case "clean":
		return hasShortFlag(args, 'f')
	case "revert":
		return true
	default:
		return false
	}
}

func hasShortFlag(args []string, short rune) bool {
	for _, a := range args {
		if a == "-"+string(short) {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1 {
			for _, r := range a[1:] {
				if r == short {
					return true
				}
			}
		}
	}
	return false
}

func pathspecArgs(args []string, sub string) []string {
	found := false
	var paths []string
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if !found {
			if a == sub {
				found = true
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			if a == "-m" || a == "--message" || a == "-e" || a == "--edit" {
				skipNext = true
			}
			continue
		}
		paths = append(paths, a)
	}
	return paths
}

func gitRepoRoot(ctx *core.Context, args []string) (string, error) {
	gitArgs := []string{"rev-parse", "--show-toplevel"}
	gitArgs = prependGlobalGitArgs(args, gitArgs)
	var out bytes.Buffer
	_, err := core.RunCommand(ctx, core.Command{Path: ctx.RealBinary, Args: gitArgs, Dir: ctx.Dir, Env: ctx.Env, Stdout: &out, Stderr: ctx.Ops.Stderr()})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func prependGlobalGitArgs(args, tail []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-C":
			if i+1 < len(args) {
				out = append(out, "-C", args[i+1])
				i++
			}
		case "--git-dir":
			if i+1 < len(args) {
				out = append(out, "--git-dir", args[i+1])
				i++
			}
		case "--work-tree":
			if i+1 < len(args) {
				out = append(out, "--work-tree", args[i+1])
				i++
			}
		}
	}
	return append(out, tail...)
}

func gitSnapshot(ctx *core.Context, repo string) error {
	branch, err := gitOutput(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil
	}
	if branch == "HEAD" {
		branch = "detached"
	}
	short, err := gitOutput(ctx, repo, "rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102-150405")
	prefix := ctx.Policy.SnapshotPrefix
	if prefix == "" {
		prefix = restoreconflict.DefaultSnapshotPrefix
	}
	name := fmt.Sprintf("%s/%s_%s_%s", prefix, ts, sanitizeBranch(branch), short)
	_, err = core.RunCommand(ctx, core.Command{Path: ctx.RealBinary, Args: []string{"-C", repo, "branch", name, "HEAD"}, Dir: ctx.Dir, Env: ctx.Env, Stderr: ctx.Ops.Stderr()})
	if err != nil {
		return fmt.Errorf("snapshot branch: %w", err)
	}
	fmt.Fprintf(ctx.Ops.Stderr(), "[interpose] snapshot: %s\n", name)
	return nil
}

func gitOutput(ctx *core.Context, repo string, args ...string) (string, error) {
	all := append([]string{"-C", repo}, args...)
	var out bytes.Buffer
	_, err := core.RunCommand(ctx, core.Command{Path: ctx.RealBinary, Args: all, Dir: ctx.Dir, Env: ctx.Env, Stdout: &out, Stderr: ctx.Ops.Stderr()})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func sanitizeBranch(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, name)
	if name == "" {
		return "branch"
	}
	return name
}

type gitRunner struct{ ctx *core.Context }

func (r gitRunner) Run(ctx context.Context, command core.Command) (core.Result, error) {
	return r.ctx.Ops.Run(ctx, command)
}
func (r gitRunner) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return r.ctx.Ops.ReadFile(ctx, path)
}
func (r gitRunner) Stderr() io.Writer { return r.ctx.Ops.Stderr() }

func restoreConflicts(ctx *core.Context, repo string) error {
	prefix := ctx.Policy.SnapshotPrefix
	if prefix == "" {
		prefix = restoreconflict.DefaultSnapshotPrefix
	}
	return restoreconflict.Restore(repo, restoreconflict.Options{
		Git:    ctx.RealBinary,
		Prefix: prefix,
		Runner: gitRunner{ctx: ctx},
	})
}

// GitDestructive exposes destructive detection for tests.
func GitDestructive(args []string) bool { return gitDestructive(args) }

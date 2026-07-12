package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenizeShellBasic(t *testing.T) {
	tokens, err := tokenizeShell("FOO=bar BAZ='hello world' /bin/echo hi")
	if err != nil {
		t.Fatalf("tokenizeShell: %v", err)
	}
	want := []string{"FOO=bar", "BAZ=hello world", "/bin/echo", "hi"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Errorf("tokens[%d] = %q, want %q", i, tokens[i], want[i])
		}
	}
}

func TestTokenizeShellQuotedValueWithColonsAndSlashes(t *testing.T) {
	tokens, err := tokenizeShell(`DEVROULETTE_URL='wss://devroulette-production.up.railway.app'`)
	if err != nil {
		t.Fatalf("tokenizeShell: %v", err)
	}
	if len(tokens) != 1 || tokens[0] != "DEVROULETTE_URL=wss://devroulette-production.up.railway.app" {
		t.Errorf("tokens = %v", tokens)
	}
}

func TestTokenizeShellUnterminatedQuoteErrors(t *testing.T) {
	if _, err := tokenizeShell(`echo 'unterminated`); err == nil {
		t.Error("want error for unterminated quote")
	}
}

func TestStripEnvPrefixMultiple(t *testing.T) {
	tokens := []string{"A=1", "B=2", "/bin/echo", "hi"}
	got := stripEnvPrefix(tokens)
	if len(got) != 2 || got[0] != "/bin/echo" || got[1] != "hi" {
		t.Errorf("stripEnvPrefix = %v", got)
	}
}

func TestStripEnvPrefixNone(t *testing.T) {
	tokens := []string{"/bin/echo", "hi"}
	got := stripEnvPrefix(tokens)
	if len(got) != 2 {
		t.Errorf("stripEnvPrefix = %v", got)
	}
}

func TestStripEnvPrefixAllEnvNoCommand(t *testing.T) {
	tokens := []string{"A=1", "B=2"}
	got := stripEnvPrefix(tokens)
	if len(got) != 0 {
		t.Errorf("stripEnvPrefix = %v, want empty", got)
	}
}

func TestResolveCommandTokenAbsolutePathExists(t *testing.T) {
	pc := newFakePathChecker()
	pc.setExists("/opt/homebrew/bin/node")
	pr := newFakePathResolver()

	ok, detail := ResolveCommandToken("/opt/homebrew/bin/node", "/base", pc, pr)
	if !ok {
		t.Errorf("ok = false, detail = %q", detail)
	}
}

func TestResolveCommandTokenAbsolutePathMissing(t *testing.T) {
	pc := newFakePathChecker()
	pr := newFakePathResolver()

	ok, detail := ResolveCommandToken("/opt/homebrew/Cellar/node/26.3.0/bin/node", "/base", pc, pr)
	if ok {
		t.Error("ok = true, want false for missing path")
	}
	if !strings.Contains(detail, "not found") {
		t.Errorf("detail = %q", detail)
	}
}

func TestResolveCommandTokenRelativePathResolvedAgainstBaseDir(t *testing.T) {
	pc := newFakePathChecker()
	pc.setExists(filepath.Join("/base", "./Codex Computer Use.app/bin/SkyComputerUseClient"))
	pr := newFakePathResolver()

	ok, _ := ResolveCommandToken("./Codex Computer Use.app/bin/SkyComputerUseClient", "/base", pc, pr)
	if !ok {
		t.Error("want relative path with embedded spaces resolved as ONE token against baseDir")
	}
}

func TestResolveCommandTokenBareNamePathHit(t *testing.T) {
	pc := newFakePathChecker()
	pr := newFakePathResolver()
	pr.setResolves("node", "/opt/homebrew/bin/node")

	ok, _ := ResolveCommandToken("node", "/base", pc, pr)
	if !ok {
		t.Error("want bare command to resolve via PATH")
	}
}

func TestResolveCommandTokenBareNamePathMiss(t *testing.T) {
	pc := newFakePathChecker()
	pr := newFakePathResolver()

	ok, detail := ResolveCommandToken("nonexistent-tool", "/base", pc, pr)
	if ok {
		t.Error("want false for unresolvable bare command")
	}
	if !strings.Contains(detail, "PATH") {
		t.Errorf("detail = %q", detail)
	}
}

func TestResolveCommandTokenEmpty(t *testing.T) {
	pc := newFakePathChecker()
	pr := newFakePathResolver()
	ok, _ := ResolveCommandToken("", "/base", pc, pr)
	if ok {
		t.Error("want false for empty token")
	}
}

func TestResolveCommandRealDevroulettExample(t *testing.T) {
	raw := `DEVROULETTE_HOOK=1 DEVROULETTE_URL='wss://devroulette-production.up.railway.app' DEVROULETTE_TERMINAL='iterm' /opt/homebrew/Cellar/node/26.3.0/bin/node /opt/homebrew/lib/node_modules/devroulette-cli/dist/cli/src/hook-runner.js start`
	pc := newFakePathChecker()
	// hook-runner.js exists, but the versioned node path does not (the real
	// scenario found on this machine after a Homebrew node upgrade).
	pc.setExists("/opt/homebrew/lib/node_modules/devroulette-cli/dist/cli/src/hook-runner.js")
	pr := newFakePathResolver()
	pr.setResolves("node", "/opt/homebrew/bin/node")

	ok, detail := ResolveCommand(raw, "/base", pc, pr)
	if ok {
		t.Error("want dangling: the exact versioned node path does not exist")
	}
	if !strings.Contains(detail, "/opt/homebrew/Cellar/node/26.3.0/bin/node") {
		t.Errorf("detail = %q, want it to name the dangling path", detail)
	}
}

func TestResolveCommandBareCommandNoEnvPrefix(t *testing.T) {
	pc := newFakePathChecker()
	pr := newFakePathResolver()
	pr.setResolves("rtk", "/opt/homebrew/bin/rtk")

	ok, _ := ResolveCommand("rtk hook claude", "/base", pc, pr)
	if !ok {
		t.Error("want bare `rtk hook claude` to resolve via PATH")
	}
}

func TestResolveCommandEmptyAfterEnvStripping(t *testing.T) {
	pc := newFakePathChecker()
	pr := newFakePathResolver()

	ok, detail := ResolveCommand("A=1 B=2", "/base", pc, pr)
	if ok {
		t.Error("want false when nothing remains after stripping env assignments")
	}
	if !strings.Contains(detail, "empty command") {
		t.Errorf("detail = %q", detail)
	}
}

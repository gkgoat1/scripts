package main

import "testing"

func TestRealInstalledCheckerClaudeCodeUsesPath(t *testing.T) {
	pr := newFakePathResolver()
	pc := newFakePathChecker()
	pr.setResolves("claude", "/opt/homebrew/bin/claude")

	ic := newRealInstalledChecker(pr, pc)
	if !ic.ClaudeCodeInstalled() {
		t.Error("want ClaudeCodeInstalled true when `claude` resolves via PATH")
	}
}

func TestRealInstalledCheckerClaudeCodeNotOnPath(t *testing.T) {
	ic := newRealInstalledChecker(newFakePathResolver(), newFakePathChecker())
	if ic.ClaudeCodeInstalled() {
		t.Error("want ClaudeCodeInstalled false when `claude` does not resolve")
	}
}

func TestRealInstalledCheckerCodexUsesPath(t *testing.T) {
	pr := newFakePathResolver()
	pr.setResolves("codex", "/opt/homebrew/bin/codex")
	ic := newRealInstalledChecker(pr, newFakePathChecker())
	if !ic.CodexInstalled() {
		t.Error("want CodexInstalled true when `codex` resolves via PATH")
	}
}

func TestRealInstalledCheckerPiUsesPath(t *testing.T) {
	pr := newFakePathResolver()
	pr.setResolves("pi", "/opt/homebrew/bin/pi")
	ic := newRealInstalledChecker(pr, newFakePathChecker())
	if !ic.PiInstalled() {
		t.Error("want PiInstalled true when `pi` resolves via PATH")
	}
}

func TestRealInstalledCheckerCursorUsesAppBundle(t *testing.T) {
	pc := newFakePathChecker()
	pc.setExists("/Applications/Cursor.app")
	ic := newRealInstalledChecker(newFakePathResolver(), pc)
	if !ic.CursorInstalled() {
		t.Error("want CursorInstalled true when /Applications/Cursor.app exists")
	}
}

func TestRealInstalledCheckerCursorMissingAppBundle(t *testing.T) {
	ic := newRealInstalledChecker(newFakePathResolver(), newFakePathChecker())
	if ic.CursorInstalled() {
		t.Error("want CursorInstalled false when /Applications/Cursor.app is absent")
	}
}

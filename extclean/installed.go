package main

// InstalledChecker reports whether each agent's binary/app is installed at
// all on this system, used for the "orphaned whole config file" criterion:
// if a tool isn't installed but its config file still exists, every entry
// in that file is a stale leftover.
type InstalledChecker interface {
	ClaudeCodeInstalled() bool
	CursorInstalled() bool
	CodexInstalled() bool
	PiInstalled() bool
}

// realInstalledChecker resolves each CLI agent via PATH, except Cursor
// (a GUI app with no reliable PATH binary), which is checked by the
// presence of its .app bundle. A non-PATH install of Claude Code/Codex/Pi
// would false-flag as "not installed" -- a known v1 limitation, documented
// in docs/extclean.md rather than silently glossed over.
type realInstalledChecker struct {
	pr PathResolver
	pc PathChecker
}

func newRealInstalledChecker(pr PathResolver, pc PathChecker) realInstalledChecker {
	return realInstalledChecker{pr: pr, pc: pc}
}

func (r realInstalledChecker) ClaudeCodeInstalled() bool {
	_, err := r.pr.LookPath("claude")
	return err == nil
}

func (r realInstalledChecker) CursorInstalled() bool {
	return r.pc.Exists("/Applications/Cursor.app")
}

func (r realInstalledChecker) CodexInstalled() bool {
	_, err := r.pr.LookPath("codex")
	return err == nil
}

func (r realInstalledChecker) PiInstalled() bool {
	_, err := r.pr.LookPath("pi")
	return err == nil
}

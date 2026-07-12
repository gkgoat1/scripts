package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// tokenizeShell splits a shell command string into words, honoring single
// and double quotes and backslash escapes (double-quote/unquoted context
// only; single quotes are fully literal, matching POSIX sh).
func tokenizeShell(s string) ([]string, error) {
	var tokens []string
	var cur []rune
	haveToken := false
	var quote rune
	escaped := false

	flush := func() {
		if haveToken {
			tokens = append(tokens, string(cur))
			cur = nil
			haveToken = false
		}
	}

	for _, r := range s {
		if escaped {
			cur = append(cur, r)
			escaped = false
			haveToken = true
			continue
		}
		switch {
		case quote != 0:
			switch {
			case r == quote:
				quote = 0
			case r == '\\' && quote == '"':
				escaped = true
			default:
				cur = append(cur, r)
			}
		case r == '\'' || r == '"':
			quote = r
			haveToken = true
		case r == '\\':
			escaped = true
			haveToken = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur = append(cur, r)
			haveToken = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in command: %q", s)
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash in command: %q", s)
	}
	flush()
	return tokens, nil
}

// stripEnvPrefix drops leading KEY=value assignment tokens, returning the
// remaining tokens starting at the actual command.
func stripEnvPrefix(tokens []string) []string {
	i := 0
	for i < len(tokens) && envAssignRe.MatchString(tokens[i]) {
		i++
	}
	return tokens[i:]
}

// ResolveCommandToken checks whether a single, already-split command token
// resolves: absolute/relative paths (resolved against baseDir if relative)
// are checked for existence; bare names are resolved via PATH.
func ResolveCommandToken(token, baseDir string, pc PathChecker, pr PathResolver) (ok bool, detail string) {
	if token == "" {
		return false, "empty command token"
	}
	if strings.ContainsRune(token, '/') {
		path := token
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		if pc.Exists(path) {
			return true, fmt.Sprintf("path exists: %s", path)
		}
		return false, fmt.Sprintf("command not found: %s (path does not exist)", path)
	}
	if resolved, err := pr.LookPath(token); err == nil {
		return true, fmt.Sprintf("resolved via PATH: %s -> %s", token, resolved)
	}
	return false, fmt.Sprintf("command not found: %s (not resolvable via PATH)", token)
}

// ResolveCommand checks a shell command string (possibly with leading
// KEY=value env assignments) by tokenizing it and resolving the first
// remaining token.
func ResolveCommand(raw, baseDir string, pc PathChecker, pr PathResolver) (ok bool, detail string) {
	tokens, err := tokenizeShell(raw)
	if err != nil {
		return false, err.Error()
	}
	cmdTokens := stripEnvPrefix(tokens)
	if len(cmdTokens) == 0 {
		return false, "empty command after stripping env assignments"
	}
	ok, detail = ResolveCommandToken(cmdTokens[0], baseDir, pc, pr)
	return ok, fmt.Sprintf("command %q: %s", cmdTokens[0], detail)
}

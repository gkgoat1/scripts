package main

import "testing"

func TestGithubRepoSlug(t *testing.T) {
	cases := []struct {
		url    string
		want   string
		wantOK bool
	}{
		{"git@github.com:owner/repo.git", "owner/repo", true},
		{"git@github.com:owner/repo", "owner/repo", true},
		{"ssh://git@github.com/owner/repo.git", "owner/repo", true},
		{"https://github.com/owner/repo.git", "owner/repo", true},
		{"https://github.com/owner/repo", "owner/repo", true},
		{"http://github.com/owner/repo", "owner/repo", true},
		{"git@gitlab.com:owner/repo.git", "", false},
		{"https://example.com/owner/repo", "", false},
		{"/local/path/to/repo", "", false},
		{"git@github.com:owner", "", false},
		{"git@github.com:", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, gotOK := githubRepoSlug(c.url)
		if got != c.want || gotOK != c.wantOK {
			t.Errorf("githubRepoSlug(%q) = (%q, %v), want (%q, %v)", c.url, got, gotOK, c.want, c.wantOK)
		}
	}
}

func TestPRBranchNameAndMatch(t *testing.T) {
	cases := []struct {
		base string
		n    int
	}{
		{"main", 1},
		{"main", 42},
		{"release-1.2", 3},
		{"feature/foo", 7},
	}
	for _, c := range cases {
		name := prBranchName(c.base, c.n)
		gotN, ok := matchPRBranch(name, c.base)
		if !ok || gotN != c.n {
			t.Errorf("matchPRBranch(%q, %q) = (%d, %v), want (%d, true)", name, c.base, gotN, ok, c.n)
		}
	}
}

func TestMatchPRBranchRejects(t *testing.T) {
	cases := []struct {
		name, base string
	}{
		{"main", "main"},                       // not our prefix at all
		{"gitall-pr/main-", "main"},            // no number
		{"gitall-pr/main-x", "main"},           // non-numeric
		{"gitall-pr/main-0", "main"},           // non-positive
		{"gitall-pr/main--1", "main"},          // negative
		{"gitall-pr/other-1", "main"},          // wrong base
		{"feature/gitall-pr/main-1", "main"},   // prefix not at start
		{"gitall-pr/release-1.2-3", "release"}, // base doesn't match exactly ("release" vs "release-1.2")
	}
	for _, c := range cases {
		if _, ok := matchPRBranch(c.name, c.base); ok {
			t.Errorf("matchPRBranch(%q, %q) unexpectedly matched", c.name, c.base)
		}
	}
}

func TestMatchPRBranchAmbiguousBase(t *testing.T) {
	// A base containing '-' must still round-trip correctly: the trailing
	// -<N> is what's stripped, not the first '-'.
	name := prBranchName("release-1.2", 5)
	if n, ok := matchPRBranch(name, "release-1.2"); !ok || n != 5 {
		t.Errorf("matchPRBranch(%q, %q) = (%d, %v), want (5, true)", name, "release-1.2", n, ok)
	}
	// Matching against the wrong (truncated) base must fail even though it's
	// a textual prefix of the real base.
	if _, ok := matchPRBranch(name, "release"); ok {
		t.Errorf("matchPRBranch(%q, %q) unexpectedly matched truncated base", name, "release")
	}
}

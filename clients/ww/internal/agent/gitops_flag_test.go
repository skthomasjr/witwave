// Tests for the pure-helper functions in gitops_flag.go and the
// closely-related splitURLBranch helper in gitsync_flags.go that
// the `ww agent create --gitsync-bundle` flag handler composes
// over user input. Mirrors the table-driven shape used in
// internal/agent/backend_spec_test.go and cmd/snapshot_test.go;
// the CR-construction (ExpandGitOps) path is exercised by tier-4+
// fixture work in a future sweep, but the user-facing parsing
// contract is pinned here so a future tweak to the last-colon or
// last-at splitting rules can't silently misroute SSH URLs.
package agent

import (
	"strings"
	"testing"
)

// TestParseGitOps pins the `--gitsync-bundle` flag parser. Contract:
//   - empty input          → zero-spec, no error (callers detect "not set")
//   - no colon             → error "form is …"
//   - empty url half       → error "empty URL"
//   - empty path half      → error "empty repo-path"
//   - last-colon splits url-and-branch from repo-path so SSH urls
//     (`git@host:owner/repo.git`) retain their host colon
//   - `@branch` suffix on url-and-branch is recognised by splitURLBranch
//
// Drift in the last-colon strategy would break either HTTPS+branch
// (would over-split) or SSH+branch (would under-split). Pin every
// shape explicitly.
func TestParseGitOps(t *testing.T) {
	t.Run("empty input returns zero spec no error", func(t *testing.T) {
		got, err := ParseGitOps("")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != (GitOpsFlagSpec{}) {
			t.Errorf("got = %+v, want zero", got)
		}
	})

	t.Run("whitespace-only input returns zero spec", func(t *testing.T) {
		got, err := ParseGitOps("   ")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != (GitOpsFlagSpec{}) {
			t.Errorf("got = %+v, want zero", got)
		}
	})

	t.Run("https no branch", func(t *testing.T) {
		got, err := ParseGitOps("https://github.com/org/repo.git:.agents/self/iris")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.URL != "https://github.com/org/repo.git" {
			t.Errorf("URL = %q", got.URL)
		}
		if got.Branch != "" {
			t.Errorf("Branch = %q, want empty", got.Branch)
		}
		if got.RepoPath != ".agents/self/iris" {
			t.Errorf("RepoPath = %q", got.RepoPath)
		}
	})

	t.Run("https with branch", func(t *testing.T) {
		got, err := ParseGitOps("https://github.com/org/repo.git@main:.agents/self/iris")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.URL != "https://github.com/org/repo.git" || got.Branch != "main" {
			t.Errorf("got = %+v", got)
		}
	})

	t.Run("ssh url with branch (SSH host-colon retained)", func(t *testing.T) {
		// `git@github.com:org/repo.git` contains a colon AS THE HOST
		// SEPARATOR; the parser's last-colon-wins strategy must pin
		// the path on the right of the LAST colon (`.agents/self/iris`),
		// keeping `git@github.com:org/repo.git` intact on the left.
		got, err := ParseGitOps("git@github.com:org/repo.git@main:.agents/self/iris")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.URL != "git@github.com:org/repo.git" {
			t.Errorf("URL = %q, want git@host:org/repo.git", got.URL)
		}
		if got.Branch != "main" {
			t.Errorf("Branch = %q, want main", got.Branch)
		}
		if got.RepoPath != ".agents/self/iris" {
			t.Errorf("RepoPath = %q", got.RepoPath)
		}
	})

	t.Run("ssh url no branch keeps host-colon intact", func(t *testing.T) {
		got, err := ParseGitOps("git@github.com:org/repo.git:.agents/self/iris")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.URL != "git@github.com:org/repo.git" {
			t.Errorf("URL = %q", got.URL)
		}
		if got.Branch != "" {
			t.Errorf("Branch = %q, want empty (no @branch)", got.Branch)
		}
	})

	t.Run("surrounding whitespace trimmed on each half", func(t *testing.T) {
		got, err := ParseGitOps("  https://github.com/org/repo.git@main  :  .agents/self/iris  ")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.URL != "https://github.com/org/repo.git" || got.Branch != "main" || got.RepoPath != ".agents/self/iris" {
			t.Errorf("got = %+v", got)
		}
	})

	errCases := []struct {
		name string
		raw  string
		want string
	}{
		{"no colon at all", "github.com-org-repo.git", "form is"},
		{"trailing colon empty path", "https://github.com/org/repo.git:", "empty repo-path"},
		{"leading colon empty url", ":.agents/self/iris", "empty URL"},
		{"whitespace-only path after colon", "https://github.com/org/repo.git:   ", "empty repo-path"},
		{"whitespace-only url before colon", "    :.agents/self/iris", "empty URL"},
	}
	for _, tc := range errCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseGitOps(tc.raw)
			if err == nil {
				t.Fatalf("err = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// TestSplitURLBranch pins the SSH-aware @-splitter that powers
// ParseGitOps and ParseGitSyncs. Contract:
//   - no `@`                   → (input, "")
//   - last `@` segment empty   → (input, "")     (trailing `@` doesn't split)
//   - last `@` segment has `/` → (input, "")     (`/` indicates path, not branch)
//   - last `@` segment has `:` → (input, "")     (`:` indicates SSH host)
//   - last `@` segment plain   → (left, right)
//
// The branch-detection rule is "last `@`, plus the suffix must be a
// plain identifier" — so `git@host:owner/repo.git` does NOT register
// the `git@host` as a branch split because `host:owner/...` contains
// `:` and `/`. Pin every branch.
func TestSplitURLBranch(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantURL    string
		wantBranch string
	}{
		{"no at returns input verbatim", "https://github.com/org/repo.git", "https://github.com/org/repo.git", ""},
		{"https with simple branch", "https://github.com/org/repo.git@main", "https://github.com/org/repo.git", "main"},
		{"https with branch containing dash and digit", "https://github.com/org/repo.git@release-1.2", "https://github.com/org/repo.git", "release-1.2"},
		{"trailing at sign with empty suffix returns input", "https://github.com/org/repo.git@", "https://github.com/org/repo.git@", ""},
		{"ssh-style url no branch keeps colon-containing suffix intact", "git@github.com:org/repo.git", "git@github.com:org/repo.git", ""},
		{"ssh-style with explicit branch suffix splits at last at", "git@github.com:org/repo.git@main", "git@github.com:org/repo.git", "main"},
		{"slash in suffix (could be a path segment) does NOT split", "https://github.com/org/repo.git@feature/foo", "https://github.com/org/repo.git@feature/foo", ""},
		{"empty input returns empty empty", "", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotBranch := splitURLBranch(tc.in)
			if gotURL != tc.wantURL || gotBranch != tc.wantBranch {
				t.Errorf("splitURLBranch(%q) = (%q, %q), want (%q, %q)",
					tc.in, gotURL, gotBranch, tc.wantURL, tc.wantBranch)
			}
		})
	}
}

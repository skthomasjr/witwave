package agent

import "testing"

func TestDeriveGitSyncName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		repo string
		want string
	}{
		// Common shapes — the headline UX.
		{"skthomasjr/witwave-test", "witwave-test"},
		{"github.com/org/repo", "repo"},
		{"https://github.com/org/my-repo", "my-repo"},
		{"https://github.com/org/my-repo.git", "my-repo"},
		{"git@github.com:org/my-repo.git", "my-repo"},

		// Sanitisation: DNS-1123 requires lowercase alphanumerics + hyphen,
		// starting and ending with alphanumeric.
		{"org/My.Repo", "my-repo"},
		{"org/snake_case", "snake-case"},
		{"org/UPPERCASE", "uppercase"},
		{"org/dots.and_underscores", "dots-and-underscores"},
		{"org/-leading-hyphen", "leading-hyphen"},
		{"org/trailing-hyphen-", "trailing-hyphen"},
		{"org/multi---hyphen", "multi-hyphen"}, // collapsed

		// Edge cases that exercise the fallback.
		{"", FallbackGitSyncName},
		{"ftp://bad-scheme/foo", FallbackGitSyncName}, // parseRepoRef rejects → fallback
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.repo, func(t *testing.T) {
			t.Parallel()
			if got := DeriveGitSyncName(tc.repo); got != tc.want {
				t.Errorf("DeriveGitSyncName(%q) = %q; want %q", tc.repo, got, tc.want)
			}
		})
	}
}

// TestDeriveGitSyncName_DNS1123Compliance sanity-checks that every
// output from Derive passes the same DNS-1123 validator ValidateName
// uses. Without this guarantee, a sanitised repo name could trip CRD
// validation at CR-apply time.
func TestDeriveGitSyncName_DNS1123Compliance(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{
		"skthomasjr/witwave-test",
		"org/My.Repo",
		"org/snake_case",
		"org/UPPERCASE",
		"org/dots.and_underscores",
	} {
		name := DeriveGitSyncName(repo)
		if err := ValidateName(name); err != nil {
			t.Errorf("DeriveGitSyncName(%q) = %q which fails ValidateName: %v",
				repo, name, err)
		}
	}
}

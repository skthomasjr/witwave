package agent

import (
	"fmt"
	"strings"
)

// GitOpsFlagSpec is the parsed form of one `--gitops` value. The flag is
// convention-driven sugar over `--gitsync` + N+1 `--gitmap` entries —
// see ExpandGitOps for the fan-out rule.
type GitOpsFlagSpec struct {
	// URL is the git repository URL (HTTPS or SSH).
	URL string

	// Branch is the optional ref. Empty string = remote HEAD.
	Branch string

	// RepoPath is the directory inside the repo the agent's identity
	// lives under. Conventional shape: `.agents/<group>/<agent>/` or
	// `.agents/<agent>/`. The harness mapping pulls
	// `<RepoPath>/.witwave/` and each backend's mapping pulls
	// `<RepoPath>/.<backend-name>/`.
	RepoPath string
}

// ParseGitOps converts a single `--gitops` flag value to a structured
// entry. Form:
//
//	<url>[@<branch>]:<repo-path>
//
// Empty input returns (zero, nil) so callers can detect "not set"
// without juggling sentinel values; any other parse failure returns
// an actionable error.
//
// Splitting strategy: the LAST `:` separates URL+branch (left side)
// from repo-path (right side). SSH URLs (`git@host:owner/repo.git`)
// keep their leading colon-bearing host because the repo-path is
// always a path-shaped suffix and contains no `:` of its own — the
// last `:` in the input is the path separator.
//
// Examples:
//
//	https://github.com/org/repo.git:.agents/self/iris        → URL=https://…, Branch="",      Path=.agents/self/iris
//	https://github.com/org/repo.git@main:.agents/self/iris   → URL=https://…, Branch=main,    Path=.agents/self/iris
//	git@github.com:org/repo.git@main:.agents/self/iris       → URL=git@…,     Branch=main,    Path=.agents/self/iris
func ParseGitOps(raw string) (GitOpsFlagSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return GitOpsFlagSpec{}, nil
	}
	colon := strings.LastIndexByte(raw, ':')
	if colon < 0 {
		return GitOpsFlagSpec{}, fmt.Errorf("--gitops %q: form is <url>[@<branch>]:<repo-path>", raw)
	}
	urlAndBranch := strings.TrimSpace(raw[:colon])
	repoPath := strings.TrimSpace(raw[colon+1:])
	if urlAndBranch == "" {
		return GitOpsFlagSpec{}, fmt.Errorf("--gitops %q: empty URL", raw)
	}
	if repoPath == "" {
		return GitOpsFlagSpec{}, fmt.Errorf("--gitops %q: empty repo-path", raw)
	}
	url, branch := splitURLBranch(urlAndBranch)
	return GitOpsFlagSpec{URL: url, Branch: branch, RepoPath: repoPath}, nil
}

// ExpandGitOps fans the convention-driven `--gitops` short-form into
// the long-form data structures the rest of the build path already
// consumes — one GitSyncFlagSpec entry plus one harness-targeted
// GitMappingFlagSpec plus one per-backend GitMappingFlagSpec for each
// declared `--backend`.
//
// Convention:
//
//   - GitSync name is derived from the URL via DeriveGitSyncName
//     (e.g. ghcr-style host/owner/repo-with-dashes).
//   - Harness mapping: <repo-path>/.witwave/ → /home/agent/.witwave/.
//   - Per backend: <repo-path>/.<backend-name>/ → /home/agent/.<backend-name>/.
//
// The repo-path's trailing slash is normalised — input `.agents/self/iris`
// and `.agents/self/iris/` produce the same mappings.
//
// Returns (nil, nil) when spec is the zero value (caller didn't pass
// `--gitops`).
func ExpandGitOps(spec GitOpsFlagSpec, backends []BackendSpec) ([]GitSyncFlagSpec, []GitMappingFlagSpec) {
	if spec.URL == "" {
		return nil, nil
	}
	syncName := DeriveGitSyncName(spec.URL)

	// Normalise repo-path: drop a trailing slash so the mapping src
	// values are consistent regardless of how the user typed the
	// flag.
	root := strings.TrimSuffix(spec.RepoPath, "/")

	syncs := []GitSyncFlagSpec{
		{Name: syncName, URL: spec.URL, Branch: spec.Branch},
	}

	mappings := []GitMappingFlagSpec{
		{
			Container: HarnessContainer,
			GitSync:   syncName,
			Src:       root + "/.witwave/",
			Dest:      "/home/agent/.witwave/",
		},
	}
	for _, b := range backends {
		mappings = append(mappings, GitMappingFlagSpec{
			Container: b.Name,
			GitSync:   syncName,
			Src:       root + "/." + b.Name + "/",
			Dest:      "/home/agent/." + b.Name + "/",
		})
	}
	return syncs, mappings
}

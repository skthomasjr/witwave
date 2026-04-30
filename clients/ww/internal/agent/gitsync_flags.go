package agent

import (
	"fmt"
	"strings"
)

// HarnessContainer is the magic container name for harness-targeted
// gitMappings. Used as the default `<container>` for --gitsync-map entries
// and as the canonical comparator inside the build / validate paths.
const HarnessContainer = "harness"

// GitSyncFlagSpec is the CLI-side representation of one `--gitsync`
// flag entry. Parsed from `<name>=<url>[@<branch>]`. ExistingSecret
// is populated separately by a matching `--gitsync-secret` flag —
// the parser leaves it empty.
type GitSyncFlagSpec struct {
	// Name is the unique-per-agent identifier the resulting
	// gitSyncs[] entry uses. Referenced by GitMappingFlagSpec.GitSync.
	Name string

	// URL is the git repository URL (HTTPS or SSH).
	URL string

	// Branch is the optional ref. Empty string means remote HEAD.
	Branch string

	// ExistingSecret references a pre-created Kubernetes Secret
	// holding the gitSync credentials (typical keys:
	// GITSYNC_USERNAME / GITSYNC_PASSWORD or GITSYNC_SSH_KEY_FILE).
	// Empty for public repos.
	ExistingSecret string
}

// GitMappingFlagSpec is the CLI-side representation of one `--gitsync-map`
// flag entry. Parsed from `[<container>=]<gitsync-name>:<src>:<dest>`.
type GitMappingFlagSpec struct {
	// Container is "harness" (the default) or one of the agent's
	// declared backend names. Determines whether this lands in
	// Spec.GitMappings[] or BackendSpec.GitMappings[].
	Container string

	// GitSync names a GitSyncFlagSpec entry the mapping pulls from.
	GitSync string

	// Src is the path within the repo (relative to repo root).
	// Trailing "/" indicates a directory copy; otherwise a single-
	// file copy.
	Src string

	// Dest is the absolute path inside the target container.
	Dest string
}

// ParseGitSyncs converts repeatable `--gitsync` flag values to
// structured entries. Form:
//
//	<name>=<url>[@<branch>]
//
// SSH-style URLs (git@host:owner/repo) keep their leading `git@`
// because the URL is recognised by the `:` and `/` characters
// after the @ — only a tail @ followed by a token containing
// neither `:` nor `/` is treated as a branch separator. So
// `name=git@github.com:org/repo.git` parses with empty branch
// and the SSH URL preserved, while
// `name=https://github.com/org/repo.git@main` parses URL +
// branch=main.
func ParseGitSyncs(raw []string) ([]GitSyncFlagSpec, error) {
	out := make([]GitSyncFlagSpec, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for i, r := range raw {
		entry := strings.TrimSpace(r)
		if entry == "" {
			return nil, fmt.Errorf("--gitsync[%d]: empty value", i)
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 1 {
			return nil, fmt.Errorf("--gitsync[%d] %q: form is <name>=<url>[@<branch>]", i, entry)
		}
		name := strings.TrimSpace(entry[:eq])
		rest := strings.TrimSpace(entry[eq+1:])
		if rest == "" {
			return nil, fmt.Errorf("--gitsync[%d] %q: empty URL", i, entry)
		}
		if err := ValidateName(name); err != nil {
			return nil, fmt.Errorf("--gitsync[%d] name %q: %w", i, name, err)
		}
		if seen[name] {
			return nil, fmt.Errorf("--gitsync[%d]: duplicate name %q", i, name)
		}
		seen[name] = true

		url, branch := splitURLBranch(rest)
		out = append(out, GitSyncFlagSpec{Name: name, URL: url, Branch: branch})
	}
	return out, nil
}

// splitURLBranch separates a `<url>[@<branch>]` string. The trailing
// `@<branch>` is recognised only when the segment after the LAST `@`
// is non-empty AND contains no `:` or `/` — those characters indicate
// the `@` is part of an SSH URL (`git@github.com:...`) or a path,
// not a branch separator.
func splitURLBranch(rest string) (url, branch string) {
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return rest, ""
	}
	candidate := rest[at+1:]
	if candidate == "" || strings.ContainsAny(candidate, ":/") {
		return rest, ""
	}
	return rest[:at], candidate
}

// ParseGitMappings converts repeatable `--gitsync-map` flag values to
// structured entries. Form:
//
//	[<container>=]<gitsync-name>:<src>:<dest>
//
// `<container>` defaults to "harness" when the leading `<x>=` segment
// is omitted. The split is "container before first `=` IF that `=`
// comes before the first `:`"; otherwise the value is treated as
// container-less (the harness default). This avoids misinterpreting
// `=` characters embedded inside URLs / paths as the container
// separator.
//
// Cross-flag validation (gitsync references, container references,
// duplicate destinations) lives in ValidateGitFlags — kept separate
// so the parser stays a pure string→struct conversion.
func ParseGitMappings(raw []string) ([]GitMappingFlagSpec, error) {
	out := make([]GitMappingFlagSpec, 0, len(raw))
	for i, r := range raw {
		entry := strings.TrimSpace(r)
		if entry == "" {
			return nil, fmt.Errorf("--gitsync-map[%d]: empty value", i)
		}
		container := HarnessContainer
		body := entry
		if eq := strings.IndexByte(entry, '='); eq >= 0 {
			firstColon := strings.IndexByte(entry, ':')
			if firstColon < 0 || eq < firstColon {
				container = strings.TrimSpace(entry[:eq])
				body = strings.TrimSpace(entry[eq+1:])
				if container == "" {
					return nil, fmt.Errorf("--gitsync-map[%d] %q: empty container before '='", i, entry)
				}
			}
		}
		parts := strings.SplitN(body, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("--gitsync-map[%d] %q: form is [<container>=]<gitsync>:<src>:<dest>", i, entry)
		}
		sync := strings.TrimSpace(parts[0])
		src := strings.TrimSpace(parts[1])
		dest := strings.TrimSpace(parts[2])
		if sync == "" || src == "" || dest == "" {
			return nil, fmt.Errorf("--gitsync-map[%d] %q: gitsync, src, and dest are all required", i, entry)
		}
		if !strings.HasPrefix(dest, "/") {
			return nil, fmt.Errorf("--gitsync-map[%d] %q: dest %q must be absolute (start with '/')", i, entry, dest)
		}
		out = append(out, GitMappingFlagSpec{
			Container: container,
			GitSync:   sync,
			Src:       src,
			Dest:      dest,
		})
	}
	return out, nil
}

// ParseGitSyncSecrets converts repeatable `--gitsync-secret` flag
// values to a (gitsync-name → k8s-secret-name) map. Form:
//
//	<gitsync-name>=<k8s-secret-name>
//
// Cross-flag validation (does the gitsync-name reference an actual
// --gitsync entry?) happens in ApplyGitSyncSecrets.
func ParseGitSyncSecrets(raw []string) (map[string]string, error) {
	out := map[string]string{}
	for i, r := range raw {
		entry := strings.TrimSpace(r)
		if entry == "" {
			return nil, fmt.Errorf("--gitsync-secret[%d]: empty value", i)
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 1 {
			return nil, fmt.Errorf("--gitsync-secret[%d] %q: form is <gitsync-name>=<k8s-secret>", i, entry)
		}
		name := strings.TrimSpace(entry[:eq])
		secret := strings.TrimSpace(entry[eq+1:])
		if name == "" || secret == "" {
			return nil, fmt.Errorf("--gitsync-secret[%d] %q: both name and secret required", i, entry)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("--gitsync-secret[%d]: duplicate gitsync %q", i, name)
		}
		out[name] = secret
	}
	return out, nil
}

// ApplyGitSyncSecrets stamps ExistingSecret onto the matching
// GitSyncFlagSpec entries in place. Returns an error if the secret
// map references a gitsync name that doesn't exist in `syncs`.
func ApplyGitSyncSecrets(syncs []GitSyncFlagSpec, secrets map[string]string) ([]GitSyncFlagSpec, error) {
	if len(secrets) == 0 {
		return syncs, nil
	}
	idx := make(map[string]int, len(syncs))
	for i, s := range syncs {
		idx[s.Name] = i
	}
	for name, sec := range secrets {
		i, ok := idx[name]
		if !ok {
			return nil, fmt.Errorf("--gitsync-secret %q: no matching --gitsync entry with that name", name)
		}
		syncs[i].ExistingSecret = sec
	}
	return syncs, nil
}

// ValidateGitFlags is the cross-flag validation gate — runs after
// every parser succeeds and before the CR is built. Checks:
//
//   - every --gitsync-map.GitSync references an actual --gitsync entry.
//   - every --gitsync-map.Container is "harness" or one of the declared
//     --backend names.
//   - no two --gitsync-map entries share the same (container, dest) —
//     duplicate destinations on the same container would render
//     ambiguously.
//
// Per-flag shape errors (form, empty values, etc.) live in the
// individual parsers; this function only catches errors that need
// the union of all three flag groups to detect.
func ValidateGitFlags(syncs []GitSyncFlagSpec, mappings []GitMappingFlagSpec, backends []BackendSpec) error {
	syncNames := make(map[string]bool, len(syncs))
	for _, s := range syncs {
		syncNames[s.Name] = true
	}
	containerNames := make(map[string]bool, len(backends)+1)
	containerNames[HarnessContainer] = true
	for _, b := range backends {
		containerNames[b.Name] = true
	}

	type destKey struct{ container, dest string }
	seen := map[destKey]int{}
	for i, m := range mappings {
		if !syncNames[m.GitSync] {
			return fmt.Errorf("--gitsync-map[%d] gitsync %q: no matching --gitsync entry", i, m.GitSync)
		}
		if !containerNames[m.Container] {
			return fmt.Errorf("--gitsync-map[%d] container %q: must be %q or one of the declared --backend names",
				i, m.Container, HarnessContainer)
		}
		k := destKey{m.Container, m.Dest}
		if prior, dup := seen[k]; dup {
			return fmt.Errorf("--gitsync-map[%d]: duplicate (container=%q, dest=%q) — already set by --gitsync-map[%d]",
				i, m.Container, m.Dest, prior)
		}
		seen[k] = i
	}
	return nil
}

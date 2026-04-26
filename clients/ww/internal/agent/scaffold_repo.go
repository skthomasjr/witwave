package agent

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// Per-shell-out timeouts. git operations here are pure metadata reads
// (config get, ls-remote, credential fill) and should be sub-second on
// the happy path; gh auth token is a local keychain read. Caps prevent
// a misbehaving credential helper or a network black hole from wedging
// the CLI indefinitely. #1616.
const (
	_gitCmdTimeout = 15 * time.Second
	_ghCmdTimeout  = 10 * time.Second

	// _credentialFillMaxBytes bounds the bytes we'll read from a
	// `git credential fill` helper. A well-behaved helper returns a
	// few hundred bytes; a malfunctioning or hostile one could stream
	// indefinitely. 64 KiB is comfortably above any legitimate output.
	_credentialFillMaxBytes = 64 * 1024
)

// _credentialEnvKeys lists env var names that we strip from any child
// process environment before exec. Mirrors the set in update/notify.go.
// Keep alphabetised. None of these are needed for the read-only git /
// gh probes this file performs.
var _credentialEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY": {},
	"GH_TOKEN":          {},
	"GITHUB_TOKEN":      {},
	"GIT_TOKEN":         {},
	"OPENAI_API_KEY":    {},
}

// sanitizeShellEnv returns a copy of env with credential-bearing
// entries removed. Operates on `KEY=VALUE` strings as produced by
// os.Environ() and consumed by exec.Cmd.Env.
func sanitizeShellEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := _credentialEnvKeys[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// commandWithTimeout wraps exec.CommandContext with a derived timeout
// context. The returned cancel function MUST be called by the caller
// (typically via defer) to release timer resources. The child's env
// is pre-sanitised to drop credential-bearing variables.
func commandWithTimeout(timeout time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = sanitizeShellEnv(os.Environ())
	return cmd, cancel
}

// osGetenv is os.Getenv, captured through a package-level indirection
// so tests can stub environment resolution without touching the real
// process environment.
var osGetenv = os.Getenv

// repoRef captures a parsed "remote repo reference" supplied via the
// --repo flag. Accepts three shorthand forms:
//
//   - `owner/repo`              → https://github.com/owner/repo.git (GitHub default)
//   - `github.com/owner/repo`   → https://github.com/owner/repo.git
//   - `https://host/owner/repo` → https://host/owner/repo.git
//   - `git@host:owner/repo`     → SSH form, passed through verbatim
//
// Trailing `.git` is optional throughout — normaliseURL tacks it on.
type repoRef struct {
	// CloneURL is the normalised URL go-git should use for remote ops.
	CloneURL string
	// Display is a short human-readable form for logs/banners
	// (usually `owner/repo`). Never contains credentials.
	Display string
	// Scheme is one of "https", "http", "ssh". Auth resolution branches
	// on this.
	Scheme string
}

// shorthandOwnerRepoRE matches the `owner/repo` shorthand. Deliberately
// narrow — any character that GitHub forbids in a username or repo name
// (spaces, `@`, `:`, `/` outside the single separator) falls through to
// the URL parser and gets rejected there.
var shorthandOwnerRepoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// sshShorthandRE matches `git@host:owner/repo` — SSH's historical
// non-URL shorthand.
var sshShorthandRE = regexp.MustCompile(`^git@[^:]+:[^/]+/[^/]+$`)

// parseRepoRef normalises the raw --repo value into a form go-git can
// clone. Error messages point operators at the accepted shorthand set.
func parseRepoRef(raw string) (repoRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return repoRef{}, fmt.Errorf("repo is required (accepts: owner/repo, github.com/owner/repo, full URL, or git@host:owner/repo)")
	}

	// SSH shorthand — pass through verbatim; go-git handles it via the
	// ssh transport.
	if sshShorthandRE.MatchString(raw) {
		// Extract owner/repo for display.
		colon := strings.IndexByte(raw, ':')
		display := strings.TrimSuffix(raw[colon+1:], ".git")
		return repoRef{CloneURL: ensureGitSuffix(raw), Display: display, Scheme: "ssh"}, nil
	}

	// Plain owner/repo shorthand → default to github.com over https.
	if shorthandOwnerRepoRE.MatchString(raw) {
		display := raw
		raw = "https://github.com/" + raw
		return repoRef{CloneURL: ensureGitSuffix(raw), Display: display, Scheme: "https"}, nil
	}

	// Host-qualified form without scheme (`github.com/owner/repo`) —
	// promote to https.
	if !strings.Contains(raw, "://") && strings.Count(raw, "/") >= 2 {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return repoRef{}, fmt.Errorf("parse repo %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return repoRef{}, fmt.Errorf(
			"repo URL must use https or git@host:owner/repo (got scheme %q)", u.Scheme,
		)
	}
	// Strip any embedded credentials from the URL before keeping it —
	// we never want `https://user:pat@...` leaking into logs or being
	// stored anywhere. Auth lives in a separate path.
	u.User = nil
	parts := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	segments := strings.Split(parts, "/")
	display := parts
	if len(segments) >= 2 {
		display = segments[len(segments)-2] + "/" + segments[len(segments)-1]
	}
	return repoRef{
		CloneURL: ensureGitSuffix(u.String()),
		Display:  display,
		Scheme:   u.Scheme,
	}, nil
}

func ensureGitSuffix(s string) string {
	if strings.HasSuffix(s, ".git") {
		return s
	}
	return s + ".git"
}

// resolveGitAuth returns the go-git AuthMethod for the given remote URL,
// following the "trust the system" posture:
//
//  1. Environment tokens first (GITHUB_TOKEN / GH_TOKEN for scripting /
//     CI use).
//  2. git credential helper via `git credential fill` — inherits
//     whatever the user's normal `git push` would use (Keychain on
//     macOS, Credential Manager on Windows, libsecret on Linux, …).
//  3. SSH agent for ssh:// and git@host:… URLs.
//  4. Nil auth — the remote is public or the transport supplies its
//     own credentials. go-git will error at transport time if this is
//     wrong; we let its error surface verbatim.
func resolveGitAuth(ref repoRef) (transport.AuthMethod, error) {
	switch ref.Scheme {
	case "ssh":
		// Default to the SSH agent; go-git's NewSSHAgentAuth handles
		// both `git@github.com:foo/bar` and `ssh://git@...` forms.
		auth, err := ssh.NewSSHAgentAuth("git")
		if err != nil {
			return nil, fmt.Errorf("ssh-agent auth: %w (is ssh-agent running and a key loaded?)", err)
		}
		return auth, nil
	case "https", "http":
		if tok := envToken(); tok != "" {
			// Username "x-access-token" is GitHub's conventional user
			// for PAT-based auth; works on GitHub, ignored on others.
			return &http.BasicAuth{Username: "x-access-token", Password: tok}, nil
		}
		// For GitHub specifically, prefer `gh auth token` over
		// `git credential fill`. The macOS Keychain / Credential
		// Manager entries for git frequently hold a stale token from a
		// prior `gh auth login` cycle — gh itself keeps the active
		// token current, so reading from gh first avoids the class of
		// bug where git-credential's stale token gets a 404 that looks
		// indistinguishable from "repo doesn't exist".
		if isGitHubHost(ref.CloneURL) {
			if tok, ok := ghAuthToken(); ok {
				return &http.BasicAuth{Username: "x-access-token", Password: tok}, nil
			}
		}
		if user, pass, ok := gitCredentialFill(ref.CloneURL); ok {
			// GitHub PAT auth requires username "x-access-token", not
			// the user ID the credential helper may return. Credential
			// helpers on macOS frequently stash the numeric account id
			// as `username`, which GitHub rejects with a 404 that looks
			// indistinguishable from "repo doesn't exist." Normalise
			// to x-access-token when the token looks like a GitHub PAT.
			if isGitHubTokenShape(pass) {
				user = "x-access-token"
			}
			return &http.BasicAuth{Username: user, Password: pass}, nil
		}
		// Last resort — try gh even on non-github hosts (some users
		// have enterprise gh configurations that cover github.com-
		// family hosts). Ignored if gh isn't installed or
		// authenticated.
		if tok, ok := ghAuthToken(); ok {
			return &http.BasicAuth{Username: "x-access-token", Password: tok}, nil
		}
		// No creds found — could be a public repo. go-git will attempt
		// unauthenticated clone; if the repo is private the transport
		// will return a 401 with a clear message.
		return nil, nil
	}
	return nil, nil
}

// envToken reads the common CI / scripting token env vars in
// precedence order. First non-empty wins.
func envToken() string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GIT_TOKEN"} {
		if v := strings.TrimSpace(envLookup(k)); v != "" {
			return v
		}
	}
	return ""
}

// envLookup is a thin wrapper over os.Getenv so tests can substitute a
// deterministic environment without monkey-patching os.Getenv.
var envLookup = func(k string) string {
	return osGetenv(k)
}

// isGitHubHost returns true when the clone URL points at github.com or
// a github.com subdomain. Used to gate GitHub-specific auth quirks
// (PAT username, gh auth preference).
func isGitHubHost(cloneURL string) bool {
	u, err := url.Parse(cloneURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Host)
	return h == "github.com" || strings.HasSuffix(h, ".github.com")
}

// isGitHubTokenShape returns true when the string looks like a GitHub
// personal/OAuth/server token (ghp_/gho_/ghs_/ghu_/github_pat_ prefix).
// Used to decide whether to override the username a credential helper
// returned — GitHub requires the literal username "x-access-token" for
// PAT auth, regardless of what account the token was issued for.
func isGitHubTokenShape(s string) bool {
	for _, prefix := range []string{"ghp_", "gho_", "ghs_", "ghu_", "github_pat_"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// ghAuthToken shells out to `gh auth token` and returns the resulting
// token. Empty + ok=false when gh is missing, unauthenticated, or any
// other failure — callers treat this as "no credentials found" and
// move on. Many ww users authenticate github.com via gh rather than
// maintaining a standalone git credential helper entry, so this is
// often the only auth path that actually works on a dev laptop.
func ghAuthToken() (string, bool) {
	cmd, cancel := commandWithTimeout(_ghCmdTimeout, "gh", "auth", "token")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", false
	}
	return tok, true
}

// detectRemoteDefaultBranch resolves the default branch of a remote by
// asking for its HEAD symref. Works against both go-git-style and
// github.com-style remotes via `git ls-remote --symref <url> HEAD`.
// Returns empty string when:
//   - git isn't installed or returns an error
//   - the remote has no HEAD (empty repo — the common case for a
//     freshly-created GitHub repo)
//   - the output can't be parsed
//
// Callers fall back to their own default ("main") on empty return.
// The fallback is deliberate: empty repos have no meaningful default
// yet, and "main" matches what GitHub creates on first push since 2020.
func detectRemoteDefaultBranch(cloneURL string) string {
	cmd, cancel := commandWithTimeout(_gitCmdTimeout, "git", "ls-remote", "--symref", cloneURL, "HEAD")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Expected output shape when the remote has a HEAD:
	//   ref: refs/heads/main	HEAD
	//   <sha>\tHEAD
	// We only care about the first line's "refs/heads/<branch>" target.
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "ref: ") {
			continue
		}
		// Format: "ref: refs/heads/<branch>\tHEAD"
		rest := strings.TrimPrefix(line, "ref: ")
		if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
			rest = rest[:tab]
		}
		rest = strings.TrimSpace(rest)
		return strings.TrimPrefix(rest, "refs/heads/")
	}
	return ""
}

// gitConfigGet runs `git config --get <key>` and returns the value,
// trimmed. Empty string when git isn't installed, the key isn't set,
// or any other failure — callers fall through to their defaults.
func gitConfigGet(key string) string {
	cmd, cancel := commandWithTimeout(_gitCmdTimeout, "git", "config", "--get", key)
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitCredentialFill runs `git credential fill` for the given URL and
// returns (username, password, ok) if the helper produced credentials.
// Any failure returns ok=false — callers fall through to the no-auth
// path.
//
// Matches what the `git` CLI itself does internally: writes
// `protocol=https\nhost=<host>\npath=<path>\n\n` to stdin, reads
// `username=...\npassword=...\n` from stdout.
func gitCredentialFill(cloneURL string) (string, string, bool) {
	u, err := url.Parse(cloneURL)
	if err != nil {
		return "", "", false
	}
	// git credential helpers key on the path WITHOUT a trailing `.git`
	// — that's what git itself passes to them when talking to GitHub.
	// Passing the .git suffix through here causes helpers to miss a
	// perfectly-good credential entry.
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	cmd, cancel := commandWithTimeout(_gitCmdTimeout, "git", "credential", "fill")
	defer cancel()
	cmd.Stdin = strings.NewReader(fmt.Sprintf(
		"protocol=%s\nhost=%s\npath=%s\n\n",
		u.Scheme, u.Host, path,
	))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", false
	}
	if err := cmd.Start(); err != nil {
		return "", "", false
	}
	// Cap output so a malfunctioning credential helper can't OOM us.
	// The helper protocol's expected output is a handful of `key=value`
	// lines; 64 KiB is two orders of magnitude over realistic.
	out, readErr := io.ReadAll(io.LimitReader(stdout, _credentialFillMaxBytes))
	// Always Wait so we don't leak the child even on a read error.
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil {
		return "", "", false
	}
	var username, password string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "username="):
			username = strings.TrimPrefix(line, "username=")
		case strings.HasPrefix(line, "password="):
			password = strings.TrimPrefix(line, "password=")
		}
	}
	if password == "" {
		return "", "", false
	}
	if username == "" {
		// Some helpers only return the token; use the GitHub convention.
		username = "x-access-token"
	}
	return username, password, true
}

// Package update implements ww's "a newer version is available" check
// plus the optional prompt/auto upgrade paths that delegate to whichever
// installer (Homebrew, `go install`, direct-binary download) placed the
// current binary on disk.
//
// Everything here is best-effort: the check is asynchronous, bounded by
// a short HTTP timeout, cached on disk to avoid hammering the GitHub
// Releases API, and silently no-op on any error. A failure in the
// version check MUST NEVER interfere with the user's actual command.
package update

import (
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// Mode is how ww should react when a newer release is detected.
type Mode string

const (
	// ModeOff disables the version check entirely — no network call,
	// no banner, no on-disk cache read.
	ModeOff Mode = "off"

	// ModeNotify (default) prints a one-line banner at the end of the
	// command's output when a newer version exists.
	ModeNotify Mode = "notify"

	// ModePrompt prints the banner and then asks the user Y/N whether
	// to run the relevant installer command. Auto-downgrades to
	// ModeNotify when stdin is not a TTY (scripts, pipelines, CI) so
	// the CLI never hangs waiting for input that will not arrive.
	ModePrompt Mode = "prompt"

	// ModeAuto prints the banner and unconditionally runs the relevant
	// installer command. Intended for users who explicitly opt into
	// unattended upgrades; *not* the default for this reason.
	ModeAuto Mode = "auto"
)

// DefaultMode is applied when no config or env var sets one.
const DefaultMode = ModeNotify

// DefaultInterval is the minimum time between successive GitHub Releases
// API calls (cache TTL for "is there a newer version"). 24h keeps the
// unauthenticated API quota (60 req/h per IP) trivially comfortable.
const DefaultInterval = 24 * time.Hour

// ParseMode accepts "off"/"notify"/"prompt"/"auto" case-insensitively
// and returns DefaultMode for the empty string. Any other value is an
// error so a typo in config.toml surfaces immediately rather than
// silently falling back.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultMode, nil
	case "off":
		return ModeOff, nil
	case "notify":
		return ModeNotify, nil
	case "prompt":
		return ModePrompt, nil
	case "auto":
		return ModeAuto, nil
	default:
		return "", &unknownModeError{value: s}
	}
}

type unknownModeError struct{ value string }

func (e *unknownModeError) Error() string {
	return "update: unknown mode " + e.value + " (expected off/notify/prompt/auto)"
}

// EffectiveMode applies runtime guardrails on top of a configured mode.
// Order of overrides (highest priority first):
//
//  1. Explicit opt-out via WW_NO_UPDATE_CHECK — forces off.
//  2. CI signals (CI, GITHUB_ACTIONS, BUILDKITE, CIRCLECI, GITLAB_CI) —
//     force off. An automated runner should never block on a version
//     check, a prompt, or an unexpected upgrade.
//  3. Non-TTY stdin + ModePrompt — downgrade to ModeNotify so we do
//     not hang reading from a pipe that will never deliver y/n.
//
// isStdinTTY is injected so tests can exercise every branch without
// attaching a pty.
func EffectiveMode(configured Mode, getenv func(string) string, isStdinTTY func() bool) Mode {
	if getenv == nil {
		getenv = os.Getenv
	}
	if isStdinTTY == nil {
		isStdinTTY = defaultIsStdinTTY
	}

	if truthy(getenv("WW_NO_UPDATE_CHECK")) {
		return ModeOff
	}

	// CI environments: a variety of providers set one of these.
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "CIRCLECI", "GITLAB_CI"} {
		if truthy(getenv(v)) {
			return ModeOff
		}
	}

	if configured == ModePrompt && !isStdinTTY() {
		return ModeNotify
	}
	return configured
}

// truthy accepts the common "this is set and non-false" shapes.
// GitHub Actions sets CI=true; some runners set it to "1"; pre-set to
// an empty string by shells sometimes — treat that as false.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func defaultIsStdinTTY() bool {
	// golang.org/x/term.IsTerminal uses the OS-appropriate syscall
	// (ioctl(TIOCGETA) on Unix, GetConsoleMode on Windows) to answer
	// "is this fd actually a terminal", not the cheaper-but-wrong
	// `Mode()&os.ModeCharDevice != 0` that counts /dev/null as a TTY
	// because /dev/null is a character device. A user redirecting
	// stdin from /dev/null (e.g. in a background script) must be
	// treated as non-interactive so prompt mode doesn't render a
	// question they can never answer.
	return term.IsTerminal(int(os.Stdin.Fd()))
}

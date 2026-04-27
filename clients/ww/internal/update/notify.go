package update

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Per-shell-out timeouts. Tied to the kind of work each tool does:
// brew can chew on tap refresh / cask download for tens of seconds
// in the worst case but should never hang forever; go install is bounded
// by the brew slot since they're in the same upgrade flow. #1616.
const (
	_brewCmdTimeout = 30 * time.Second
	_goCmdTimeout   = 60 * time.Second
)

// _credentialEnvKeys lists env var names that we strip from any child
// process environment before exec. None of these are required by brew /
// git / gh for the read-only operations this package performs, and any
// of them leaking into a subprocess that logs its env (or one that
// happens to be replaced by a malicious shim on PATH) is a credential-
// disclosure footgun. Keep alphabetised; matches scaffold_repo.go.
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
// (typically via defer) to release timer resources promptly even when
// the child exits before the deadline.
func commandWithTimeout(parent context.Context, timeout time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = sanitizeShellEnv(os.Environ())
	return cmd, cancel
}

// Notify renders the user-facing output for an available upgrade,
// following the configured Mode. It is called from cobra's post-run
// hook after the user's actual command has completed, so we are
// allowed to print to stderr and (for ModePrompt) read from stdin.
//
// For ModePrompt and ModeAuto, Notify shells out to the installer
// command discovered via DetectInstallMethod. If no automatic path is
// available (InstallMethodBinary), both modes degrade gracefully to
// printing instructions and returning — the user then downloads the
// tarball themselves.
//
// The return value is purely informational: nil on success, an error
// when a non-trivial step failed (e.g. `brew upgrade` returned non-zero
// exit). Callers may choose to print the error or swallow it; the
// version-check system is strictly best-effort.
func Notify(
	ctx context.Context,
	mode Mode,
	notice *Notice,
	method InstallMethod,
	stdout io.Writer,
	stderr io.Writer,
	stdin io.Reader,
) error {
	if notice == nil || mode == ModeOff {
		return nil
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	banner := formatBanner(notice, method)
	fmt.Fprintln(stderr, banner)

	switch mode {
	case ModeNotify:
		return nil

	case ModePrompt:
		if !askYesNo(stdin, stderr, "Upgrade now?") {
			return nil
		}
		return RunUpgrade(ctx, method, notice.CurrentVersion, stdout, stderr)

	case ModeAuto:
		return RunUpgrade(ctx, method, notice.CurrentVersion, stdout, stderr)
	}
	return nil
}

// formatBanner produces the single-line "an upgrade is available"
// notice plus the right upgrade instruction for the current install
// method. Uses stderr-suitable formatting — no ANSI color, no
// clever box-drawing. Matches the rest of ww's output restraint.
func formatBanner(notice *Notice, method InstallMethod) string {
	channelSuffix := ""
	if notice.Channel == ChannelBeta {
		channelSuffix = " (beta channel)"
	}

	header := fmt.Sprintf(
		"↑ ww %s is available%s (you're on %s). %s",
		notice.LatestTag, channelSuffix, notice.CurrentVersion, notice.LatestURL,
	)

	instruction := ""
	if cmd := method.UpgradeCommand(); cmd != "" {
		instruction = "  To upgrade: " + cmd
	} else {
		// Binary fallback: no recognized auto-upgrade path; point the
		// user at the release page and let them do whatever they did
		// last time.
		instruction = "  Download the new binary from the URL above, or set [update] mode = \"off\" in ~/.config/ww/config.toml to silence this notice."
	}
	return header + "\n" + instruction
}

// askYesNo reads a single line from stdin and returns true for an
// affirmative answer. Default is "yes" — hitting Enter with no input
// confirms the upgrade, matching most CLI prompts where the capitalized
// letter in `[Y/n]` is the default.
//
// Guards against a nil/closed stdin by returning false (treats as "no")
// so automated invocations that slipped past the ModeEffective downgrade
// still can't end up upgraded without explicit consent.
func askYesNo(stdin io.Reader, stderr io.Writer, prompt string) bool {
	if stdin == nil {
		return false
	}
	fmt.Fprintf(stderr, "%s [Y/n] ", prompt)
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		return false
	}
	line := strings.ToLower(strings.TrimSpace(scanner.Text()))
	switch line {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

// RunUpgrade dispatches to the installer command matching the detected
// install method. For brew installs it first refreshes the witwave-ai
// tap so a newly-pushed Casks/ww.rb is visible; then runs
// `brew upgrade ww`. (Pre-#1446 the tap shipped Formula/ww.rb; the
// migration retired the formula path. brew 4.x+ handles `brew upgrade
// <name>` agnostically across formula and cask, so this command stays
// the same — but a user with a pre-migration formula install will need
// to `brew uninstall ww && brew install witwave-ai/homebrew-ww/ww`
// once to switch to the cask, since brew does not auto-migrate same-name
// formula → cask within a tap. See Homebrew/brew#20585.) For go-install
// it runs `go install @latest`. For binary installs there's no automatic
// path — we print a message and return nil.
//
// stdout/stderr are plumbed through to the child process so the user
// sees the installer's output in real time. This is important for brew
// in particular, which can take tens of seconds.
func RunUpgrade(ctx context.Context, method InstallMethod, currentVersion string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	switch method {
	case InstallMethodBrew:
		// Explicitly refresh all taps FIRST. We used to rely on
		// `brew upgrade`'s own auto-update — but that path is
		// heuristic (once-per-24h by default, skipped entirely when
		// HOMEBREW_NO_AUTO_UPDATE=1 is set), so a ww user whose brew
		// cache was "recent enough" would hit the path where `brew
		// upgrade ww` sees the stale tap index, reports "already
		// installed", and ww printed a lying "Upgraded." line.
		// Running `brew update` (no args) refreshes every tap
		// including witwave-ai/ww and is a reliable ~1s call.
		// On failure we don't bail — log it and try the upgrade
		// anyway; if the user really is already current the upgrade
		// is a no-op, and we'll detect that below regardless.
		updateCmd, cancelUpdate := commandWithTimeout(ctx, _brewCmdTimeout, "brew", "update")
		defer cancelUpdate()
		updateCmd.Stdout, updateCmd.Stderr = stdout, stderr
		// #1554: refuse to inherit stdin on any brew call. Both
		// `brew update` and `brew upgrade` can trigger interactive
		// prompts (sudo for /usr/local writes on older macOS, "Y/n"
		// on certain cask upgrades, the GitHub-API rate-limit
		// login prompt). Inheriting the parent's stdin makes an
		// unattended self-upgrade block indefinitely when we hit
		// one of those branches. Closing stdin lets brew fail fast
		// with "needs tty" rather than wedging the parent.
		updateCmd.Stdin = nil
		if err := updateCmd.Run(); err != nil {
			fmt.Fprintf(stderr, "warning: `brew update` failed: %v — continuing with upgrade anyway\n", err)
		}

		upgradeCmd, cancelUpgrade := commandWithTimeout(ctx, _brewCmdTimeout, "brew", "upgrade", "ww")
		defer cancelUpgrade()
		upgradeCmd.Stdout, upgradeCmd.Stderr = stdout, stderr
		upgradeCmd.Stdin = nil // #1554 — see update call above.
		if err := upgradeCmd.Run(); err != nil {
			return fmt.Errorf("brew upgrade ww: %w", err)
		}

		// Verify the upgrade actually took. `brew upgrade` exits 0
		// both when it upgraded AND when it thinks nothing to do,
		// so we can't trust the exit code alone. Ask brew what
		// version is installed now and compare against what the
		// caller expected — if brew still reports the old version,
		// the user likely has a stale tap pin or a HOMEBREW_* env
		// var interfering. Surface that explicitly instead of
		// printing "Upgraded." when nothing moved.
		installed, err := brewInstalledVersion(ctx)
		if err != nil {
			// Don't fail the overall command on a diagnostic probe
			// — the upgrade may have worked; we just can't confirm.
			fmt.Fprintf(stderr, "note: couldn't verify installed version via brew: %v\n", err)
			fmt.Fprintln(stderr, "Run your ww command again to use the new version.")
			return nil
		}
		stripped := strings.TrimPrefix(currentVersion, "v")
		if installed != "" && installed == stripped {
			fmt.Fprintf(stderr,
				"ww is already at %s according to brew, even though GitHub reports a newer release. "+
					"This usually means HOMEBREW_NO_AUTO_UPDATE=1 is set or the witwave-ai/ww tap is "+
					"pinned/forked locally. Try `brew untap witwave-ai/ww && brew tap witwave-ai/ww` "+
					"and re-run `ww update`.\n",
				installed,
			)
			return nil
		}
		if installed != "" {
			fmt.Fprintf(stderr, "Upgraded to %s. Run your ww command again to use the new version.\n", installed)
		} else {
			fmt.Fprintln(stderr, "Upgraded. Run your ww command again to use the new version.")
		}
		return nil

	case InstallMethodGoInstall:
		goCmd, cancelGo := commandWithTimeout(ctx, _goCmdTimeout, "go", "install",
			"github.com/witwave-ai/witwave/clients/ww@latest",
		)
		defer cancelGo()
		goCmd.Stdout, goCmd.Stderr = stdout, stderr
		if err := goCmd.Run(); err != nil {
			return fmt.Errorf("go install: %w", err)
		}
		fmt.Fprintln(stderr, "Upgraded. Run your ww command again to use the new version.")
		return nil

	case InstallMethodCurl:
		// Re-run the canonical install pipeline. The script is
		// idempotent and self-contained — it'll pick up the latest
		// stable, verify the SHA256, and atomically replace the
		// running binary at the same install location. We invoke
		// `sh` with the script piped in over stdin to mirror what a
		// user would type, rather than trying to fetch + exec a
		// local copy of install.sh (which the binary doesn't ship).
		//
		// Bounded by a generous timeout — the script itself does
		// retried HTTP downloads and may have to traverse a corporate
		// proxy. Re-uses _goCmdTimeout because the work shape (one
		// HTTP fetch + a local file write) is comparable.
		shCmd, cancelSh := commandWithTimeout(ctx, _goCmdTimeout, "sh", "-c",
			"set -e; "+
				"if command -v curl >/dev/null 2>&1; then "+
				"  curl -fsSL "+curlInstallURL+" | sh; "+
				"elif command -v wget >/dev/null 2>&1; then "+
				"  wget -qO- "+curlInstallURL+" | sh; "+
				"else "+
				"  echo 'ww update: neither curl nor wget on PATH' >&2; exit 1; "+
				"fi",
		)
		defer cancelSh()
		shCmd.Stdout, shCmd.Stderr = stdout, stderr
		// Same posture as the brew shell-out (#1554): refuse to
		// inherit stdin so a script that decides to prompt
		// (`sudo` password, etc.) fails fast rather than wedging.
		shCmd.Stdin = nil
		// Sanitise creds out of the child env — install.sh has no
		// reason to see ANTHROPIC_API_KEY etc.
		shCmd.Env = sanitizeShellEnv(os.Environ())
		if err := shCmd.Run(); err != nil {
			return fmt.Errorf("curl-installer upgrade: %w", err)
		}
		fmt.Fprintln(stderr, "Upgraded. Run your ww command again to use the new version.")
		return nil

	default:
		fmt.Fprintln(stderr,
			"No automatic upgrade path for this binary — download the "+
				"new tarball from the releases URL above.",
		)
		return nil
	}
}

// brewInstalledVersion returns the version string brew reports as the
// currently-installed version of ww, or empty when brew doesn't have
// it installed. Output shape is `ww 0.5.0` — we split on whitespace
// and take the last token. Best-effort: any parse failure returns
// ("", err) so callers can degrade gracefully instead of crashing.
func brewInstalledVersion(ctx context.Context) (string, error) {
	cmd, cancel := commandWithTimeout(ctx, _brewCmdTimeout, "brew", "list", "--versions", "ww")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		// `brew list --versions` exits non-zero when the cask isn't
		// installed. Don't treat that as a hard failure — just return
		// empty + nil so the caller prints a neutral message.
		return "", nil
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return "", fmt.Errorf("brew list output had %d fields, expected >= 2: %q", len(fields), string(out))
	}
	// Last field is the version string (brew can list multiple if
	// multiple kegs are installed; we take the last reported one as
	// that's typically the most recent).
	return fields[len(fields)-1], nil
}

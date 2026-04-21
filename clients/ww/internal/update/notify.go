package update

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

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
// tap so a newly-pushed Formula/ww.rb is visible; then runs
// `brew upgrade ww`. For go-install it runs `go install @latest`.
// For binary installs there's no automatic path — we print a message
// and return nil.
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
		updateCmd := exec.CommandContext(ctx, "brew", "update")
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

		upgradeCmd := exec.CommandContext(ctx, "brew", "upgrade", "ww")
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
		goCmd := exec.CommandContext(ctx, "go", "install",
			"github.com/skthomasjr/witwave/clients/ww@latest",
		)
		goCmd.Stdout, goCmd.Stderr = stdout, stderr
		if err := goCmd.Run(); err != nil {
			return fmt.Errorf("go install: %w", err)
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
	cmd := exec.CommandContext(ctx, "brew", "list", "--versions", "ww")
	out, err := cmd.Output()
	if err != nil {
		// `brew list --versions` exits non-zero when the formula isn't
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

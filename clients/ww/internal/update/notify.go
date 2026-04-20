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
		return RunUpgrade(ctx, method, stdout, stderr)

	case ModeAuto:
		return RunUpgrade(ctx, method, stdout, stderr)
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
func RunUpgrade(ctx context.Context, method InstallMethod, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	switch method {
	case InstallMethodBrew:
		// `brew upgrade` performs its own auto-update of installed
		// taps (unless the user set HOMEBREW_NO_AUTO_UPDATE=1), so a
		// pre-step `brew update` is redundant and, depending on the
		// command shape, either silently no-ops or surfaces a
		// "No available formula" warning (brew's `update` takes a
		// repo path, not a formula name). Just run the upgrade and
		// let brew handle its own tap freshness.
		upgradeCmd := exec.CommandContext(ctx, "brew", "upgrade", "ww")
		upgradeCmd.Stdout, upgradeCmd.Stderr = stdout, stderr
		if err := upgradeCmd.Run(); err != nil {
			return fmt.Errorf("brew upgrade ww: %w", err)
		}
		fmt.Fprintln(stderr, "Upgraded. Run your ww command again to use the new version.")
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

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/skthomasjr/witwave/clients/ww/internal/config"
	"github.com/skthomasjr/witwave/clients/ww/internal/update"
	"github.com/spf13/cobra"
)

// newUpdateCmd builds the `ww update` subcommand — the explicit
// "check + upgrade me now" action, complementing the async background
// banner that mode=notify/prompt/auto drive from the root PreRun hook.
//
// Default flow:
//
//  1. Detect install method (brew / go install / standalone binary)
//  2. Hit the GitHub Releases API on the configured channel
//  3. If a newer version exists → shell out to the appropriate
//     installer (`brew upgrade ww`, `go install @latest`) and stream
//     the installer's output inline so the user sees progress
//  4. If already current → print that + exit 0
//
// Flags:
//
//	--force   skip the check, unconditionally run the upgrade
//	--check   check only; do NOT run the upgrade even if one exists
//
// The config [update] section still governs default channel + interval
// (so `ww update` on a 23-hour-old cache returns quickly without
// hitting the API). Env var overrides (WW_UPDATE_CHANNEL etc.) apply
// the same way they do for the background check. Unlike the background
// check, `ww update` IGNORES update.mode=off — the user explicitly
// asked, so we run.
func newUpdateCmd() *cobra.Command {
	var (
		force bool
		check bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a newer ww release and upgrade in place",
		Long: "Checks whether a newer ww release is available on the configured\n" +
			"update channel (stable by default), then shells out to the\n" +
			"appropriate installer — `brew upgrade ww` for Homebrew installs,\n" +
			"`go install ...@latest` for go-install users, or a download hint\n" +
			"for standalone binaries.\n\n" +
			"Runs in the foreground; streams the installer's output so you\n" +
			"can see progress. Respects the [update] config block for channel\n" +
			"and cache interval (to avoid hammering the GitHub API) but NOT\n" +
			"the mode field — `ww update` is an explicit request, so it\n" +
			"runs even when mode=off.",
		Args: cobra.NoArgs,
		// Opt out of PersistentPreRunE — this command doesn't need a
		// configured harness client and should work even when config
		// is broken.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		RunE: func(cc *cobra.Command, _ []string) error {
			if force && check {
				return fmt.Errorf("--force and --check are mutually exclusive")
			}
			return runUpdate(cc, force, check)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"skip the version check and run the upgrade unconditionally")
	cmd.Flags().BoolVar(&check, "check", false,
		"check for a newer release without running the upgrade")
	return cmd
}

// runUpdate is the core of `ww update` — extracted from the cobra
// closure so tests can exercise the logic without synthesizing a
// Command tree.
func runUpdate(cc *cobra.Command, force, check bool) error {
	resolved, err := config.Load(rootConfigFlag(cc), config.FlagOverrides{}, os.Getenv)
	if err != nil {
		// Non-fatal — we can still detect install method and query
		// the default channel. Log the config issue and proceed.
		fmt.Fprintf(os.Stderr, "ww: warning: %v\n", err)
	}

	method := update.DetectInstallMethod(nil, nil)

	// --force bypasses the check entirely. The installer itself
	// decides whether anything needs doing (e.g. `brew upgrade`
	// no-ops when already at the latest tap version).
	if force {
		return update.RunUpgrade(context.Background(), method, Version, os.Stdout, os.Stderr)
	}

	// Default + --check: perform the channel query first.
	channel, _ := update.ParseChannel(resolved.Update.Channel)
	interval := resolveInterval(resolved.Update.Interval)
	checker := update.NewChecker(Version, channel, interval)

	// Slightly longer timeout than the background check — the user
	// is watching, so a 5s wait is fine if the network is slow.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	notice := checker.Check(ctx)

	if notice == nil {
		// Either we're on the latest, or the check failed silently
		// (GH API down, parse error). Either way, nothing to do
		// here — we don't claim the API answered definitively.
		fmt.Printf("ww %s — no newer release detected on the %s channel.\n",
			Version, channelLabel(channel))
		if method == update.InstallMethodBinary {
			fmt.Println("Install method: standalone binary.")
			fmt.Println("Downloads: https://github.com/skthomasjr/witwave/releases")
		} else if cmdHint := method.UpgradeCommand(); cmdHint != "" {
			fmt.Printf("Install method: %s. Manual re-check: %s\n", method, cmdHint)
		}
		return nil
	}

	fmt.Printf("Newer ww release available: %s (you're on %s).\n",
		notice.LatestTag, Version)
	fmt.Printf("  Release notes: %s\n", notice.LatestURL)

	if check {
		// --check: report only, don't upgrade.
		if instr := method.UpgradeCommand(); instr != "" {
			fmt.Printf("  To upgrade: %s\n", instr)
		} else {
			fmt.Println("  Standalone binary — download the new tarball from the URL above.")
		}
		return nil
	}

	// Default flow: run the upgrade. Empty line so the brew output
	// below doesn't visually mash against our banner.
	fmt.Println()
	return update.RunUpgrade(context.Background(), method, Version, os.Stdout, os.Stderr)
}

// resolveInterval turns the optional config string into a duration,
// falling back to the update package's default when the string is
// empty, non-positive, or unparseable. A bad interval value is never
// fatal here — we'd rather run the check with a sane default than
// refuse to help the user upgrade because their config has a typo.
func resolveInterval(s string) time.Duration {
	if s == "" {
		return update.DefaultInterval
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return update.DefaultInterval
	}
	return d
}

// channelLabel returns a human-readable label for the UX. Exists so
// the "no newer release" message reads naturally.
func channelLabel(c update.Channel) string {
	switch c {
	case update.ChannelBeta:
		return "beta"
	default:
		return "stable"
	}
}

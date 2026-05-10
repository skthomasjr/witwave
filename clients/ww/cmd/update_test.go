// Tests for the pure-helper functions in update.go that the
// `ww update` subcommand and the root PreRun-hook background-checker
// both compose over user config. Mirrors the table-driven shape
// used in snapshot_test.go and validate_test.go; the GitHub-Releases
// fetch and installer-shell-out paths are exercised against real
// services, so this file just pins the helper contracts.
package cmd

import (
	"testing"
	"time"

	"github.com/witwave-ai/witwave/clients/ww/internal/update"
)

// TestResolveInterval pins the config-string→duration helper used by
// the update-checker scheduler. Contract:
//   - empty string → DefaultInterval (24h, see update.DefaultInterval)
//   - unparseable string → DefaultInterval (never fatal — see comment)
//   - non-positive parsed duration → DefaultInterval
//   - well-formed positive duration → returned verbatim
//
// The "never fatal on a bad config string" guarantee is load-bearing:
// a typo in user config must not block the upgrade nudge. Pin every
// branch so a future tightening (say, return error on unparseable) is
// a deliberate decision rather than a quiet drift.
func TestResolveInterval(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty string returns default", "", update.DefaultInterval},
		{"unparseable string returns default", "not-a-duration", update.DefaultInterval},
		{"zero duration returns default", "0s", update.DefaultInterval},
		{"negative duration returns default", "-5m", update.DefaultInterval},
		{"valid 1h returns 1h", "1h", time.Hour},
		{"valid 30m returns 30m", "30m", 30 * time.Minute},
		{"valid 7d-shaped 168h returns 168h", "168h", 168 * time.Hour},
		{"valid 500ms returns 500ms", "500ms", 500 * time.Millisecond},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := resolveInterval(tc.in)
			if got != tc.want {
				t.Errorf("resolveInterval(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestChannelLabel pins the human-readable channel-display helper used
// in the "no newer release" / "upgrading from <ver> on <channel>" UX
// strings. Contract: ChannelBeta → "beta"; everything else (including
// ChannelStable, the empty Channel, and any future-typo Channel value)
// → "stable". The default-stable fallback is intentional — an unknown
// channel string should never crash the UX; "stable" is the safe
// default. Pin so a future tighter validation is deliberate.
func TestChannelLabel(t *testing.T) {
	cases := []struct {
		name string
		in   update.Channel
		want string
	}{
		{"stable channel returns stable", update.ChannelStable, "stable"},
		{"beta channel returns beta", update.ChannelBeta, "beta"},
		{"empty channel returns stable (default)", update.Channel(""), "stable"},
		{"unknown channel returns stable (default)", update.Channel("nightly"), "stable"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := channelLabel(tc.in)
			if got != tc.want {
				t.Errorf("channelLabel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

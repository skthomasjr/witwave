// Tests for the pure-helper functions in list.go that the `ww agent
// list` renderer composes over k8s data. Mirrors the table-driven
// shape used in internal/operator/events_test.go (TestAgeOrExact)
// and internal/operator/snapshot_test.go (TestFormatTime); the
// k8s-backed ListAgents path is exercised against a real cluster,
// so this file just pins the pure age-formatter contract so a
// future tweak to the duration thresholds (or the unit suffix
// strings) fails loudly rather than silently shifting the column
// content for every cluster operator running `ww agent list`.
package agent

import (
	"testing"
	"time"
)

// TestFormatAge pins the `Age` column formatter. Contract:
//   - zero time → "-" sentinel (the CR somehow had no creation TS).
//   - d <  1m → "<n>s" (integer seconds, truncated).
//   - d <  1h → "<n>m" (integer minutes, truncated).
//   - d < 24h → "<n>h" (integer hours, truncated).
//   - d ≥ 24h → "<n>d" (integer days, truncated).
//
// Future-clock-skew (now - t < 0) currently flows through the
// "<n>s" branch as a negative integer; callers are k8s-server
// timestamps so the case is exotic but allowed — pinning behaviour
// rather than asserting a specific sign here so a CR scheduled
// fractions of a second in the future doesn't surprise the table
// renderer (the column truncates to "0s" for the typical case).
//
// Drift here would change every `ww agent list` row's Age column.
func TestFormatAge(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero time returns dash sentinel", time.Time{}, "-"},
		{"just-now (30s) renders as seconds", now.Add(-30 * time.Second), "30s"},
		{"sub-minute boundary (59s) stays in seconds", now.Add(-59 * time.Second), "59s"},
		{"exactly one minute renders as minutes", now.Add(-1 * time.Minute), "1m"},
		{"mid-hour (45m) renders as minutes", now.Add(-45 * time.Minute), "45m"},
		{"sub-hour boundary (59m) stays in minutes", now.Add(-59 * time.Minute), "59m"},
		{"exactly one hour renders as hours", now.Add(-1 * time.Hour), "1h"},
		{"mid-day (12h) renders as hours", now.Add(-12 * time.Hour), "12h"},
		{"sub-day boundary (23h) stays in hours", now.Add(-23 * time.Hour), "23h"},
		{"exactly one day renders as days", now.Add(-24 * time.Hour), "1d"},
		{"multi-day (7d) renders as days", now.Add(-7 * 24 * time.Hour), "7d"},
		{"long-lived agent (90d) still renders as days", now.Add(-90 * 24 * time.Hour), "90d"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := FormatAge(tc.in)
			if got != tc.want {
				t.Errorf("FormatAge(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

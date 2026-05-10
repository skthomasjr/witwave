// Tests for the pure-helper functions in tail.go that govern the
// `ww tail` reconnect / backoff loop. Mirrors the table-driven shape
// used in validate_test.go and agent_test.go; the SSE / HTTP path is
// exercised end-to-end against a real harness, here we just pin the
// backoff bounds so a future tweak to the cap or jitter percentage
// is a deliberate decision rather than silent behaviour drift.
package cmd

import (
	"testing"
	"time"
)

// TestNextBackoff covers the exponential backoff with ±25% jitter
// that `ww tail`'s reconnect loop uses between disconnect and the
// next dial. The contract documented at the call-site:
//
//   - next = d * 2, capped at 10 * time.Second
//   - jitter = uniform-random in [0, next/2)
//   - return = next/2 + jitter
//
// So output ∈ [next/2, next). We assert bounds across many samples
// rather than exact values (because the jitter is randomized).
func TestNextBackoff(t *testing.T) {
	cases := []struct {
		name        string
		input       time.Duration
		wantNextLow time.Duration // lower bound (inclusive) — next/2
		wantNextHi  time.Duration // upper bound (exclusive) — next
	}{
		{"100ms doubles to 200ms with jitter", 100 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond},
		{"500ms doubles to 1s with jitter", 500 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second},
		{"1s doubles to 2s with jitter", 1 * time.Second, 1 * time.Second, 2 * time.Second},
		{"3s doubles to 6s with jitter", 3 * time.Second, 3 * time.Second, 6 * time.Second},
		{"5s doubles to 10s with jitter (at cap edge)", 5 * time.Second, 5 * time.Second, 10 * time.Second},
		{"6s caps at 10s (would be 12s)", 6 * time.Second, 5 * time.Second, 10 * time.Second},
		{"10s caps at 10s (would be 20s)", 10 * time.Second, 5 * time.Second, 10 * time.Second},
		{"30s caps at 10s (would be 60s)", 30 * time.Second, 5 * time.Second, 10 * time.Second},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Sample several times to defend against a single
			// random-jitter draw landing right at a boundary; if
			// the bounds are wrong, repeated samples will surface
			// it consistently.
			for i := 0; i < 16; i++ {
				got := nextBackoff(tc.input)
				if got < tc.wantNextLow || got >= tc.wantNextHi {
					t.Errorf("nextBackoff(%v) sample %d = %v, want in [%v, %v)",
						tc.input, i, got, tc.wantNextLow, tc.wantNextHi)
				}
			}
		})
	}
}

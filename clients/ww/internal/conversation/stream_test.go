// Tests for the pure timestamp formatters in stream.go. The SSE
// streaming path itself (StreamSession) is exercised end-to-end
// against a real backend; the formatters live in the rendering layer
// and are amenable to table-driven tests. Mirrors the table-driven
// shape used in client_test.go.
package conversation

import "testing"

// TestFormatTS pins the human-readable RFC3339Nano-to-UTC formatter
// used by the conversation renderer. The contract has three branches:
// empty-input passthrough, valid-RFC3339 → "YYYY-MM-DD HH:MM:SS" in
// UTC, malformed input passthrough (so a diagnostic value the user
// might still find useful is never silently dropped).
func TestFormatTS(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty passes through", "", ""},
		{"Z-suffix UTC", "2026-05-11T04:30:15Z", "2026-05-11 04:30:15"},
		{"Z-suffix with nanos truncated", "2026-05-11T04:30:15.123456789Z", "2026-05-11 04:30:15"},
		{"non-UTC offset normalised to UTC", "2026-05-11T06:30:15+02:00", "2026-05-11 04:30:15"},
		{"negative offset normalised to UTC", "2026-05-10T23:30:15-05:00", "2026-05-11 04:30:15"},
		{"malformed passes through unchanged", "not-a-timestamp", "not-a-timestamp"},
		{"date-only passes through unchanged", "2026-05-11", "2026-05-11"},
		{"epoch-zero formatted", "1970-01-01T00:00:00Z", "1970-01-01 00:00:00"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := FormatTS(tc.in)
			if got != tc.want {
				t.Errorf("FormatTS(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFormatTSCompact pins the time-only renderer used by the table
// formatter when caller has confirmed every timestamp is same-day.
// Same three-branch contract as FormatTS (empty, valid, malformed)
// but the output is just HH:MM:SS in UTC. Pin each branch so a
// future format-string tweak fails the test rather than silently
// shifting the rendered column width.
func TestFormatTSCompact(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty passes through", "", ""},
		{"Z-suffix UTC", "2026-05-11T04:30:15Z", "04:30:15"},
		{"Z-suffix with nanos truncated", "2026-05-11T04:30:15.987654Z", "04:30:15"},
		{"non-UTC offset normalised to UTC", "2026-05-11T06:30:15+02:00", "04:30:15"},
		{"negative offset crossing midnight", "2026-05-10T23:30:15-05:00", "04:30:15"},
		{"malformed passes through unchanged", "not-a-timestamp", "not-a-timestamp"},
		{"date-only passes through unchanged", "2026-05-11", "2026-05-11"},
		{"epoch-zero renders 00:00:00", "1970-01-01T00:00:00Z", "00:00:00"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := FormatTSCompact(tc.in)
			if got != tc.want {
				t.Errorf("FormatTSCompact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

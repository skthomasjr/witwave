// Tests for the pure-helper functions in snapshot.go that the
// `ww jobs/tasks/triggers/continuations list|view` commands compose
// over `ww` snapshot fetches. Mirrors the table-driven shape used in
// validate_test.go and agent_test.go.
package cmd

import "testing"

// TestFirstNonEmpty pins the variadic-args first-non-empty fallback
// helper used pervasively in snapshot rendering to choose a display
// label when multiple alias keys may exist for the same field
// (e.g. "name" / "id" / "displayName"). Drift here would silently
// change which key wins when more than one is set.
func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"zero args returns empty", nil, ""},
		{"single empty returns empty", []string{""}, ""},
		{"all empty returns empty", []string{"", "", ""}, ""},
		{"single non-empty returns it", []string{"foo"}, "foo"},
		{"first non-empty wins", []string{"foo", "bar"}, "foo"},
		{"first non-empty after empties wins", []string{"", "", "first-real", "second"}, "first-real"},
		{"single-space is non-empty", []string{"", " ", "x"}, " "},
		{"multibyte first wins", []string{"", "café"}, "café"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := firstNonEmpty(tc.in...)
			if got != tc.want {
				t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

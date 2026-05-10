// Tests for the pure-helper functions in conversation.go that every
// `ww conversation list` / `ww conversation show` invocation exercises.
// Mirrors the table-driven shape used in validate_test.go and
// internal/output/output_test.go; the HTTP / k8s paths are exercised
// end-to-end against a real cluster, so this file just pins the helper
// contracts so a future rename or off-by-one in the truncation /
// filter logic fails loudly instead of silently changing what users
// see in the table output.
package cmd

import (
	"reflect"
	"testing"

	"github.com/witwave-ai/witwave/clients/ww/internal/conversation"
)

// TestShortSessionID covers the 8-char session-id truncation used in
// the `ww conversation list` table column and the live-fan-out
// session-prefix display. The full id is still printed in the boxed
// transcript header (--expand) so users can copy-paste; this helper
// only governs the table-cell rendering. Pin the boundary cases so a
// future tweak (say, 7 chars or 12 chars to match a different short
// convention) updates both the helper and these expectations together.
func TestShortSessionID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty string returns empty", "", ""},
		{"shorter than 8 returns input verbatim", "abc", "abc"},
		{"exactly 7 returns input verbatim", "1234567", "1234567"},
		{"exactly 8 returns full input", "12345678", "12345678"},
		{"9 chars returns first 8", "123456789", "12345678"},
		{"UUID-shaped 36 chars returns first 8", "550e8400-e29b-41d4-a716-446655440000", "550e8400"},
		{"UUID with leading dashes returns first 8 incl dashes", "----abcdefgh", "----abcd"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := shortSessionID(tc.in)
			if got != tc.want {
				t.Errorf("shortSessionID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFilterTargetsByAgent pins the conversation fan-out filter that
// narrows a multi-agent target list down to the one named agent. The
// `ww conversation list --agent <name>` flow runs every result through
// this filter before fan-out HTTP probes — getting it wrong (case mix,
// substring match, etc.) silently shows results from the wrong agents.
// Pin equality semantics here so a future "case-insensitive lookup"
// or "substring contains" rewrite is a deliberate decision rather
// than a quiet drift.
func TestFilterTargetsByAgent(t *testing.T) {
	tA := conversation.AgentTarget{Agent: "claude"}
	tB := conversation.AgentTarget{Agent: "codex"}
	tC := conversation.AgentTarget{Agent: "claude"}
	tD := conversation.AgentTarget{Agent: "Claude"} // different case

	cases := []struct {
		name    string
		targets []conversation.AgentTarget
		filter  string
		want    []conversation.AgentTarget
	}{
		{"empty input returns empty slice", nil, "claude", []conversation.AgentTarget{}},
		{"no match returns empty slice", []conversation.AgentTarget{tA, tB}, "gemini", []conversation.AgentTarget{}},
		{"single match returns just it", []conversation.AgentTarget{tA, tB}, "claude", []conversation.AgentTarget{tA}},
		{"multiple matches returns all in order", []conversation.AgentTarget{tA, tB, tC}, "claude", []conversation.AgentTarget{tA, tC}},
		{"match is case-sensitive (Claude != claude)", []conversation.AgentTarget{tA, tD}, "claude", []conversation.AgentTarget{tA}},
		{"empty filter matches no entries with non-empty agents", []conversation.AgentTarget{tA, tB}, "", []conversation.AgentTarget{}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := filterTargetsByAgent(tc.targets, tc.filter)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filterTargetsByAgent(%v, %q) = %v, want %v", tc.targets, tc.filter, got, tc.want)
			}
		})
	}
}

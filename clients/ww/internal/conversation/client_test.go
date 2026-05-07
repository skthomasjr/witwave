package conversation

import (
	"testing"
)

// Tests for the pure-logic helpers in client.go. The HTTP path itself
// is exercised end-to-end against a real cluster; here we cover the
// slicing + summarisation + inference logic that's amenable to
// table-driven tests.

func ptrStr(s string) *string { return &s }

func TestFilterSessionMatches(t *testing.T) {
	in := []Entry{
		{TS: "2026-05-07T10:00:00Z", Agent: "evan", SessionID: "a"},
		{TS: "2026-05-07T10:01:00Z", Agent: "evan", SessionID: "b"},
		{TS: "2026-05-07T10:02:00Z", Agent: "evan", SessionID: "a"},
		{TS: "2026-05-07T10:03:00Z", Agent: "kira", SessionID: "a"},
	}
	got := FilterSession(in, "a")
	if len(got) != 3 {
		t.Fatalf("expected 3 matches for session a, got %d", len(got))
	}
	if got[0].SessionID != "a" || got[2].Agent != "kira" {
		t.Errorf("filter ordering changed unexpectedly: %v", got)
	}
}

func TestFilterSessionEmpty(t *testing.T) {
	if got := FilterSession(nil, "a"); len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %v", got)
	}
	if got := FilterSession([]Entry{{SessionID: "b"}}, "a"); len(got) != 0 {
		t.Errorf("expected empty result for non-matching id, got %v", got)
	}
}

func TestSummarizeGroupsByAgentAndSession(t *testing.T) {
	in := []Entry{
		{TS: "2026-05-07T10:00:00Z", Agent: "evan", SessionID: "a", Role: "user"},
		{TS: "2026-05-07T10:01:00Z", Agent: "evan", SessionID: "a", Role: "agent"},
		{TS: "2026-05-07T10:02:00Z", Agent: "evan", SessionID: "b", Role: "user"},
		{TS: "2026-05-07T11:00:00Z", Agent: "kira", SessionID: "a", Role: "user"},
	}
	got := Summarize(in, "witwave-self")
	if len(got) != 3 {
		t.Fatalf("expected 3 summaries (evan/a, evan/b, kira/a), got %d", len(got))
	}
	// First in result should be most-recent activity → kira/a (11:00).
	if got[0].Agent != "kira" || got[0].SessionID != "a" {
		t.Errorf("expected kira/a first by last-activity desc, got %s/%s", got[0].Agent, got[0].SessionID)
	}
	// Each summary should carry the namespace from the caller.
	for _, s := range got {
		if s.Namespace != "witwave-self" {
			t.Errorf("expected namespace witwave-self, got %s", s.Namespace)
		}
	}
	// Find evan/a summary; should have 2 turns and span 10:00 → 10:01.
	for _, s := range got {
		if s.Agent == "evan" && s.SessionID == "a" {
			if s.Turns != 2 {
				t.Errorf("evan/a turns = %d, expected 2", s.Turns)
			}
			if s.Started != "2026-05-07T10:00:00Z" || s.LastActivity != "2026-05-07T10:01:00Z" {
				t.Errorf("evan/a span wrong: started=%s, last=%s", s.Started, s.LastActivity)
			}
		}
	}
}

func TestSummarizeIgnoresMissingSessionID(t *testing.T) {
	in := []Entry{
		{TS: "2026-05-07T10:00:00Z", Agent: "evan", SessionID: "", Role: "user"},
		{TS: "2026-05-07T10:01:00Z", Agent: "evan", SessionID: "a", Role: "user"},
	}
	got := Summarize(in, "ns")
	if len(got) != 1 {
		t.Fatalf("expected 1 summary (the empty SessionID skipped), got %d", len(got))
	}
}

func TestInferSourceHeartbeat(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"heartbeat phrase", "Heartbeat check. Follow these instructions: ...", "heartbeat"},
		{"run-your phrase", "Run your dispatch-team skill...", "heartbeat"},
		{"a2a fallback", "fix bugs in operator", "a2a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Entry{Role: "user", Text: ptrStr(tc.text)}
			if got := inferSource(e); got != tc.want {
				t.Errorf("inferSource(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestInferSourceEmpty(t *testing.T) {
	e := &Entry{Role: "agent"}
	if got := inferSource(e); got != "" {
		t.Errorf("inferSource(no text, agent role) = %q, want empty", got)
	}
}

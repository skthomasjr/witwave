package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
	"github.com/witwave-ai/witwave/clients/ww/internal/conversation"
)

func TestParseTeamStatusSince(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"1h", time.Hour},
		{"4h", 4 * time.Hour},
		{"12h", 12 * time.Hour},
		{"24h", 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"day", 24 * time.Hour},
		{"2days", 48 * time.Hour},
	}
	for _, tc := range tests {
		got, err := parseTeamStatusSince(tc.in)
		if err != nil {
			t.Fatalf("parseTeamStatusSince(%q) returned error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("parseTeamStatusSince(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestTeamStatusWatchFlagsRegistered(t *testing.T) {
	cmd := newTeamStatusCmd(&teamFlags{})
	watch := cmd.Flags().Lookup("watch")
	if watch == nil {
		t.Fatal("--watch flag is not registered")
	}
	if watch.Shorthand != "w" {
		t.Fatalf("--watch shorthand = %q, want w", watch.Shorthand)
	}
	interval := cmd.Flags().Lookup("interval")
	if interval == nil {
		t.Fatal("--interval flag is not registered")
	}
	if interval.DefValue != "10s" {
		t.Fatalf("--interval default = %q, want 10s", interval.DefValue)
	}
}

func TestBuildTeamStatusRowsAggregatesPerAgent(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	tok1000 := 1000
	tok2500 := 2500
	agents := []agent.AgentSummary{
		{Namespace: "witwave", Name: "zora", Phase: "Ready", Ready: 1, Backends: []string{"claude", "codex"}},
		{Namespace: "witwave", Name: "iris", Phase: "Ready", Ready: 1, Backends: []string{"codex"}},
		{Namespace: "witwave", Name: "piper", Phase: "Ready", Ready: 1, Backends: []string{"claude", "codex"}},
		{Namespace: "witwave", Name: "fred", Phase: "Ready", Ready: 1, Backends: []string{"echo"}},
		{Namespace: "witwave", Name: "nova", Phase: "Reconciling", Ready: 0, Backends: []string{"claude"}},
	}
	results := []conversation.FanOutResult{
		{
			Target: conversation.AgentTarget{Namespace: "witwave", Agent: "zora"},
			Entries: []conversation.Entry{
				{TS: now.Add(-30 * time.Minute).Format(time.RFC3339), Agent: "zora", SessionID: "s1", Role: "user", Tokens: &tok1000},
				{TS: now.Add(-20 * time.Minute).Format(time.RFC3339), Agent: "zora", SessionID: "s1", Role: "assistant"},
				{TS: now.Add(-2 * time.Minute).Format(time.RFC3339), Agent: "zora", SessionID: "s2", Role: "assistant", Tokens: &tok2500},
			},
		},
		{
			Target: conversation.AgentTarget{Namespace: "witwave", Agent: "iris"},
			Entries: []conversation.Entry{
				{TS: now.Add(-45 * time.Minute).Format(time.RFC3339), Agent: "iris", SessionID: "s3", Role: "assistant"},
			},
		},
		{Target: conversation.AgentTarget{Namespace: "witwave", Agent: "piper"}},
		{
			Target: conversation.AgentTarget{Namespace: "witwave", Agent: "fred"},
			Err:    errors.New("port-forward failed"),
		},
	}

	rows, unreachable := buildTeamStatusRows(agents, results, teamStatusBuildOptions{
		Now:    now,
		Window: time.Hour,
	})

	if len(rows) != len(agents) {
		t.Fatalf("got %d rows, want %d", len(rows), len(agents))
	}
	assertRow := func(idx int, agentName, state string) teamStatusRow {
		t.Helper()
		row := rows[idx]
		if row.Agent != agentName || row.State != state {
			t.Fatalf("rows[%d] = %s/%s, want %s/%s", idx, row.Agent, row.State, agentName, state)
		}
		return row
	}
	zora := assertRow(0, "zora", "RECENT")
	if zora.Sessions != 2 || zora.Turns != 3 || zora.Tokens != 3500 {
		t.Fatalf("zora aggregate = sessions %d turns %d tokens %d, want 2/3/3500",
			zora.Sessions, zora.Turns, zora.Tokens)
	}
	if zora.LastTurn != "2m ago" {
		t.Fatalf("zora LastTurn = %q, want 2m ago", zora.LastTurn)
	}
	if zora.Activity == "[------------]" {
		t.Fatalf("zora Activity should show at least one active bucket")
	}
	assertRow(1, "iris", "QUIET")
	assertRow(2, "piper", "IDLE")
	fred := assertRow(3, "fred", "UNKNOWN")
	if fred.Note != "conversation read failed" {
		t.Fatalf("fred Note = %q, want conversation read failed", fred.Note)
	}
	nova := assertRow(4, "nova", "OFFLINE")
	if !strings.Contains(nova.Note, "phase=Reconciling") {
		t.Fatalf("nova Note = %q, want phase detail", nova.Note)
	}
	if len(unreachable) != 1 || unreachable[0].Agent != "fred" {
		t.Fatalf("unreachable = %#v, want fred only", unreachable)
	}
}

func TestRenderTeamStatusTable(t *testing.T) {
	rows := []teamStatusRow{
		{
			Namespace: "witwave",
			Agent:     "zora",
			State:     "RECENT",
			Backends:  []string{"claude", "codex"},
			LastTurn:  "2m ago",
			Sessions:  2,
			Turns:     3,
			Tokens:    3500,
			Activity:  "[-----#-#]",
		},
	}
	var buf bytes.Buffer
	renderTeamStatusTable(&buf, rows, true)
	got := buf.String()
	for _, want := range []string{
		"NAMESPACE",
		"AGENT",
		"witwave",
		"zora",
		"claude,codex",
		"3.5k",
		"[-----#-#]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered table missing %q:\n%s", want, got)
		}
	}
}

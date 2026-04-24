package agent

import (
	"context"
	"strings"
	"testing"
)

func TestTeamJoin_FromUngroupedToTeam(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamJoin(context.Background(), nil, TeamJoinOptions{
		Agent:     "hello",
		Namespace: "default",
		Team:      "alpha",
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	if got := updated.GetLabels()[TeamLabel]; got != "alpha" {
		t.Errorf("label = %q; want %q", got, "alpha")
	}
	mustContain(t, out.String(), `now in team "alpha"`)
	mustContain(t, out.String(), "ungrouped")
}

func TestTeamJoin_MoveBetweenTeams(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	cr.SetLabels(map[string]string{TeamLabel: "alpha"})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamJoin(context.Background(), nil, TeamJoinOptions{
		Agent:     "hello",
		Namespace: "default",
		Team:      "bravo",
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	if got := updated.GetLabels()[TeamLabel]; got != "bravo" {
		t.Errorf("label = %q; want %q", got, "bravo")
	}
	// Banner must call out the transition so users see they're moving, not joining.
	mustContain(t, out.String(), `was:  team="alpha"`)
	mustContain(t, out.String(), `now:  team="bravo"`)
}

func TestTeamJoin_Idempotent(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	cr.SetLabels(map[string]string{TeamLabel: "alpha"})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamJoin(context.Background(), nil, TeamJoinOptions{
		Agent:     "hello",
		Namespace: "default",
		Team:      "alpha",
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Already in team")
}

func TestTeamJoin_DryRun_DoesNotPatch(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := TeamJoin(context.Background(), nil, TeamJoinOptions{
		Agent:     "hello",
		Namespace: "default",
		Team:      "alpha",
		DryRun:    true,
		Out:       captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	if _, ok := updated.GetLabels()[TeamLabel]; ok {
		t.Error("dry-run should not have patched the label")
	}
}

func TestTeamJoin_RejectsInvalidTeamName(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := TeamJoin(context.Background(), nil, TeamJoinOptions{
		Agent:     "hello",
		Namespace: "default",
		Team:      "Bad-Team-UPPER",
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected an error for non-DNS-1123 team name")
	}
}

func TestTeamLeave_DropsLabel(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	cr.SetLabels(map[string]string{TeamLabel: "alpha"})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamLeave(context.Background(), nil, TeamLeaveOptions{
		Agent:     "hello",
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	if _, ok := updated.GetLabels()[TeamLabel]; ok {
		t.Error("leave should have removed the team label")
	}
	mustContain(t, out.String(), `left team "alpha"`)
}

func TestTeamLeave_AlreadyUngrouped(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamLeave(context.Background(), nil, TeamLeaveOptions{
		Agent:     "hello",
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "already ungrouped")
}

func TestTeamList_AllTeams(t *testing.T) {
	// Three agents: two in "alpha", one ungrouped.
	a := seedAgent("a", "default", nil)
	a.SetLabels(map[string]string{TeamLabel: "alpha"})
	b := seedAgent("b", "default", nil)
	b.SetLabels(map[string]string{TeamLabel: "alpha"})
	c := seedAgent("c", "default", nil)
	dyn := makeFakeDynamic(a, b, c)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamList(context.Background(), nil, TeamListOptions{
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := out.String()
	mustContain(t, s, "alpha (2)")
	mustContain(t, s, "(ungrouped) (1)")
	// Sorted alpha-then-ungrouped is lexical "(" < "a": parens sort first.
	if !strings.Contains(s, "(ungrouped)") || !strings.Contains(s, "alpha") {
		t.Errorf("both group headers must be present:\n%s", s)
	}
}

func TestTeamList_FilterByTeam(t *testing.T) {
	a := seedAgent("a", "default", nil)
	a.SetLabels(map[string]string{TeamLabel: "alpha"})
	b := seedAgent("b", "default", nil)
	b.SetLabels(map[string]string{TeamLabel: "bravo"})
	dyn := makeFakeDynamic(a, b)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamList(context.Background(), nil, TeamListOptions{
		Namespace: "default",
		Team:      "alpha",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), `Team "alpha"`)
	mustContain(t, out.String(), "- a")
	mustNotContain(t, out.String(), "- b")
}

func TestTeamList_FilterByTeam_NoMembers(t *testing.T) {
	a := seedAgent("a", "default", nil)
	a.SetLabels(map[string]string{TeamLabel: "alpha"})
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamList(context.Background(), nil, TeamListOptions{
		Namespace: "default",
		Team:      "ghost",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), `No agents in team "ghost"`)
}

func TestTeamShow_WithTeammates(t *testing.T) {
	a := seedAgent("a", "default", nil)
	a.SetLabels(map[string]string{TeamLabel: "alpha"})
	b := seedAgent("b", "default", nil)
	b.SetLabels(map[string]string{TeamLabel: "alpha"})
	c := seedAgent("c", "default", nil)
	c.SetLabels(map[string]string{TeamLabel: "bravo"})
	dyn := makeFakeDynamic(a, b, c)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamShow(context.Background(), nil, TeamShowOptions{
		Agent:     "a",
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := out.String()
	mustContain(t, s, `in team "alpha"`)
	mustContain(t, s, "Teammates (1)")
	mustContain(t, s, "- b")
	mustNotContain(t, s, "- c")
}

func TestTeamShow_Ungrouped_TeammatesAreAllUngroupedPeers(t *testing.T) {
	// Two ungrouped agents share the namespace-wide manifest.
	a := seedAgent("a", "default", nil)
	b := seedAgent("b", "default", nil)
	// Plus one in a real team — should NOT show up as a peer of a.
	c := seedAgent("c", "default", nil)
	c.SetLabels(map[string]string{TeamLabel: "alpha"})
	dyn := makeFakeDynamic(a, b, c)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := TeamShow(context.Background(), nil, TeamShowOptions{
		Agent:     "a",
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := out.String()
	mustContain(t, s, "ungrouped")
	mustContain(t, s, "- b")
	mustNotContain(t, s, "- c")
}

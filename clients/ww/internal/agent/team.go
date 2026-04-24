package agent

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// TeamLabel is the CR label the operator watches for team membership.
// Agents sharing the same value are listed together in a per-team
// manifest ConfigMap (operator/internal/controller/witwaveagent_resources.go
// `teamLabel`). Kept in sync here so the CLI can patch the same key the
// operator reconciles on — a drift between the two would silently break
// membership changes.
const TeamLabel = "witwave.ai/team"

// TeamJoinOptions are inputs to `ww agent team join`.
type TeamJoinOptions struct {
	Agent     string
	Namespace string
	Team      string

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// TeamJoin sets the `witwave.ai/team=<team>` label on a WitwaveAgent.
// Idempotent: re-setting to the same value is a no-op with a clear log
// line so the user isn't surprised. Moving between teams (already
// labelled, different value) is allowed and reported explicitly.
func TeamJoin(ctx context.Context, cfg *rest.Config, opts TeamJoinOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("TeamJoinOptions.Out is required")
	}
	if err := ValidateName(opts.Agent); err != nil {
		return err
	}
	if err := ValidateName(opts.Team); err != nil {
		return fmt.Errorf("team name %q: %w", opts.Team, err)
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Agent)
	if err != nil {
		return err
	}

	currentTeam := cr.GetLabels()[TeamLabel]

	// Plan banner describes the transition. Same shape used across the
	// other mutating verbs so CI logs stay scannable.
	fmt.Fprintf(opts.Out, "\nAction:    join team %q on WitwaveAgent %q in %s\n",
		opts.Team, opts.Agent, opts.Namespace)
	switch {
	case currentTeam == opts.Team:
		fmt.Fprintf(opts.Out, "  Already in team %q — no change needed.\n", opts.Team)
	case currentTeam == "":
		fmt.Fprintf(opts.Out, "  was:  (ungrouped — in namespace-wide manifest)\n")
		fmt.Fprintf(opts.Out, "  now:  team=%q\n", opts.Team)
	default:
		fmt.Fprintf(opts.Out, "  was:  team=%q\n", currentTeam)
		fmt.Fprintf(opts.Out, "  now:  team=%q\n", opts.Team)
	}
	fmt.Fprintln(opts.Out, "  ConfigMap: operator will update witwave-manifest-"+opts.Team+
		" on next reconcile; members see the new peer within seconds.")

	if opts.DryRun {
		fmt.Fprintln(opts.Out, "Dry-run mode — no API calls made.")
		return nil
	}
	if currentTeam == opts.Team {
		return nil
	}

	labels := cr.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[TeamLabel] = opts.Team
	cr.SetLabels(labels)

	if _, err := updateAgentCR(ctx, dyn, cr); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s now in team %q.\n",
		opts.Namespace, opts.Agent, opts.Team)
	return nil
}

// TeamLeaveOptions are inputs to `ww agent team leave`.
type TeamLeaveOptions struct {
	Agent     string
	Namespace string

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// TeamLeave removes the witwave.ai/team label from a WitwaveAgent.
// The agent falls back into the namespace-wide manifest (the bucket
// every label-less agent shares). No-ops cleanly when the agent
// wasn't in a team to begin with.
func TeamLeave(ctx context.Context, cfg *rest.Config, opts TeamLeaveOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("TeamLeaveOptions.Out is required")
	}
	if err := ValidateName(opts.Agent); err != nil {
		return err
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Agent)
	if err != nil {
		return err
	}

	currentTeam := cr.GetLabels()[TeamLabel]
	fmt.Fprintf(opts.Out, "\nAction:    leave team on WitwaveAgent %q in %s\n",
		opts.Agent, opts.Namespace)
	if currentTeam == "" {
		fmt.Fprintln(opts.Out, "  Agent is already ungrouped — no change needed.")
		if opts.DryRun {
			fmt.Fprintln(opts.Out, "Dry-run mode — no API calls made.")
		}
		return nil
	}
	fmt.Fprintf(opts.Out, "  was:  team=%q\n", currentTeam)
	fmt.Fprintln(opts.Out, "  now:  (ungrouped — joins namespace-wide manifest)")

	if opts.DryRun {
		fmt.Fprintln(opts.Out, "Dry-run mode — no API calls made.")
		return nil
	}

	labels := cr.GetLabels()
	delete(labels, TeamLabel)
	cr.SetLabels(labels)

	if _, err := updateAgentCR(ctx, dyn, cr); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s left team %q.\n",
		opts.Namespace, opts.Agent, currentTeam)
	return nil
}

// TeamListOptions are inputs to `ww agent team list`.
type TeamListOptions struct {
	Namespace string

	// Team, when non-empty, filters the output to only that team's
	// members. Empty lists every team in the namespace.
	Team string

	Out io.Writer
}

// TeamList renders a tree of teams → members in the target namespace.
// Agents without the team label are bucketed under the synthetic
// "(ungrouped)" label so users can see what falls through to the
// namespace-wide manifest.
func TeamList(ctx context.Context, cfg *rest.Config, opts TeamListOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("TeamListOptions.Out is required")
	}
	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	list, err := dyn.Resource(GVR()).Namespace(opts.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}

	// team → [agent names]. The synthetic "(ungrouped)" key collects
	// label-less agents so the output is complete (you can't miss an
	// agent because it has no label).
	byTeam := make(map[string][]string, len(list.Items))
	for i := range list.Items {
		cr := &list.Items[i]
		team := cr.GetLabels()[TeamLabel]
		if team == "" {
			team = "(ungrouped)"
		}
		byTeam[team] = append(byTeam[team], cr.GetName())
	}

	// When --team is set, narrow to that team only.
	if opts.Team != "" {
		members, ok := byTeam[opts.Team]
		if !ok {
			fmt.Fprintf(opts.Out, "No agents in team %q in namespace %s.\n",
				opts.Team, opts.Namespace)
			return nil
		}
		sort.Strings(members)
		fmt.Fprintf(opts.Out, "Team %q in namespace %s:\n", opts.Team, opts.Namespace)
		for _, m := range members {
			fmt.Fprintf(opts.Out, "  - %s\n", m)
		}
		return nil
	}

	if len(byTeam) == 0 {
		fmt.Fprintf(opts.Out, "No WitwaveAgents in namespace %s.\n", opts.Namespace)
		return nil
	}

	teams := make([]string, 0, len(byTeam))
	for t := range byTeam {
		teams = append(teams, t)
	}
	sort.Strings(teams)
	fmt.Fprintf(opts.Out, "Teams in namespace %s:\n", opts.Namespace)
	for _, t := range teams {
		members := byTeam[t]
		sort.Strings(members)
		fmt.Fprintf(opts.Out, "  %s (%d)\n", t, len(members))
		for _, m := range members {
			fmt.Fprintf(opts.Out, "    - %s\n", m)
		}
	}
	return nil
}

// TeamShowOptions are inputs to `ww agent team show`.
type TeamShowOptions struct {
	Agent     string
	Namespace string
	Out       io.Writer
}

// TeamShow prints the team membership of a single agent plus the
// peers that share the same manifest. Reads the same labels the
// operator reconciles on, so what you see here is what the operator
// believes.
func TeamShow(ctx context.Context, cfg *rest.Config, opts TeamShowOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("TeamShowOptions.Out is required")
	}
	if err := ValidateName(opts.Agent); err != nil {
		return err
	}
	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Agent)
	if err != nil {
		return err
	}

	team := cr.GetLabels()[TeamLabel]

	// To compute peers we have to list the namespace. Without a team
	// label the "peers" are every other label-less agent.
	list, err := dyn.Resource(GVR()).Namespace(opts.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	peers := collectTeammates(list.Items, team, opts.Agent)

	if team == "" {
		fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s is ungrouped (namespace-wide manifest).\n",
			opts.Namespace, opts.Agent)
	} else {
		fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s is in team %q.\n",
			opts.Namespace, opts.Agent, team)
	}
	if len(peers) == 0 {
		fmt.Fprintln(opts.Out, "Teammates: (none — this is the only agent in the manifest)")
		return nil
	}
	fmt.Fprintf(opts.Out, "Teammates (%d):\n", len(peers))
	for _, p := range peers {
		fmt.Fprintf(opts.Out, "  - %s\n", p)
	}
	return nil
}

// collectTeammates returns the names of agents sharing the given team
// (or, when team is empty, every other label-less agent in the slice),
// excluding the agent named `self`. Returned sorted.
func collectTeammates(items []unstructured.Unstructured, team, self string) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		cr := &items[i]
		if cr.GetName() == self {
			continue
		}
		peerTeam := cr.GetLabels()[TeamLabel]
		if peerTeam != team {
			continue
		}
		out = append(out, cr.GetName())
	}
	sort.Strings(out)
	return out
}

// labelMapString returns a stable, human-readable "k=v, k=v" rendering
// of a label map. Unused by the verbs themselves but handy for future
// diagnostics (e.g. `ww agent team show` gaining a --verbose flag).
func labelMapString(m map[string]string) string {
	if len(m) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

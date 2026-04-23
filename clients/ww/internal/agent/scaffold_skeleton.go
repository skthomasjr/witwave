package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// skeletonFile is one file the scaffolder materialises. Paths are
// repo-relative; content is the exact byte string to write.
type skeletonFile struct {
	Path    string
	Content string
}

// buildSkeleton returns the minimum-viable file set for a freshly
// scaffolded agent. Shape:
//
//	.agents/[<group>/]<name>/
//	├── README.md
//	├── .witwave/
//	│   ├── backend.yaml          # routing — single backend, points at sidecar
//	│   └── HEARTBEAT.md          # hourly heartbeat, unless skel.NoHeartbeat
//	└── .<backend>/
//	    ├── agent-card.md         # A2A identity skeleton
//	    └── <BEHAVIOR>.md         # only for LLM-backed backends (claude/codex/gemini)
//
// Deliberately omits: `jobs/`, `tasks/`, `triggers/`, `continuations/`,
// `webhooks/`. Per DESIGN.md SUB-1..4 their absence is how an agent
// expresses "I don't use this feature yet" — we don't pre-create
// dormant subsystems for those.
//
// HEARTBEAT.md is a documented exception to that rule: we scaffold it
// on by default because a running agent that reports "HEARTBEAT_OK"
// on a schedule is the cheapest possible proof that the dispatch path,
// the backend sidecar, and the routing config all actually work. Users
// who genuinely want a heartbeat-free agent pass skel.NoHeartbeat = true
// (cobra: --no-heartbeat), which keeps the dormant-default posture for
// that subsystem.
func buildSkeleton(skel skeletonOpts) []skeletonFile {
	root := filepath.ToSlash(agentRepoRoot(skel.Name, skel.Group))
	port := BackendPort(0)

	files := []skeletonFile{
		{
			Path:    root + "/README.md",
			Content: renderAgentReadme(skel.Name, skel.Group, skel.Backend, !skel.NoHeartbeat),
		},
		{
			Path:    root + "/.witwave/backend.yaml",
			Content: renderBackendYAML(skel.Backend, port),
		},
		{
			Path:    root + "/." + skel.Backend + "/agent-card.md",
			Content: renderAgentCard(skel.Name, skel.Backend),
		},
	}

	if !skel.NoHeartbeat {
		files = append(files, skeletonFile{
			Path:    root + "/.witwave/HEARTBEAT.md",
			Content: renderHeartbeat(),
		})
	}

	// LLM backends carry a behavioural-instructions file that the
	// container mounts at /home/agent/.<backend>/. Echo has no SDK
	// and no tool loop — nothing to instruct.
	if behaviorName, ok := behaviorFileName(skel.Backend); ok {
		files = append(files, skeletonFile{
			Path:    root + "/." + skel.Backend + "/" + behaviorName,
			Content: renderBehaviorStub(skel.Name, skel.Backend),
		})
	}

	return files
}

// skeletonOpts is the narrow slice of ScaffoldOptions buildSkeleton
// cares about. Passing a struct (rather than five positional args)
// keeps call sites compact and lets new fields land without rippling
// through every helper.
type skeletonOpts struct {
	Name        string
	Group       string
	Backend     string
	CLIVersion  string
	NoHeartbeat bool
}

// agentRepoRoot returns the repo-relative directory for an agent,
// honouring the optional group segment. `group == ""` produces the
// flat `.agents/<name>/` layout (the default, per the product
// discussion); a group name nests one level deeper.
func agentRepoRoot(name, group string) string {
	if group == "" {
		return filepath.Join(".agents", name)
	}
	return filepath.Join(".agents", group, name)
}

// behaviorFileName returns the backend-specific behavioural-instructions
// filename, mirroring how the existing test agents are laid out:
// CLAUDE.md for claude, AGENTS.md for codex, GEMINI.md for gemini. The
// `ok` return is false for backends that don't carry such a file
// (today: only echo).
func behaviorFileName(backend string) (string, bool) {
	switch backend {
	case BackendClaude:
		return "CLAUDE.md", true
	case BackendCodex:
		return "AGENTS.md", true
	case BackendGemini:
		return "GEMINI.md", true
	case BackendEcho:
		return "", false
	}
	return "", false
}

// renderAgentReadme produces a one-screen README.md explaining what
// this agent is and what the file layout means. Intentionally terse —
// someone reading this while clicking around the repo should grasp the
// shape without any witwave context.
func renderAgentReadme(name, group, backend string, hasHeartbeat bool) string {
	var groupLine string
	if group != "" {
		groupLine = fmt.Sprintf("Group:   `%s`\n", group)
	}
	behaviourHint := ""
	if behaviorName, ok := behaviorFileName(backend); ok {
		behaviourHint = fmt.Sprintf(
			"`.%s/%s`        Behavioural instructions mounted into the backend container at\n"+
				"                         /home/agent/.%s/%s. Edit to change how the agent responds.\n\n",
			backend, behaviorName, backend, behaviorName,
		)
	}
	heartbeatLine := ""
	if hasHeartbeat {
		heartbeatLine = ".witwave/HEARTBEAT.md    Hourly heartbeat. Fires a prompt at the agent every hour\n" +
			"                         at minute 0. Edit the schedule (cron) or body, or delete\n" +
			"                         the file entirely to go silent.\n"
	}
	heartbeatBullet := ""
	if hasHeartbeat {
		heartbeatBullet = "- `HEARTBEAT.md` — recurring prompt the agent fires at itself on a schedule. *(Scaffold seeds one hourly; edit or delete as you like.)*\n"
	} else {
		heartbeatBullet = "- `HEARTBEAT.md` — recurring prompt the agent fires at itself on a schedule.\n"
	}
	return fmt.Sprintf(
		`# %s

WitwaveAgent configuration for `+"`%s`"+`, scaffolded by `+"`ww agent scaffold`"+`.

%sBackend: `+"`%s`"+`

This directory is gitSync-mounted into the running agent pod. When files
here change on the configured branch, the agent picks them up on the
next sync interval without a restart.

## Layout

`+"```"+`
.witwave/backend.yaml    Harness routing config. Points at the chosen backend
                         sidecar on its allocated port.
%s%s.%s/agent-card.md       A2A identity card returned from /.well-known/agent-card.json.
                         This is what other agents see when they discover this one.
`+"```"+`

## Next steps

- `+"`ww agent create %s`"+` — deploy on the cluster (if not already done).
- `+"`ww agent git add %s --repo <this-repo>`"+` — wire the running agent to pull from this directory.
- `+"`ww agent send %s \"hello\"`"+` — round-trip a test prompt.

Add scheduled work by dropping files under `+"`.witwave/`"+`:

%s- `+"`jobs/*.md`"+` — one-shot jobs fired when the agent boots.
- `+"`tasks/*.md`"+` — calendar-scheduled tasks (days, time window, date range).
- `+"`triggers/*.md`"+` — inbound HTTP trigger endpoints.
- `+"`continuations/*.md`"+` — follow-up prompts on upstream completion.
- `+"`webhooks/*.md`"+` — outbound webhook subscriptions.

Absence of any of these files means the agent does not use that feature.
The harness is quiet about dormant subsystems — file presence is the
enablement signal.
`,
		name, name, groupLine, backend, heartbeatLine, behaviourHint, backend, name, name, name, heartbeatBullet,
	)
}

// renderHeartbeat returns an hourly "HEARTBEAT_OK" heartbeat. Body is
// deliberately minimal — proving the dispatch path is cheaper than any
// prompt the scaffold could guess at. Users customise by editing; users
// who want no heartbeat at all scaffold with --no-heartbeat.
//
// Cron `0 * * * *` fires at the top of every hour. Matches what most
// users reach for when asked "schedule hourly" — easier to reason about
// than "every 3600 seconds from boot."
func renderHeartbeat() string {
	return `---
description: |
  Hourly heartbeat. Fires a prompt at the agent every hour at minute 0
  to exercise the dispatch path end-to-end and prove the backend sidecar
  is answering. Edit the schedule below, edit the body, or delete this
  file entirely to disable heartbeats.
schedule: "0 * * * *"
enabled: true
---

Respond HEARTBEAT_OK.
`
}

// renderAgentCard returns the A2A agent-card skeleton. Format mirrors
// the text the backends' load_agent_description helper already reads —
// a plain markdown document whose first paragraph(s) become the A2A
// card description.
func renderAgentCard(name, backend string) string {
	return fmt.Sprintf(
		`An autonomous agent named `+"`%s`"+`, running on the %s backend.

Edit this file to customise the A2A identity card that callers see at
`+"`/.well-known/agent-card.json`"+`. The full contents are surfaced verbatim as
the agent-card description.
`,
		name, backend,
	)
}

// renderBehaviorStub returns the behavioural-instructions stub for an
// LLM backend. Deliberately minimal — we don't want to bias the agent's
// personality, just give the user a clearly-marked place to add their
// own prompt.
func renderBehaviorStub(name, backend string) string {
	behaviorName, _ := behaviorFileName(backend)
	return fmt.Sprintf(
		`# %s

Behavioural instructions for `+"`%s`"+`. This file is loaded by the %s backend
at startup and injected as the agent's system prompt.

Edit freely. The harness reloads `+"`%s`"+` on change (via gitSync pulls)
without restarting the pod.

## Identity

You are %s, an autonomous agent. Replace this section with the persona,
tone, and goals you want the agent to inhabit.

## Capabilities

List the tools, MCP servers, and scheduled routines this agent has access
to. Keep this section honest — hallucinated capabilities are a common
failure mode when the agent reasons about what it can do.

## Constraints

Note any hard rules (e.g. "never write to /etc", "only answer questions
about this codebase") the agent should enforce before acting.
`,
		strings.ToUpper(backend[:1])+backend[1:],
		name, backend, behaviorName, name,
	)
}

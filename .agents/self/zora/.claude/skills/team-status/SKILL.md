---
name: team-status
description:
  Read team state and return a snapshot for the user. Per-peer health + backlog + last activity, recent releases,
  any open escalations. Read-only — does NOT make decisions or dispatch. Trigger when the user says "team status",
  "what's the team doing?", "status report", "who's working?".
version: 0.1.0
---

# team-status

Snapshot of the team's current state. Read-only. Use when a human asks; don't run this from the heartbeat (that's
`dispatch-team`'s job).

## Instructions

### 1. Read state (same as dispatch-team Step 2, but report-only)

Build the snapshot from:

- `git log` — recent commits, latest tag, commits since tag
- Each peer's `MEMORY.md` index + deferred-findings backlog count
- Recent CI workflow runs (`gh run list --branch main --limit 10`)
- Your own `decision_log.md` — recent dispatches and rationales
- Your own `pause_mode.flag` — are you in observation-only mode?

### 2. Format the response

Return a structured markdown block:

```markdown
## Team status — YYYY-MM-DD HH:MM UTC

**Mode:** <active | paused (observation-only)>

### Release state

- Latest tag: `vX.Y.Z` (cut HH:MM UTC, NN hours ago)
- Commits since tag: N (M `feat:`, K `fix:`, ...)
- CI on `main` HEAD: <green | red | in-flight (workflow-name)>
- Last release pipeline: <conclusion>
- Time until next release-warranted check: <auto-applied at next tick>

### Peers

| Peer  | Status   | Last fired (skill, time) | Open backlog | Notes                          |
| ----- | -------- | ------------------------ | ------------ | ------------------------------ |
| iris  | <ok|silent> | release, HH:MM           | n/a          | <any escalation>               |
| nova  | <ok|silent> | code-cleanup, HH:MM      | N            |                                |
| kira  | <ok|silent> | docs-cleanup, HH:MM      | N            |                                |
| evan  | <ok|silent> | bug-work, HH:MM          | N flagged    | <critical-finding count if >0> |

### Recent decisions (last 6 ticks)

- HH:MM — <dispatch/standdown/release/escalation> — <one-line rationale>
- HH:MM — ...

### Open escalations

- <none | one-line per escalation with link to memory entry>

### Hard-cap state

- Dispatches this hour: N / 5
- Releases today: N / 4
- Batch-reverts today: N / 3
```

### 3. Exit cleanly

Return only the snapshot. Do not dispatch any peer, do not change any memory file, do not invoke any other skill.
This is read-only.

## Out of scope for this skill

- **Decisions.** Read-only report. If the user wants you to act, they'll either invoke `dispatch-team` directly
  or wait for the next heartbeat.
- **Dispatching peers.** Same as above.
- **Changing the priority policy or cadence floors.** Policy is in CLAUDE.md; change happens via the user editing
  CLAUDE.md, not via this skill.

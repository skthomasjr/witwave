---
name: dispatch-team
description:
  Single decision-loop pass. Reads team state (git log, peer memories, CI status, peer health), applies the priority
  policy from CLAUDE.md, decides what (if anything) to dispatch this tick, dispatches via call-peer, logs decision
  rationale to memory. The main work skill — runs once per heartbeat. Trigger when the heartbeat fires (the harness's
  heartbeat scheduler invokes this) or when the user says "run a decision pass" / "do your thing" / "tick".
version: 0.1.0
---

# dispatch-team

One decision-loop pass. Run by the heartbeat scheduler every 30 minutes (v1).

## Inputs

None from the prompt. Read state from:

- `git log origin/main` (the canonical source of truth for what's landed)
- Peer `MEMORY.md` indexes + deferred-findings memory files
- Recent CI workflow runs (via shell-out to `gh run list` — read-only; iris owns the auth, but read on `main` is
  unauth-allowed for public repos)
- Your own memory: `decision_log.md` (your last decisions), `team_state.md` (last-fire times per peer)

## Instructions

### 1. Pause-mode check

If your `pause_mode.flag` file exists in your memory namespace, you're in observation-only mode (per CLAUDE.md → "Pause
control"). Read state, log what you WOULD have decided to `decision_log.md` with a `[paused: would-have]` prefix, then
exit. Do NOT dispatch.

```sh
test -f /workspaces/witwave-self/memory/agents/zora/pause_mode.flag && echo "PAUSED"
```

### 2. Read team state (every tick)

Build a current snapshot:

#### 2a. Git state

```sh
LATEST_TAG=$(git -C <checkout> describe --tags --abbrev=0 2>/dev/null)
COMMITS_SINCE_TAG=$(git -C <checkout> rev-list --count "${LATEST_TAG}..origin/main" 2>/dev/null)
LAST_RELEASE_TIME=$(git -C <checkout> log -1 --format=%cI "${LATEST_TAG}" 2>/dev/null)
LAST_COMMIT_TIME=$(git -C <checkout> log -1 --format=%cI origin/main 2>/dev/null)
```

#### 2b. CI state on main HEAD

```sh
gh run list --branch main --limit 5 --json name,status,conclusion,headSha
```

Filter to runs whose `headSha` matches `git rev-parse origin/main`. If any are still `in_progress`, note the CI as
"settling." If any are red and concluded, note as "red on main."

#### 2c. Peer memories

For each peer in `[iris, nova, kira, evan]`:

```sh
PEER_MEMORY=/workspaces/witwave-self/memory/agents/<peer>/MEMORY.md
PEER_FINDINGS=/workspaces/witwave-self/memory/agents/<peer>/project_*_findings.md
```

Read the index. From each peer's deferred-findings file, count `[flagged: ...]` markers (open backlog) and look for any
`[CRITICAL]` severity markers (in evan's risk-work output specifically).

#### 2d. Peer health

For each peer, the harness scheduler runs heartbeats — check the most recent heartbeat-OK in your own memory's
`peer_heartbeat_log.md` (you maintain this from previous ticks). If a peer hasn't been seen healthy in 1h+, mark
unhealthy.

#### 2e. Last-fire times

Read your `team_state.md`: when did you last dispatch each peer for which skill?

### 3. Apply priority policy

Walk these in order. The first match wins; act and exit (after logging).

#### Priority 1 — Urgent

- **Critical CVE in evan's deferred-findings** → dispatch `evan risk-work` with explicit instruction to fix that
  candidate now (preempt other risk-work work).
- **Red CI on main** that no peer is currently fixing → log to memory + send a status note via call-peer to whoever the
  breaking commit's author is (likely a peer; if unclear, escalate to user).
- **Stuck peer** (no heartbeat 1h+) → escalate to user via decision-log entry tagged `escalation`. Do not dispatch more
  work to that peer until they're back.

#### Priority 2 — Cadence floor breached (peer dispatch)

For each peer, compute `time-since-last-fire`. If it exceeds the floor in CLAUDE.md → "Priority policy" → dispatch that
peer with a routine task in their domain. Floors:

- evan `bug-work` — 6h
- evan `risk-work` — 12h
- nova `code-cleanup` — 12h
- kira `docs-cleanup` — 24h
- kira `docs-research` — 7d

If multiple peers have breached, pick the one with the largest current backlog.

#### Priority 3 — Cadence floor breached (team-tidy, your own work)

If no priority 1 or priority 2 firing this tick, AND your `team-tidy` cadence floor (6h) has breached, invoke your own
`team-tidy` skill in-process. This is YOUR work — not a call-peer dispatch. The skill reads all identity files, finds
one consistency or small-improvement opportunity (per the strict bar in `team-tidy/SKILL.md`), applies it, commits,
delegates the push to iris, watches CI.

Compute floor:

```sh
LAST_TIDY=$(grep -oE "^## [0-9-]+ [0-9:]+ UTC — team-tidy" /workspaces/witwave-self/memory/agents/zora/decision_log.md | tail -1)
```

Parse the timestamp; if >6h ago (or never), invoke the skill.

Counts toward the team-tidy daily cap (3/day), not the peer-dispatch hourly cap.

#### Priority 4 — Backlog-weighted (peer dispatch)

Within cadence (no floor breached), pick the peer with the largest open backlog (count of `[flagged: ...]` items in
their deferred-findings memory). Dispatch them on the appropriate skill for their domain.

#### Priority 5 — Release-warranted check

This runs **independent** of priorities 1-4 — every tick.

```
IF COMMITS_SINCE_TAG > 0
AND no CI red on main HEAD
AND no in-flight release pipeline (check gh for running "Release" / "Release — ww CLI" / "Release — Helm charts")
AND no in-flight batch-revert (check git log for recent "Revert evan bug-work batch")
AND time since last release > 1h
AND no critical findings open in any peer's deferred-findings (the medium quality bar)
THEN dispatch iris with the release skill
```

Bump kind based on conventional-commit inference of `git log v<latest>..main`:

- Any `BREAKING CHANGE:` / `!:` → major
- Any `feat:` → minor
- Otherwise (only `fix:`, `chore:`, etc.) → patch

#### Priority 6 — Stand down

Nothing in any priority bucket fires → log "no action this tick" to decision log, exit cleanly.

### 4. Apply hard caps before dispatching

Before any dispatch in steps 3.1-3.4:

- **Max 5 dispatches/hour:** count entries in `decision_log.md` with timestamp within the last hour. If ≥5, abort the
  dispatch, log `[capped: dispatches/hour]`, exit.
- **Max 4 releases/day:** count `[release-dispatched]` entries in `decision_log.md` in the last 24h. If ≥4, abort the
  release dispatch (if that's what you were about to do), log `[capped: releases/day]`, fall back to the next priority.
- **Max 3 batch-reverts/day:** count `[revert-detected]` entries. If ≥3, this is systemic — escalate to user via
  `[escalation: revert-storm]` and enter pause-mode automatically.
- **Cycle detection:** for the candidate you're about to dispatch a fix for, check whether the same `[file:line]` has
  appeared in 3+ `[flagged: fix-forward-failed]` or `[reverted]` entries in the last 24h. If yes, mark `[frozen]` in
  evan's findings memory (via call-peer "freeze candidate X") and skip.

### 5. Dispatch (if any priority fired)

Use `call-peer` with a focused prompt that:

- Names the skill explicitly (e.g., "Run your `bug-work` skill")
- Includes the depth + sections (for evan/nova/kira)
- Includes a one-line rationale ("Cadence floor breached" / "Critical CVE re-surfaced" / etc.)
- Does NOT block on completion — fire and forget. The peer's response acknowledges receipt; the actual run state
  surfaces in their memory next tick.

Example dispatch prompt template (substitute `<peer>` and `<rationale>`):

> Hi <peer> — zora here. Dispatching <skill> per <rationale>. <skill-specific args>. Run your usual procedure; commit +
> iris-delegate as designed. I'll see your result in your memory next tick.

### 6. Log decision rationale

Append to `/workspaces/witwave-self/memory/agents/zora/decision_log.md`:

```markdown
## YYYY-MM-DD HH:MM UTC — tick

**State snapshot:**

- Latest tag: `v<X.Y.Z>`. Commits since tag: N.
- CI on main HEAD: <green/red/in-flight>.
- Peer health: iris=<ok|silent>, nova=<...>, kira=<...>, evan=<...>.
- Backlogs: iris=<n>, nova=<n>, kira=<n>, evan=<n>.

**Decision:** <dispatch <peer> <skill> | release-dispatched | stand-down | escalation | capped>

**Rationale:** <one-line reason from the priority policy>

**If dispatched:** prompt sent to <peer> at <time>; awaiting next-tick state read.
```

### 7. Update team state

Update `/workspaces/witwave-self/memory/agents/zora/team_state.md` with:

- New last-fire timestamp for the dispatched peer (if any)
- Updated peer health (heartbeat snapshot)
- Updated backlog counts

### 8. Exit cleanly

Return a one-paragraph summary to whoever invoked you (typically the heartbeat scheduler, but the user could invoke this
skill manually for a one-off pass). Format:

> Tick at HH:MM UTC. State: <one-line>. Decision: <one-line>. Next tick at HH:MM UTC.

## Out of scope for this skill

- **Authoring code or doc changes.** Dispatch the appropriate peer.
- **Direct git operations.** Iris owns push; you don't commit code.
- **Direct gh API calls.** Iris owns GitHub authority. You read CI state via `gh run list` (read-only on public repo)
  but do not invoke any mutating gh command.
- **Inventing new priorities or cadence floors on your own.** The policy is what's in CLAUDE.md → "Priority policy". If
  a new pattern emerges, surface it to the user via `[escalation: policy-question]` and let the user edit CLAUDE.md
  before you act.

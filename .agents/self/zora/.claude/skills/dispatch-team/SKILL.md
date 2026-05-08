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

#### 2b. CI state across every commit since latest tag

A binary built from a _failing_ commit is broken even if HEAD has moved past it — so checking only HEAD's CI runs the
way the original v1 policy did silently masked multi-hour red windows (the 2026-05-07 ww-CLI gofmt incident: 3
consecutive `CI — ww CLI` failures spanning ~1h45m were invisible to zora because each subsequent commit's "CI — docs"
run on the new HEAD was green, while the prior commits' ww-CLI failure aged out of the HEAD-only filter).

Today's check covers **every commit between `v<latest-tag>` and `origin/main`**:

```sh
LATEST_TAG=$(git -C <checkout> describe --tags --abbrev=0)
COMMITS=$(git -C <checkout> rev-list "${LATEST_TAG}..origin/main")  # newest-first
gh run list --branch main --limit 50 --json name,status,conclusion,headSha
```

For each `(workflow_name, headSha)` pair where `headSha` ∈ COMMITS:

- **Any concluded with `failure`** → mark CI as `[red on <commit_sha[0:8]>: <workflow>]` and feed this signal into
  Priority 1 (red-CI auto-dispatch). Order doesn't matter — one failure on any commit since the tag blocks the whole
  window from being considered "green."
- **Any still `in_progress`** → mark CI as `[settling]`. Don't fire release-warranted; do still proceed with
  cadence-floor dispatches because settling is normal post-push state.
- **All concluded `success`** across every (workflow, commit) pair → mark CI as `[green]`.

Your pod has `GITHUB_TOKEN` + `GITHUB_USER` injected from the `zora-claude` secret (added 2026-05-07 to close the
previous "infer CI from indirect signals" gap). You're read-only on git/gh per your tool posture; iris remains the
team's write authority for push, tag, and gh-API writes.

#### 2c. Peer memories

For each peer in `[iris, nova, kira, evan]`:

```sh
PEER_MEMORY=/workspaces/witwave-self/memory/agents/<peer>/MEMORY.md
PEER_FINDINGS=/workspaces/witwave-self/memory/agents/<peer>/project_*_findings.md
```

Read the index. As of 2026-05-07 all three findings-producing peers (evan / nova / kira) use the same status- marker
schema — `[pending]`, `[flagged: <reason>]`, `[fixed: <SHA>]` — going forward. Sections written before that date are
still in their original narrative format, so the **per-peer adapter** below combines a marker count on recent sections
with a narrative-count fallback on older ones:

| Peer | Findings file              | Adapter — count "open" entries                                                                                                                                                                                                                                                                                                                                           |
| ---- | -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| evan | `project_evan_findings.md` | Count `[pending]` + `[flagged: …]` markers (canonical schema since day one). Look for `[CRITICAL]` severity markers in risk-work output.                                                                                                                                                                                                                                 |
| nova | `project_code_findings.md` | For sections dated **2026-05-07 onward**: count `[pending]` + `[flagged: …]` markers (same canonical schema). For sections dated **before 2026-05-07** (legacy narrative format): read the most recent dated narrative section header (`## YYYY-MM-DD`); within it, sum the bullet-list counts nova recorded inline (e.g., `× 94`, `× 90`, "118 remaining diagnostics"). |
| kira | `project_doc_findings.md`  | Same shape as nova: marker schema on 2026-05-07-or-newer sections; narrative-bullet count on older ones.                                                                                                                                                                                                                                                                 |
| iris | n/a (service peer)         | No backlog count — iris is on-demand only.                                                                                                                                                                                                                                                                                                                               |

The legacy-narrative branch is a transient compatibility shim — once the legacy sections age out (typically as peers run
new sweeps that supersede the older entries' relevance), the adapter degenerates to a pure marker count and the schema
is fully uniform team-wide. Until then, the adapter gets the count _right enough_ — within ±5 — for backlog tiebreaking.

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
- **Red CI on any commit since latest tag** → **dispatch evan to fix it**, regardless of who authored the breaking
  commit. Author-agnostic: a binary built from a failing commit is broken whether the author was a peer or a human;
  treating "human authored = wait for human" is what froze the team for ~1h45m on the 2026-05-07 ww-CLI gofmt incident.
  Procedure:

  1. Fetch the failing job's logs:
     ```sh
     gh run view <run-id> --log-failed
     ```
  2. Extract the failing-step name + a tight context window (~30 lines around the first FAIL).
  3. `call-peer evan` with prompt:
     `Run bug-work on <failing-workflow> failure on commit <sha[0:8]>. Failing step: <step>. Context: <log excerpt>. Goal: produce a fix commit that turns this workflow green. Use your existing fix-bar; if the fix is out of scope (config / infra / not bug-class), flag and report back.`
     Mark this dispatch with `[priority-1: red-ci-recovery]` so it doesn't share the cadence-driven dispatch budget.
  4. If two consecutive evan attempts fail to clear the red CI → escalate harder per the time-bounded escalation rules
     below; do NOT keep retrying evan indefinitely.

- **Failed release workflow** (iris's release skill returned `[release-workflow-failed]`, OR a `Release*` workflow on
  the latest tag concluded `failure` / `cancelled` / `timed_out`) → **stop cadence-driven dispatching and redirect the
  team to recover.** A pushed tag is just the start of the release; the three workflows that fire post-tag publish the
  actual artifacts users pull. One failed = partial release = silent breakage downstream. Procedure:

  1. **Freeze regular dispatching** for this tick and subsequent ticks until recovery. Peer cadence floors keep counting
     (they'll fire on the recovery tick), but don't emit the dispatches now — every commit a peer produces during a
     partial-release window risks tangling the recovery.
  2. **Surface immediately**. Append `[escalation: release-workflow-failed]` to
     `/workspaces/witwave-self/memory/escalations.md` with the tag, failing workflow name, run URL, and recovery path.
     User sees this without trawling decision_log.
  3. **Diagnose the failure mode** from iris's reply (or from `gh run view <run-id> --log-failed` if the reply lacks
     detail):

     - **Transient infrastructure** (registry timeout, network blip, runner OOM, GitHub Actions outage) →
       `call-peer iris` with `gh run rerun --failed <run-id>`. If the re-run succeeds, surface
       `[release-workflow-recovered]` in `escalations.md` and resume normal cadence next tick.
     - **Real bug in the workflow's source target** (ww CLI build failed because a code regression got past
       `CI — ww CLI`; Helm chart push failed because chart YAML is malformed; container build failed because a
       Dockerfile regressed) → `call-peer evan` with the failing-job log + breaking commit, same shape as red-CI
       dispatch. After evan lands a fix, two paths:
       - If the workflow can re-target the same tag (re-run picks up the fixed source) → ask iris to re-run.
       - If the workflow's artifact for that tag is permanently published in a broken state → ask iris to cut `vX.Y.Z+1`
         with the fix once ANY commit lands. Tag is poisoned; ship a clean follow-up.

  4. **Two failed iris re-run attempts** OR evan can't fix → escalate hard. Append `[needs-human]` to `escalations.md`
     with the failure log + recovery options for human decision. Enter pause-mode.
  5. **Don't fire any new release-warranted dispatches** until this one is recovered. Otherwise the team layers broken
     release on broken release.

- **Stuck peer** (peer dispatch in flight >1h, OR peer's pod has dirty WIP blocking subsequent dispatches) → follow the
  **time-bounded escalation** ladder below. Don't just stand down forever — past versions of this policy held the team
  idle for 3+ hours waiting for human resolution while every cadence floor breached and zero fix attempts ran. Today's
  policy attempts auto-recovery before paging the user.

  - **T+0** — file `[escalation: stuck-peer]` in `decision_log.md` AND in
    `/workspaces/witwave-self/memory/escalations.md` (team-visible surface). Stop dispatching the stuck peer.
  - **T+30m** — dispatch iris with `git-investigate-and-restore` (or her general git-plumbing surface): "peer <name>'s
    pod tree has dirty WIP `<file-list>`; investigate the diff, decide commit-or-discard based on whether the change
    looks complete and safe, log decision in your memory, restore the tree to clean either way." Iris is the team's git
    plumber — she's the right surface for "investigate, decide, unblock."
  - **T+1h** — if still stuck after iris's recovery attempt: **harder escalation** to user. Append a one-line summary to
    `escalations.md` with `[needs-human]` prefix, including (peer, file-list, age). The user should see this on their
    next `ww escalations` (or equivalent visibility surface).
  - **T+2h** — automatic pause-mode entry. Touch `pause_mode.flag`; emit `[escalation: auto-paused]` log entry. Continue
    ticking but log-only until the user clears the flag.

  Cadence floors continue counting throughout — when the escalation resolves, breached cadences fire immediately on the
  recovery tick.

#### Priority 2 — Cadence floor breached (peer dispatch)

For each peer, compute `time-since-last-fire`. If it exceeds the floor in CLAUDE.md → "Priority policy" → dispatch that
peer with a routine task in their domain. Floors:

- evan `bug-work` — 3h (tightened from 6h on 2026-05-07; bug-class drainage drives release velocity)
- evan `risk-work` — 8h (tightened from 12h)
- nova `code-cleanup` — 8h (tightened from 12h)
- kira `docs-cleanup` — 6h (tightened from 24h on 2026-05-07; docs drift on every team commit)
- kira `docs-research` — 7d

If multiple peers have breached, pick the one with the largest current backlog.

**Choosing depth for evan dispatches (polish-tier control).** evan's `bug-work` and `risk-work` accept a `depth`
argument 1-10. The team works UP the polish ladder `5 → 7 → 9`; each tier exhausts the cheap finds for the next.
Cadence-mandated sweeps start at **depth=5** (per evan's own SKILL "After 1-3 has been run" default), not at the parser
default of 3 — depth 1-3 are reserved for ad-hoc cheap-pass triggered by the user or a peer. The CLAUDE.md priority
policy spells out the principle; this section is the mechanics.

Read the current tier from `team_state.md`:

```
polish_tier_evan_bug:                <int, default 5>
polish_tier_evan_risk:               <int, default 5>
polish_tier_evan_bug_zero_streak:    <int, default 0>  # consecutive 0-finding runs at current tier
polish_tier_evan_risk_zero_streak:   <int, default 0>
polish_tier_evan_bug_last_run_sha:   <sha, default latest tag at first run>
polish_tier_evan_risk_last_run_sha:  <sha, default latest tag at first run>
```

Decide the tier for THIS dispatch:

1. **Reset check.** Look at `git log <last_run_sha>..HEAD`. If any commits landed in evan's section scope (`harness/`,
   `backends/`, `tools/`, `shared/`, `operator/`, `clients/ww/`, `helpers/`, `scripts/`, `.github/workflows/`) — set
   tier back to **5** and zero the streak. Fresh source has new candidates worth a fresh function-level reasoning sweep.
2. **Advance check.** If no fresh source AND `zero_streak ≥ 2` at the current tier — advance the tier to the next rung
   on the ladder (`5 → 7 → 9`; cap at 9) and zero the streak. The advance encodes "we've exhausted this tier; go
   deeper."
3. **Hold check.** Otherwise keep the tier as-is.

Pass it to evan in the call-peer prompt: `Run your bug-work skill at depth=<tier>, sections=all-day-one`. Same shape for
risk-work (`sections=all-deps` is the default scope for risk-work).

After the dispatch, when evan reports back, update `team_state.md`:

- If evan's run returned 0/0/0 (0 candidates / 0 fixed / 0 flagged) — increment `zero_streak`. Update `last_run_sha` to
  current HEAD either way.
- If evan returned anything substantive (≥1 candidate, fixed or flagged) — zero the streak. Update `last_run_sha` to
  current HEAD.

Log the tier choice + reason in `decision_log.md` on each dispatch:

```
- evan bug-work dispatched at depth=7 (advanced from 5 — last 2 runs 0/0/0 at depth=5, no fresh source since).
- evan risk-work dispatched at depth=5 (reset from 7 — fresh commits in operator/ since last run).
```

This is how the team becomes _actually_ bug-free / risk-free rather than "0 found at the cheap depth." Treat each tier
as its own ground to cover; only depth=9 across all-day-one with adversarial passes counts as "we've looked hard."

**Choosing the skill for nova / kira dispatches (polish-tier control).** Same advance/reset mechanism as evan, but
instead of a depth integer the "tier" is a skill name. Cheap-pass = the default cleanup skill; deep-pass = the heavier
authoring/research skill.

Read from `team_state.md`:

```
polish_skill_nova:                <"code-cleanup" | "code-document", default "code-cleanup">
polish_skill_kira:                <"docs-cleanup" | "docs-research", default "docs-cleanup">
polish_skill_nova_zero_streak:    <int, default 0>
polish_skill_kira_zero_streak:    <int, default 0>
polish_skill_nova_last_run_sha:   <sha, default latest tag at first run>
polish_skill_kira_last_run_sha:   <sha, default latest tag at first run>
```

Section scope for the fresh-source check:

- nova: same as evan (source code paths — `harness/`, `backends/`, `tools/`, `shared/`, `operator/`, `clients/ww/`,
  `helpers/`, `scripts/`, `.github/workflows/`, plus `clients/dashboard/src/`).
- kira: docs surface — any `*.md` (root, per-subproject, `docs/**`, `.agents/**`), `AGENTS.md`, `CLAUDE.md`,
  `CHANGELOG.md`, `README.md`. (When kira herself commits docs that land here, the next-dispatch reset is fine — fresh
  docs may have new drift to find.)

Decide the skill for THIS dispatch:

1. **Reset check.** If `git log <last_run_sha>..HEAD -- <scope>` returns any commits, set the skill back to the
   cheap-pass default and zero the streak.
2. **Advance check.** If no fresh source AND `zero_streak ≥ 2` at the cheap-pass — flip to the deep-pass skill for THIS
   dispatch (then auto-flip back to cheap-pass next time, since the deep-pass is one-shot, not steady-state).
3. **Hold check.** Otherwise keep the skill as-is.

Pass it to the peer in the call-peer prompt: `Run your <skill_name> skill` (no depth arg — nova/kira don't accept one).

After the dispatch, when the peer reports back, update `team_state.md`:

- 0 commits / 0 findings → increment `zero_streak`. Update `last_run_sha`.
- ≥1 commit OR ≥1 finding → zero the streak. Update `last_run_sha`.
- If THIS dispatch was a deep-pass (advance fired), zero the streak regardless and flip back to cheap-pass for next
  time. (The deep-pass only fires on advance; it never holds as steady state.)

Log in `decision_log.md`:

```
- nova code-cleanup dispatched (held — last 2 runs found things, no streak).
- nova code-document dispatched (advanced — code-cleanup returned 0/0/0 twice on stable source; one-shot deeper pass).
- kira docs-research dispatched (advanced — docs-cleanup returned 0/0/0 twice; one-shot research refresh).
```

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

#### Priority 5 — Release-warranted check (velocity-driven)

This runs **independent** of priorities 1-4 — every tick.

**Step 1: compute weighted commits since latest tag.** For each commit in `git log v<latest>..main`:

| Commit prefix                               | Weight |
| ------------------------------------------- | ------ |
| `feat:` / `feat(<scope>):`                  | 2.0    |
| `fix:` / `fix(<scope>):`                    | 1.0    |
| `docs:` / `docs(<scope>):`                  | 0.5    |
| `chore:` / `style:` / `refactor:` / `test:` | 0.25   |
| Anything else (no conventional prefix)      | 0.5    |

**Exclude these from the weighted sum** (release-artifact commits — counting them re-triggers a release for releasing):

- `docs(changelog):` commits.
- Any commit whose message body indicates it was authored by iris during a release cut.

**Step 2: detect critical-fix fast-path.** Scan `git log v<latest>..main` for any commit matching `fix(security):` OR a
body containing the literal word `critical`. If found, set `critical_fix_present = true`.

**Step 3: gate.**

```
IF (weighted_commits ≥ 3.0 OR critical_fix_present)
AND no CI red on main HEAD
AND no in-flight release pipeline (check gh for running "Release" / "Release — ww CLI" / "Release — Helm charts")
AND no in-flight batch-revert (check git log for recent "Revert evan bug-work batch")
AND ≥ 15 minutes since last release  ← hygiene floor only; not a cadence knob
AND no critical findings open in any peer's deferred-findings (the medium quality bar)
THEN dispatch iris with the release skill
```

Bump kind based on conventional-commit inference of `git log v<latest>..main`:

- Any `BREAKING CHANGE:` / `!:` → major
- Any `feat:` → minor
- Otherwise (only `fix:`, `chore:`, etc.) → patch

**Why velocity-driven.** The previous policy (≥1h floor + max 4 releases/day) double-locked itself the night of
2026-05-06 → 05-07: 4 productive releases burst-shipped in ~6h, then the team stood down for the next 14h with the
release surface frozen. Velocity-driven cadence lets bursty mornings ship 6 releases when there's real content and quiet
stretches batch over hours, without arbitrary daily cliffs.

#### Priority 6 — Stand down

Nothing in any priority bucket fires → log "no action this tick" to decision log, exit cleanly.

### 4. Apply hard caps before dispatching

Before any dispatch in steps 3.1-3.4:

- **Max 8 dispatches/hour:** count entries in `decision_log.md` with timestamp within the last hour. If ≥8, abort the
  dispatch, log `[capped: dispatches/hour]`, exit. (Raised from 5 on 2026-05-07; 5/hr was binding under the tightened
  cadence floors when iris-cleanup chains stacked alongside peer dispatches.)
- **Max 20 releases/day (runaway guard, not cadence policy):** count `[release-dispatched]` entries in `decision_log.md`
  in the last 24h. If ≥20, this is a runaway loop — log `[capped: releases/day]`, enter pause-mode, and escalate to the
  user via `[escalation: release-storm]`. Velocity-driven release-warranted is the everyday knob; this exists only to
  halt a malfunction.
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

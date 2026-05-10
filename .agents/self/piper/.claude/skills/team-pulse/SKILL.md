---
name: team-pulse
description:
  Single outreach-loop pass. Reads team state (git log, peer memories, Zora's logs, CI runs, recent releases), scores
  recent events on a 0-10 substantive-score model, applies the time-since-last-post multiplier, and either posts to
  GitHub Discussions (Announcements ≥9 / Progress 5-8) or stays silent (<5). Logs every tick to `pulse_log.md`. Trigger
  when the heartbeat scheduler invokes this (every 15min) or when the user says "run a pulse" / "do your thing" / "fire
  team-pulse".
version: 0.1.0
---

# team-pulse

One outreach-loop pass. Run by the heartbeat scheduler every 15 minutes (loosened from 5min on 2026-05-10 once voice +
filter + Guard 0 moderation stabilised; matches Zora's decision-loop cadence).

## Inputs

None from the prompt. Read state from:

- `git log origin/main` — what landed since your `last_run_sha` in `pulse_log.md`
- Peer `MEMORY.md` indexes + deferred-findings memory files (`project_evan_findings.md`, `project_finn_findings.md`,
  `project_code_findings.md`, `project_doc_findings.md`)
- Zora's memory: `decision_log.md`, `escalations.md`, `team_state.md`
- `gh run list --branch main --limit 20` — recent CI conclusions
- `git tag -l 'v*' --sort=-creatordate | head -3` — recent releases
- Your own memory: `pulse_log.md` (last-tick state, last-post timestamp), `drafts/`

## Instructions

### 0. Verify the source tree + pin git identity

```sh
git -C <checkout> rev-parse --show-toplevel
```

If missing, log "source tree absent" and stand down. Pin git identity via `git-identity` skill (idempotent; defends
against the rare case where you'd write to your own memory and want the audit trail).

### 1. Pause-mode check

If `pause_mode.flag` exists in your memory namespace, you're in observation-only mode. Read state, log what you WOULD
have decided to `pulse_log.md` with a `[paused: would-have]` prefix, then exit. Do NOT post.

```sh
test -f /workspaces/witwave-self/memory/agents/piper/pause_mode.flag && echo "PAUSED"
```

### 1.5. Engage on Discussions (one or more discuss-\* skills, in order)

**Before doing the regular pulse walk, run the engagement skills in order. Each is independent; each scans a different
surface; each applies its own guards.**

1. **Step 1.5a — `discuss-comments`** — replies on Piper's own Announcements/Progress posts. Four guards (Guard 0
   moderation pre-screen, author filter, engagement-signal gate, per-thread cooldown). Full details in
   `discuss-comments/SKILL.md`.

2. **Step 1.5b — `discuss-bugs`** — investigation + reply on threads in the GitHub Discussions `Bugs` category.
   Multi-tick investigation pattern; deep code-reading to verify whether reports are real bugs; routes confirmed bugs
   through `bugs-from-users.md` for Zora to dispatch the right peer. Full details in `discuss-bugs/SKILL.md`.

3. **Step 1.5c — `discuss-questions`** — engagement on the `General` category. Open-ended Q&A from humans about the
   team, the platform, design rationale, the autonomous-experiment narrative. Investigation discipline matches
   `discuss-bugs` (read the code, verify before answering); response shape is factual answer rather than bug-class
   verdict. Full details in `discuss-questions/SKILL.md`.

4. **(Future Step 1.5d — `discuss-ideas`)** — when scaffolded; engagement on the `Ideas` category.

All four guards (Guard 0 first, then 1-3) hold across every discuss-\* skill. Guard 0 (moderation pre-screen via
`minimizeComment` ± `lockLockable`) is terminal — matched bad-shape content gets moderated and never enters the reply
path. See CLAUDE.md → "Moderation posture" for the canonical pattern table.

Reply latency matters — you don't want a human comment sitting unanswered for hours just because no release shipped. By
doing engagement first, every 15-min heartbeat is also a chance to engage. A tick that posts no new content (silent
stand-down on scoring) can still produce useful conversation if a human commented since the last tick.

If any skill returns "no eligible threads" or "no value-add replies", that's normal — continue to the next skill, then
to Step 2. If a skill returned errors (e.g., PAT scope problem), the error is logged in pulse_log and surfaced to
needs-human-review; carry on with the remaining engagement skills + the rest of the tick.

### 2. Read team state

Build the snapshot.

#### 2a. Git activity since your last tick

```sh
LAST_RUN_SHA=$(grep '^last_run_sha:' /workspaces/witwave-self/memory/agents/piper/pulse_log.md | tail -1 | awk '{print $2}')
LAST_POST_TS=$(grep '^last_post_ts:' /workspaces/witwave-self/memory/agents/piper/pulse_log.md | tail -1 | awk '{print $2}')
git -C <checkout> log "${LAST_RUN_SHA:-HEAD~50}..origin/main" --format='%h %an %s'
```

If `pulse_log.md` doesn't exist (first ever tick), default `LAST_RUN_SHA` to the latest tag (use the tag's SHA) and
`LAST_POST_TS` to "never".

#### 2b. Recent CI

```sh
gh run list --branch main --limit 20 --json status,conclusion,name,databaseId,headSha,createdAt
```

Surface anything `failure` / `cancelled` / `timed_out` since `LAST_RUN_SHA`. Also note any `in_progress` `Release*`
workflows (release pipeline still running).

#### 2c. Recent releases

```sh
git -C <checkout> tag -l 'v*' --sort=-creatordate | head -3
```

Compare against `pulse_log.md` to find tags created since your last post (these score high — see Step 3).

#### 2d. Peer memories — what's each peer been doing?

For each peer in `[Iris, Kira, Nova, Evan, Finn, Zora]`:

```sh
# Last-modified timestamp on each peer's findings file
PEER_FINDINGS_MTIME=$(stat -c %Y /workspaces/witwave-self/memory/agents/<peer>/project_*_findings.md 2>/dev/null)
```

Plus read each peer's `MEMORY.md` index for narrative-style updates. You don't need to deeply parse — the events you'll
score come from `git log` (commits with peer authorship). Memory reads are for tone + context (e.g., "Evan flagged
something CRITICAL in his last sweep — surface it as bad news").

#### 2e. Zora's escalations + decision log

```sh
cat /workspaces/witwave-self/memory/escalations.md
tail -200 /workspaces/witwave-self/memory/agents/zora/decision_log.md
```

Look for: `[escalation: ...]` openings/closings, `[needs-human]` markers, `release-workflow-failed`,
`stuck-commits-iris-blocked`, etc. These are high-value events that often score 8-10.

### 3. Score events on the substantive-score 0-10 model

For every event detected since your last post, assign a score:

| Score   | Examples                                                                                                                                                                                                                       |
| ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **10**  | New release (tag pushed AND 3/3 release pipeline succeeded); critical CVE/security fix shipped; v1.0.0 milestone                                                                                                               |
| **9**   | `[needs-human]` escalation surfaced; user-visible surface change shipped (new `ww` subcommand, new flag, new helm chart feature); release pipeline `[release-workflow-failed]` or `[release-workflow-stuck]`                   |
| **8**   | Red CI on `main` (current state, not historical); peer wedged with stuck-commits open; `[escalation: peer-offline-extended]`                                                                                                   |
| **7**   | High-severity bug fixed (`[REL:HIGH]` reliability or `[CRITICAL]` security `[fixed: SHA]`); multi-commit substantive landing (≥3 commits from one peer in <30min, e.g., 3 coordinated test commits backfilling a coverage gap) |
| **6**   | Polish-tier advance (e.g., evan 5→7, finn 3→5); first productive run of a new agent (finn's first non-zero gap-work after sandbox unblock); toolchain unblock that opens new analyzer surface (e.g., staticcheck unblocked)    |
| **5**   | Single substantive `fix:` commit (single bug or single risk fixed at Medium severity); CI recovered from red→green                                                                                                             |
| **3-4** | Routine `code:` ruff format commit; cadence-floor breach with 0-1 candidates surfaced; benign team-tidy commit                                                                                                                 |
| **0-2** | HEARTBEAT_OK pings; auto-format docs commit; nova/kira hygiene with 0 substantive findings; stand-down ticks                                                                                                                   |

**Composite scoring when multiple events happen in the same window:** take the MAX score across events; you'll bundle
the supporting events into a single post at that route. Don't sum.

**Bad-news scoring is symmetric with good-news:** red CI (8) is the same range as a multi-commit landing (7), so both
eligible for Progress; a release-workflow-failed (9) goes to Announcements same as a release shipping (10). Bad news
gets posted plainly, no spin.

### 4. Apply the time-since-last-post multiplier

The threshold scales with how recently you posted:

| Time since last post | Threshold adjustment                                                      |
| -------------------- | ------------------------------------------------------------------------- |
| **<15min**           | +3 (very high bar — only score=10 events pass)                            |
| **15min–1h**         | +1 (modestly higher bar)                                                  |
| **1h–4h**            | 0 (baseline)                                                              |
| **>4h**              | -1 (relaxed bar; team's been quiet, even moderate events worth narrating) |

**Effective threshold for posting** = `9 (announcements) - adjustment` for Announcements route, or
`5 (progress) - adjustment` for Progress route. So at <15min since last post: a Progress-eligible event needs score ≥ 8
to actually post; Announcements needs ≥ 12 (impossible — event must wait, or be score=10 to override because score caps
at 10).

Actually clearer formulation: **compute effective score = raw_score - adjustment**. Then route by effective score with
the SAME 9-and-up / 5-7-Progress / <5-silent table. Adjustment subtracts from raw score.

### 5. Decide route

After applying the multiplier:

- **Effective score ≥ 9** → post to **Announcements**.
- **Effective score 5-8** → check 30-min cooldown on Progress posts. If last Progress post was <30min ago, draft to
  `drafts/` for bundling next eligible window. Otherwise post to **Progress**.
- **Effective score < 5** → silent stand-down.

When you draft to `drafts/`, append the new event's context to the existing draft (or create one if absent) so the
next-eligible-window post bundles them naturally.

### 6. (Optional) Ask peers for clarification — only after exhausting local reads

**Default to non-intrusive.** Peers are doing real work; every clarification round-trip costs them LLM time and adds
noise. Before reaching for `ask-peer-clarification`, walk this read-first checklist and confirm at least one source is
genuinely silent on the question:

1. The peer's own `MEMORY.md` index + relevant deferred-findings file (often the answer is in their own audit trail).
2. The commit message body of the relevant commit (`git show <sha>` — a peer's atomic per-finding commit typically
   explains the why).
3. Zora's `decision_log.md` for the relevant tick (her decisions cite the rationale; if you don't see it, read more
   entries before asking).
4. The relevant source code when the finding cites a file or symbol.
5. `escalations.md` for the surrounding context if the question relates to an escalation.

If after the checklist the answer is still genuinely missing AND the post's framing meaningfully depends on it AND the
peer you'd ping is the authoritative source — then invoke `ask-peer-clarification`. The peer's reply becomes part of the
post's context.

If you can't get clarification in this tick (peer slow / unreachable / not authoritative for the question), **defer the
post** to the next eligible window. A delayed accurate post is always better than a speculative on-time post or a
peer-pinged interrupt.

For most ticks the checklist will resolve the question or the question won't matter — the answer will be "draft without
the clarification, the framing doesn't actually hinge on it." Don't ping unless it does.

Especially Zora — she's the team-state oracle, but her `decision_log.md` is also the most-read source for "what happened
and why." Read carefully before asking. When you do ask Zora, the question is usually about a specific
decision-or-state-transition you saw in her log but can't fully parse — not "what's the team doing?" (which the log
already answers).

### 7. Draft + post (when applicable)

Voice rules in CLAUDE.md → "Voice" section apply. Concretely:

- Title: ≤ 60 chars, descriptive — e.g., "v0.23.4 shipped" / "Three test coverage commits from Finn" / "CI red on `main`
  — Evan investigating".
- Body: 2-5 short paragraphs. Lead with the headline event; cite short SHAs (8 chars) and PR/issue numbers; end with
  what's next on the radar (one sentence).
- No marketing phrasing. No internal markers. No hype.
- Mention peers by capitalized first name (Iris, Kira, Nova, Evan, Finn, Zora) in prose; identifiers stay lowercase.

Post via `post-discussion` skill (passes the category ID + title + body to `gh api graphql`).

### 8. Log the tick

Append to `/workspaces/witwave-self/memory/agents/piper/pulse_log.md` regardless of whether you posted:

```markdown
## YYYY-MM-DDTHH:MMZ — tick

- last_run_sha: <head SHA after this tick>
- events_observed: <comma-separated short list, e.g., "release v0.23.4 cut", "evan-fix 2a6d27d0", "ci-green">
- raw_score: <int>
- adjustment: <int> (time-since-last-post)
- effective_score: <int>
- route: <Announcements | Progress | silent | drafted>
- post_url: <https://github.com/.../discussions/NNN | n/a>
- last_post_ts: <RFC3339 timestamp from this tick if posted, else carry forward from previous>
- summary: <one-line — for human-grep auditing>
```

Trim entries older than 30 days during the same write so the file stays bounded.

Update `drafts/` directory: prune drafts older than 24h; if you posted, mark any drafts that bundled into this post as
`[bundled]` and remove.

### 9. Return a one-paragraph summary

Format:

> Tick at HH:MMZ. Observed: <events>. Score: <raw>/<effective>. Route: <route>. Posted: <url> OR Silent (<reason>) OR
> Drafted (<reason>; will reconsider in <next eligible window>).

That's the A2A reply when invoked manually; for heartbeat ticks the summary is logged but not surfaced unless the user
reads `pulse_log.md`.

## Failure modes worth surfacing explicitly

- **GitHub PAT missing / expired** — `gh api graphql` returns 401. Log the failure to `pulse_log.md` with
  `[error: gh-auth-failed]` AND surface to the user via memory (`needs-human-review.md` if you have one; otherwise the
  user reads the pulse_log). Don't silent-fail; the public surface goes dark while we believe it's working.
- **Discussions API rate-limited** (5000/hr authenticated, but the team is small and `team-pulse` posts rarely) — back
  off, log, retry next tick. Don't burn the budget on retries.
- **Category ID drift** — the `Announcements` and `Progress` category IDs in `witwave-ai/witwave/discussions` are pinned
  in your reference memory (`reference_gh_discussions.md`). If the IDs change (admin renamed/deleted/recreated a
  category), `post-discussion` will 404 — fall back to `gh api graphql` query for current category list and update the
  reference memory.
- **`call-peer` to Zora times out** when asking for clarification — defer the post to the next eligible window; don't
  post without the answer.

## Out of scope for this skill

- **Replying to humans** — v1 is post-only. The `read-discussion-thread` skill (handle `@piper-agent-witwave` mentions)
  is deferred to v2.
- **Posting to other surfaces** (Twitter, Slack, etc.) — deferred to v2; v1 is GitHub Discussions only.
- **Filing GitHub Issues** — Discussions ≠ Issues. If you see something issue-worthy in your scan, route via the
  relevant peer (or Zora) instead. You don't file issues.
- **Dispatching peers for work** — only `call-peer` for clarification. Never to ask a peer to DO something.
- **Editing your own posts after publish** — leave them as a record. If the post turns out to be wrong, post a follow-up
  correction; don't rewrite history.

## When to invoke

- **Heartbeat-driven** — every 15 min via `.witwave/HEARTBEAT.md`. Each tick = one team-pulse pass.
- **On demand** — user sends "run a pulse" / "do your thing" / "fire team-pulse". Same flow; the A2A reply carries the
  one-paragraph summary.
- **After a major event the user knows about** — user might ping "Piper, post about the Go toolchain unblock that just
  landed" — handle as on-demand, but always go through the scoring + filter (the user's intuition for "substantive"
  should align with yours; if it doesn't, the answer is calibration, not bypass).

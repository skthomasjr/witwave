---
name: team-tidy
description:
  Maintain consistency + small improvements across the team's identity files (`.agents/self/**`). Reads every
  agent's CLAUDE.md, agent-card.md, SKILL.md files; detects consistency drift, schema mismatches, and small-bore
  improvements; picks ONE per pass; applies as a single atomic commit; delegates push to iris; watches CI; reverts
  on fail. Includes zora's own files (full self-improvement autonomy). Strict bar: thoughtful, minimal,
  consistency-or-improvement-only — no wild changes. Trigger when zora's heartbeat decides team-tidy cadence has
  breached, or when the user says "tidy the team", "consistency pass", "improve the agents", "team-tidy".
version: 0.1.0
---

# team-tidy

Single-pass team-identity consistency + improvement work. Mirrors evan's `bug-work` shape but operates on the
team's identity files (`.agents/self/**`) instead of source code.

The bar is **strict**:

- **Consistency or genuine improvement only.** Three valid categories:
  - **(C1) Drift correction** — sections that should match across agents but don't. Example: the "Team
    coordinator" section we wrote into iris/kira/nova/evan should stay byte-identical except for peer-specific
    cross-agent-collaboration phrasing; if it drifts, normalise.
  - **(C2) Pattern propagation** — a useful pattern established in one agent that applies to others. Example:
    evan's `[pending]/[fixed: <SHA>]/[flagged: <reason>]` marker schema in his deferred-findings memory; nova
    and kira would benefit from adopting the same.
  - **(C3) Small clear improvements** — typo, broken cross-ref, stale version pin, redundant prose, dead reference
    to a renamed file/skill. The kind of change a careful reviewer would unambiguously approve.
- **NOT in scope:** reorganisations, "while I'm here" rewrites, aesthetic preferences, pattern *invention* (vs
  propagation), substantive design changes, anything that meaningfully alters an agent's behaviour, anything
  outside `.agents/self/**`.
- **Atomic, minimal.** One logical change per commit. Cap ~50 lines changed per commit. If the change touches
  multiple files (e.g., propagating a pattern to four peers), one commit is fine — but it's still ONE logical
  change.

You can edit your own files (`.agents/self/zora/**`) too. **Full self-improvement autonomy** under the same strict
bar — no "surgeon doesn't operate on themselves" exception. Self-edits are subject to the same backout discipline:
if your edit to your own decision logic breaks something, the same revert path applies.

## Inputs

None from the prompt. Run when invoked (by `dispatch-team` per cadence policy, or directly by the user). State
read from disk + memory.

## Hard caps (check before any commit)

- **Max 3 team-tidy commits per day.** Count `[team-tidy]` entries in `decision_log.md` within the last 24h.
  If ≥3, skip team-tidy this tick, log "[capped: team-tidy/day]", exit cleanly.
- **Max ~50 lines changed per commit.** If the candidate change requires more, refuse it as out-of-scope (mark
  as "needs-human-review" in your own deferred-decisions memory and skip).

## Instructions

Read these from CLAUDE.md before starting:

- **`<checkout>`** — local working-tree path (Primary repository → Local checkout).
- **`<branch>`** — default branch (`main`).

### 0. Pause-mode + cap check

```sh
test -f /workspaces/witwave-self/memory/agents/zora/pause_mode.flag && echo "PAUSED" && exit
TIDY_TODAY=$(grep -c "\[team-tidy\]" /workspaces/witwave-self/memory/agents/zora/decision_log.md 2>/dev/null || echo 0)
[ "$TIDY_TODAY" -ge 3 ] && echo "DAILY CAP REACHED" && exit
```

### 1. Verify the source tree

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

If working tree dirty: log to memory, stand down. Don't try to clean it — that's a per-peer concern.

Pin git identity via `git-identity` skill.

```sh
PRE_TIDY_SHA=$(git -C <checkout> rev-parse HEAD)
```

### 2. Read all identity files

Build a snapshot of every agent's identity files:

```sh
find /workspaces/witwave-self/source/witwave/.agents/self/ -type f \
  \( -name "CLAUDE.md" -o -name "agent-card.md" -o -name "SKILL.md" -o -name "HEARTBEAT.md" -o -name "backend.yaml" \) \
  | sort
```

Read each file. Note size + last-modified. Build a per-agent + per-skill mental map.

### 3. Detect candidates

Walk the snapshot looking for the three valid categories:

#### C1 — Drift correction

For sections that SHOULD be identical across agents (or near-identical with peer-name swaps):

- "Team coordinator" section in iris/kira/nova/evan CLAUDE.md (added 2026-05-07 in commit ce79335). Should stay
  byte-identical except for the cross-agent-collaboration sentence which is peer-specific. Diff to detect drift.
- "Memory" section structure (Types, How to save, What NOT to save, When to access, Cross-agent reads). Same
  across all 5 agents.
- The cross-agent skills (`call-peer`, `discover-peers`, `git-identity`) under each agent's
  `.claude/skills/`. These are byte-identical copies; if any has drifted, normalise to the canonical
  (kira's, since she was first).

For each detected drift: candidate is "normalise <file>:<section> to match <reference>".

#### C2 — Pattern propagation

Look for patterns established in one agent that obviously apply to others:

- **Findings-marker schema.** evan uses `[pending]/[fixed: <SHA>]/[flagged: <reason>]/[ci-fix-forward: <SHA>]` in
  his `project_evan_findings.md`. nova's `project_code_findings.md` and kira's `project_doc_findings.md` use
  prose without markers. Propagating evan's schema would let zora's backlog-counter work uniformly. Candidate:
  "add status markers to nova/kira's findings file format (one peer per commit)."
- **Step 0.5 recovery + Step 1.5 persist patterns.** evan's bug-work has these durability guards. nova's
  code-cleanup and kira's docs-cleanup don't. If their long sweeps are at risk of mid-loop death, the same
  durability guard would help. Candidate: propose adding (but flag as "needs-human-review" — it's a substantive
  design change, may exceed the team-tidy bar).
- **Safe-pattern catalogue concept.** evan has one in bug-work. nova's hygiene work has analogous canonical-fix
  patterns (e.g., `prettier --write` outputs are deterministic; rerun → fix → done). Could codify. Candidate:
  flag as "needs-human-review" — design-significant.

For each detected propagation opportunity: assess if it's clearly C2 (drop in pattern that's already used
elsewhere) or border-line C2 (substantive design change). Border-line goes to needs-human-review, NOT applied.

#### C3 — Small clear improvements

- Typos, awkward phrasing, broken markdown links
- Stale version pins in skill descriptions (e.g., a skill mentions "ruff 0.6.9" but the image now has 0.7.x —
  update the reference)
- Dead cross-references (a skill mentions `bug-sweep` from before it was renamed to `bug-work`)
- Redundant prose where a sentence already said the same thing in cleaner form earlier
- Comment-vs-code drift in skill instructions (skill says "use --foo flag" but the actual command moved to --bar)

### 4. Filter + prioritise

Apply the strict bar to every candidate:

- Atomic? Single logical change?
- Within the ~50-line cap?
- Within `.agents/self/**`?
- Demonstrably consistency or improvement, not preference / reorganisation / behaviour change?

Drop anything that fails. Out of the survivors, pick **one** by leverage (drift-correction across multiple files
> pattern-propagation across two peers > single-file improvement > typo).

If zero survivors: log "no team-tidy candidates this pass" to decision log, exit cleanly.

### 5. Apply the change

Use Edit / Write tools, scoped to `.agents/self/**`. **Never write outside that prefix.** If you find yourself
wanting to edit something outside `.agents/self/**`, you've drifted out of scope — abort and log.

Apply the minimal edit. Verify post-edit:

- The change matches the candidate's category (C1 / C2 / C3).
- No unrelated changes accreted.
- Total lines changed ≤50 (count from `git diff --stat`).

If anything fails: revert the working-tree change (`git checkout -- <file>`), log the candidate to
`needs-human-review.md`, exit cleanly.

### 6. Commit (one logical change per commit)

```sh
git -C <checkout> add <files>
git -C <checkout> commit -m "agents: <one-line description of consistency/improvement> [team-tidy]

<2-4 lines: which category (C1/C2/C3), which files, what changed, why it's
in scope. Reference the canonical source if normalising. Tag the commit with
[team-tidy] in the subject so future zora ticks can count today's quota.>
"
```

The `[team-tidy]` marker in the subject is what step 0's hard-cap counter looks for. Don't omit it.

### 7. Delegate push + watch CI to iris

Same procedure as evan's bug-work Step 7. Send iris a `call-peer` prompt asking her to push + watch CI + fetch
failing-job log on red. Wait for her report.

- **Push success + CI green:** done. Log `[team-tidy: <commit-SHA>] applied — <one-line>` to decision log.
- **Push success + CI red:** **fix-forward, ONCE.** If you can fix the breakage in scope (likely — most
  identity-file edits don't break CI; if one does, it's usually a markdown lint or a broken link), do it. If
  not in scope, batch-revert (the team-tidy commit) and log.
- **Push failure:** STOP. Log to decision_log; the next tick re-attempts naturally.

### 8. Update memory

Append to `decision_log.md`:

```markdown
## YYYY-MM-DD HH:MM UTC — team-tidy

**Category:** <C1 drift-correction | C2 pattern-propagation | C3 small-improvement>
**Files touched:** <comma-separated list>
**Lines changed:** N
**Commit:** `<SHA>` `<subject>`
**Iris push:** <success/fail>
**CI:** <green/red, with fix-forward outcome if relevant>
**Rationale:** <one-line: what was inconsistent or improvable, why this change is in scope>
```

If a candidate was deferred to `needs-human-review.md`, also append to that file:

```markdown
## YYYY-MM-DD — <candidate description>

- **What:** <the proposed change>
- **Why deferred:** <which gate failed: out-of-scope / >50 lines / behaviour change / unclear leverage>
- **Suggested approach:** <one-line for the human reviewer>
```

### 9. Exit cleanly

Return a one-paragraph summary to the caller:

> team-tidy at HH:MM UTC. Category: <C1/C2/C3>. Change: <one-line>. Commit: <SHA>. CI: <conclusion>. Quota
> today: N/3.

If no candidates / quota exceeded / nothing to do, return:

> team-tidy at HH:MM UTC. No action this pass: <reason>. Quota today: N/3.

## Out of scope for this skill

- **Substantive design changes.** Adding new sections to CLAUDE.md, redesigning a skill's procedure, changing
  agent identity / scope. These need human review; defer to `needs-human-review.md`.
- **Source code edits.** Anything outside `.agents/self/**`. The team-identity surface is the entire scope.
- **Pattern invention.** If a pattern doesn't already exist in at least one peer, this skill doesn't establish
  it. Propagation is the verb; inception is human work.
- **Aesthetic preferences.** Reflowing prose, changing list style, "this reads better with two paragraphs."
  These need human review.
- **Mass refactors.** Single logical change per commit. If you find yourself wanting to apply the same fix to
  10 files at once, that's still one logical change ("propagate X to all peers") — but watch the line count.
  If >50 lines, defer and surface for human review.

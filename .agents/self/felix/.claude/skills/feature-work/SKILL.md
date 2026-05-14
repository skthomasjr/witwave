---
name: feature-work
description:
  Author a new feature end-to-end in the primary repo. Reads the request (from user A2A, zora dispatch, or piper-routed
  Discussion), tiers the work against the 1-10 risk-tier ladder, plans the implementation, executes if within the
  autonomous ceiling, commits atomically with the non-waivable fix-bar, and delegates push + CI watch to iris.
  Single-pass shape — one feature request per invocation. Trigger when the user says "build X", "implement Y", "add a Z"
  (run mode); or "plan a feature for X" (plan-only mode); or specifies tier / scope ("build Q at tier 2", "plan a
  feature in the harness").
version: 0.1.0
---

# feature-work

One feature, one pass. Read request → tier → plan → (if approved) implement → test → commit → delegate push → watch CI →
fix-forward → log.

## Inputs

- **`request`** — the feature description. Free-text prompt or a structured reference (Discussion number, roadmap item,
  A2A text). Required.
- **`mode`** _(optional)_ — `run` (plan + implement; default) or `plan` (plan only, no implementation; produces draft in
  `drafts/` for human review).
- **`tier_hint`** _(optional)_ — user's hint at the expected tier. You compute your own; this is for cross-check.
- **`source`** _(optional)_ — origin of the request: `user-a2a` / `zora-dispatch` / `piper-routed-discussion` /
  `roadmap-derived`. For audit logging.

## Pre-flight (skip directly to Step 1 if any fails)

### 0a. Verify the source tree

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --short
```

- Missing checkout → log "source tree absent" and stand down. Don't try to clone.
- Dirty tree → stand down. Iris owns the tree state; if there's dirty WIP, surface to `escalations.md` and exit. Don't
  commit on top of dirty state.

### 0b. Verify CI is green on main HEAD

```sh
gh run list --branch main --limit 5 --json status,conclusion,name,headSha
```

If any concluded `failure` since `v<latest-tag>` → stand down. Red CI is evan's lane (red CI on main is the team's
highest-priority state per CLAUDE.md → "Never leave a broken build"). Surface a note to `feature_plans.md` and exit.
Don't ship features on top of red.

### 0c. Verify your own in-flight work

```sh
ls /workspaces/witwave-self/memory/agents/felix/drafts/ 2>/dev/null
```

If a tier-3+ feature is awaiting human approval (`drafts/<slug>.md` with `status: awaiting-human-approval`), pause new
tier-3+ work until approval lands. Lower-tier work can still proceed in parallel.

### 0d. Pin git identity

```sh
git config user.name "felix-agent-witwave"
git config user.email "felix-agent@witwave.ai"
```

Done idempotently via the `git-identity` skill.

## Instructions

### 1. Read the request + ground in current state

Read the request literally. If it's a Discussion number, fetch the body:

```sh
gh api graphql -f query='
{
  repository(owner: "witwave-ai", name: "witwave") {
    discussion(number: <N>) {
      title body author { login } comments(first: 10) { nodes { body author { login } } }
    }
  }
}'
```

If it's a roadmap reference, read the matching section in `docs/product-vision.md`.

Then ground the request in current state:

- **`git log` on the affected scope** since latest tag — what's recently changed?
- **`docs/architecture.md`** — what's the established shape in this area?
- **Existing code** in the relevant subsystem — read the closest sibling component for convention
- **Peer findings memories** if the area overlaps — has evan flagged risks here? finn marked gaps?

You should be able to articulate, before planning, what currently exists and what's being added.

### 2. Tier the work

Apply the tier ladder from CLAUDE.md → "The tier ladder":

| Tier | Shape                                              | Required gates                                        |
| ---- | -------------------------------------------------- | ----------------------------------------------------- |
| 1    | ≤30 lines, no new deps, single-file                | Tests + existing tests pass                           |
| 2    | Single-file new helper, bounded                    | Tests + docs                                          |
| 3    | Multi-file within existing subsystem               | Tests + docs + chart values + **human approval (v1)** |
| 4    | New shared helper module                           | Tests + docs + cross-peer review + **human approval** |
| 5    | New harness endpoint / MCP tool / chart capability | Tests + docs + chart + dashboard + **human approval** |
| 6+   | Cross-cutting / architectural / breaking           | **Human approval; not autonomous**                    |

**v1 autonomous ceiling: tier 3.** Until 30 days of clean tier-1/2 output, tier 3+ requires explicit per-commit human
approval.

**Tier-reset state** lives in `/workspaces/witwave-self/memory/agents/felix/team_state.md`:

```yaml
tier_ceiling: 3 # autonomous ceiling; demoted by 1 on triggered fix-forward
last_demotion_at: null
clean_streak_days: 0 # days since last triggered fix-forward
```

If your computed tier > `tier_ceiling`, route to plan-only mode for that work (Step 3 produces draft; no
implementation). If `tier_ceiling > computed_tier`, proceed.

Record your tier classification with reasoning in `feature_plans.md`. Future audits should be able to second-guess your
tiering.

### 3. Plan the implementation

Write a structured plan to `feature_plans.md` (append) AND to `drafts/<slug>.md` (new file). The plan must include:

```markdown
---
slug: <kebab-case-slug>
request_source: <user-a2a | zora-dispatch | piper-routed-discussion | roadmap-derived>
request_ref: <Discussion # / roadmap section / user A2A timestamp>
computed_tier: <1-10>
tier_reasoning: <one paragraph — why this tier, what makes it not lower or higher>
mode: <run | plan-only>
status: <planning | awaiting-human-approval | approved | in-progress | committed | shipped | deferred>
created_at: <RFC3339>
---

## Request

<verbatim request body>

## Current state

<2-3 paragraphs — what exists today in this area>

## Proposed implementation

### Files affected (with justification)

- `path/to/file.go` — new; <one-line why>
- `path/to/existing.go` — modified; <one-line why>
- ...

### Tests added

- `path/to/test_file.go` — new; covers <which behaviors>
- ...

### Docs updated

- `README.md` — <which section, what change>
- `docs/<file>.md` — <which section, what change>
- ...

### Commit shape

<list of atomic commits in order; each must stand alone>

1. `<commit subject>` — <files in this commit>
2. ...

## Risks identified

<honest enumeration of what could go wrong; one bullet per risk>

## Out-of-scope (deliberately deferred)

<what this PR explicitly does NOT do, and why>
```

If `mode == plan-only` OR `computed_tier > tier_ceiling`, set `status: awaiting-human-approval`, write the draft, and
exit Step 3. Return the draft URL/path to the caller.

Otherwise set `status: approved` (you tiered within ceiling; no external gate) and proceed.

### 4. Implement

Walk the commit shape from your plan, one commit at a time. Per commit:

#### 4a. Apply the edits

Use the Edit / Write tools. Stick to the file list in the plan; if you realize you need a file not in the plan, STOP and
update the plan first. Scope creep is a fix-bar fail (#6 below).

#### 4b. Write the tests in the same commit

Test coverage is non-waivable. The test must demonstrate the new behavior, not just exist. Tests that only assert "the
function returns without error" are insufficient — the test must verify the specific behavior change.

#### 4c. Update docs in the same commit (if applicable)

User-visible features ship with matching docs. The doc update lands in the same commit as the code change so a future
bisect doesn't show a window where code+docs disagree.

#### 4d. Run the local test suite for the affected scope

```sh
# Python (pytest):
cd <relevant-subproject> && python -m pytest -x tests/test_<area>.py

# Go:
cd <relevant-subproject> && go test ./<package>/...

# Vue / dashboard:
cd clients/dashboard && npm run test
```

If red → fix-forward in the same commit (re-edit, re-test). If you can't fix-forward in the session, surface to
`feature_plans.md` with `[deferred: tests-red-during-implementation]` and exit without committing.

#### 4e. Lint + format

```sh
# Python
ruff format <files>
ruff check --fix <files>

# Go
gofmt -w <files>
goimports -w <files>

# Markdown / YAML / JSON
prettier --write <files>
```

Nova will format-correct on her cadence if you miss; but doing it inline keeps the commit cleaner.

#### 4f. Apply the fix-bar (non-waivable check)

Before committing, walk every rule from CLAUDE.md → "The fix-bar":

1. Is this genuinely a feature (not a bug / gap / doc fix)? ✓
2. Is the tier correctly identified, within the ceiling? ✓
3. Test coverage is present, demonstrates the behavior? ✓
4. Local test suite passes for affected scope? ✓
5. Docs updated to match? ✓
6. No scope creep beyond the plan? ✓
7. Commit is atomic + revertable? ✓

If any fails → revert the working-tree changes for that commit, mark `feature_plans.md` entry as `status: blocked`,
surface to memory, exit.

#### 4g. Commit

```sh
git add <specific-files-from-plan>
git commit -m "$(cat <<'EOF'
<conventional-commit subject — feat/feat(scope) prefix>

<body — what changed, why, and reference to the feature plan slug>

Slug: <slug>
Tier: <N>
Co-Authored-By: <model> <noreply@anthropic.com>
EOF
)"
```

Use `feat:` or `feat(<scope>):` prefix. Conventional commits are how zora's release-warranted check identifies feature
work (weighted 2.0 — substantive).

### 5. Delegate push + CI watch to iris

```text
call-peer peer=iris prompt="felix here — feature commits ready: <N commits, range a1b2c3..d4e5f6>.
Slug: <slug>. Tier: <N>. Please push + CI watch.

Per fix-forward semantics: if CI red, ping me on this same thread with the failing-job logs and I'll
fix-forward in the same session. If CI green, no action needed; the work is done."
```

Don't push yourself. Don't go around iris.

### 6. Handle iris's reply

- **CI green** → mutate `feature_plans.md` entry to `status: shipped` with the commit range and reply timestamp. Done.
- **CI red** → enter fix-forward mode. Read iris's reply for the failing-job logs. Diagnose the failure. Apply the fix
  in a NEW commit (not amending). Re-run the affected test scope locally. Re-apply the fix-bar. Commit. Ask iris to push
  again. Repeat up to 2 fix-forward attempts.
- **2 fix-forward attempts failed** → revert all your feature commits via batch-revert; mark `feature_plans.md`
  `status: reverted`; surface to `escalations.md` for human review.

### 7. Log the run

Append a final block to `feature_plans.md` summarizing:

- **slug:** <slug>
- **tier:** <N>
- **commits:** <range>
- **status:** shipped | reverted | blocked | deferred
- **request_source:** <source>
- **fix_forward_attempts:** <0-2>
- **ci_outcome:** <green-first-try | green-after-fix | red-2-attempts-reverted>
- **notes:** <one-line — anything surprising>

If `status == shipped`, also increment the `clean_streak_days` counter in `team_state.md`. If `status == reverted` AND
the revert was triggered by your own commit (not external), reset `clean_streak_days` to 0 AND demote `tier_ceiling` by
1 with a 7-day countdown to re-promotion.

### 8. Return a one-paragraph summary

To the caller:

> Feature `<slug>` at tier `<N>`: <shipped|reverted|blocked|deferred>. <Commit range>. CI: <outcome>. Plan in
> `feature_plans.md`; <one-line context>.

## Failure modes worth surfacing explicitly

- **Request straddles the line.** If the request is ambiguously a feature vs gap vs bug — surface to user via memory,
  defer. Don't guess. The peer-boundary clarity is load-bearing.
- **Tier is genuinely uncertain.** Tier reasoning belongs in the plan. If you can't argue confidently for one tier,
  default to the higher tier and route through human approval.
- **A peer has surfaced a conflicting finding.** If evan has flagged a risk in the same area, OR finn has marked a gap,
  AND your feature touches that file — ask the peer (via `call-peer`) before committing. Don't land work on top of an
  unresolved peer finding.
- **The request references an architectural change** ("rewrite the harness scheduler", "split the backend into N
  services"). That's tier 7+; produce a plan-only draft and exit. Architecture is a human decision.
- **`call-peer iris` times out / 5xx's repeatedly.** Iris may be in stuck-peer escalation. Surface to `escalations.md`;
  don't auto-push around iris.

## Out of scope for this skill

- **Bug fixes / gap-fills / doc maintenance / formatting / releases** — peer-owned lanes; redirect.
- **Architectural changes / breaking changes** — surface plan to user; no autonomous implementation.
- **Removing existing functionality** — explicit human approval required.
- **Modifying memory in peer namespaces** — never. Use `call-peer` for cross-peer communication.
- **Direct `git push`** — iris's lane.
- **Direct `gh api` writes** (creating issues, PRs, releases) — iris's lane.
- **Cluster ops** (kubectl, helm install/upgrade, secret creation) — operator / human lane.

## When to invoke

- **User A2A** — "felix, build X", "felix, implement Y", "felix, plan a feature for Z".
- **Zora dispatch** — she may route a feature request from the team inbox to you.
- **Piper-routed Discussion** — when a feature request lands in GitHub Discussions, Piper logs to
  `feature-requests-from-users.md` (parallel to her `bugs-from-users.md`); zora reads that file each tick and dispatches
  felix when there's an unrouted entry.

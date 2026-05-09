---
name: docs-cleanup
description:
  Top-level documentation maintenance orchestrator. Runs the full docs sweep — Tier 1 mechanical reformatting
  (`docs-validate` + `docs-links`, ALL .md files including Cat A/B), then Tier 2 semantic checks (`docs-verify` +
  `docs-consistency`, Cat C only) — commits the auto-fixable changes per category, and delegates the push to iris via
  `call-peer`. The single entry point for "ask the group to clean up the docs". Trigger when the user says "clean up
  docs", "docs cleanup", "do a full docs sweep", "fix all the documentation", or similar.
version: 0.1.0
---

# docs-cleanup

The single entry point for a complete documentation maintenance pass. Coordinates the existing per-tier skills, batches
commits per category, and hands the push off to iris (the team's git plumber). When a caller says "clean up the docs",
this is what runs.

## Division of labour

You are the documentation domain owner. Iris is the git plumber who publishes the work. This skill encodes that
contract:

- **Kira does:** source verify, identity pin, run all Tier 1 + Tier 2 docs skills, commit the resulting changes locally.
- **Iris does:** receive a `call-peer` request from kira, run her `git-push` skill on the shared workspace checkout,
  return success or surface the failure.

This split keeps domain expertise (knowing what "clean docs" means) on the docs owner and git posture
(refuse-on-conflict, no-force, rebase-on-race) on the agent whose responsibility that is.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path
- **`<branch>`** — default branch (typically `main`)

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

Two checks:

- The first command confirms the checkout exists. If missing, log "source tree absent at scan time" to deferred-findings
  memory and **stop**. (Iris owns clone/sync — see CLAUDE.md → Responsibilities → 1.)
- The second should print nothing. A non-empty working tree means uncommitted changes from a previous run or another
  agent — **stop and log**, don't try to stash or reset.

### 2. Pin git identity

Invoke the `git-identity` skill. Idempotent — safe to run even if identity is already set. Without it, the per-category
commits in step 7 fail with "Please tell me who you are."

### 3. Capture the pre-scan ref

```sh
PRE_SCAN_SHA=$(git -C <checkout> rev-parse HEAD)
```

Used at step 8 to compute the diff range that landed and to phrase the push delegation cleanly.

### 4. Run Tier 1 — `docs-validate`

Invoke the `docs-validate` skill (target: ALL `*.md` including Cat A and Cat B — pure mechanical reformatting is safe
across all categories). Capture its summary: files Prettier reformatted, files markdownlint --fix touched, remaining
diagnostics that weren't auto-fixable.

If files were modified, commit them as one batch:

```sh
git -C <checkout> add -A
git -C <checkout> commit -m "docs: apply markdownlint + prettier auto-fixes"
```

If nothing changed, don't commit (silence is the right output).

### 5. Run Tier 1 — `docs-links`

Invoke the `docs-links` skill (target: ALL `*.md`). Capture unambiguous fixes applied + ambiguous findings logged to
memory.

If files were modified, commit them as a separate batch:

```sh
git -C <checkout> add -A
git -C <checkout> commit -m "docs: fix broken internal links and anchors"
```

Per-category commits keep the log bisectable — `docs-validate` fixes shouldn't ride in the same commit as link fixes.

### 6. Run Tier 2 — `docs-verify` (Cat C only)

Invoke the `docs-verify` skill. Read-only: it logs broken references (paths that don't exist, command examples that no
longer match the CLI, version numbers that drifted) to memory without touching files.

No commit produced — Tier 2 is memory-log only.

### 7. Run Tier 2 — `docs-consistency` (Cat C only)

Invoke the `docs-consistency` skill. Read-only: it logs cross-file disagreements (version mismatches, subproject README
↔ root README disagreements, command-surface drift) to memory.

No commit produced.

### 8. Decide whether to push

Compare HEAD to `PRE_SCAN_SHA`:

```sh
git -C <checkout> log --oneline ${PRE_SCAN_SHA}..HEAD
```

- **No commits** → nothing to push. Skip directly to step 10 with the report.
- **One or more commits** → proceed to step 9.

### 9. Delegate the push to iris via `call-peer`

This is the explicit handoff. You committed; iris pushes. Use the `call-peer` skill with `iris` as the target peer.

The prompt to iris should be unambiguous and self-contained:

> _"docs-cleanup batch ready to publish. <N> commit(s) on `<branch>` since `<PRE_SCAN_SHA>`. Subjects:_
>
> - _<commit subject 1>_
> - _<commit subject 2>_
>
> _Please run `git-push` to land them on origin/<branch>."_

This way iris (a) has full context for her audit log, (b) doesn't need to ask follow-up questions, and (c) if the push
hits the standard sibling-pushed-first race, her git-push skill handles it without bouncing back to you.

Capture iris's reply:

- **Success** — extract the pushed commit range from her message; carry forward to the report in step 10.
- **Failure** (auth, conflict on rebase retry, branch protection, stuck-commits state, etc.) — surface the failure
  verbatim. Do NOT try to push yourself; iris's domain handles git posture, and overriding her decision (e.g.
  force-pushing to "win" a race) violates the team's rules.
- **Iris unreachable / call-peer error / timeout** — treat as a failure equivalent to iris reporting failure; surface
  the call-peer error verbatim and do NOT silently mark this run as successful.

**Caller-return semantics — load-bearing.** Whatever your overall orchestrator status is, it MUST reflect iris's
outcome:

- **`completed-and-pushed`** — Tier 1 + Tier 2 ran; commits made; iris confirmed the push landed.
- **`completed-locally-push-failed`** — Tier 1 + Tier 2 ran; commits made; iris reported failure of any kind
  (stuck-commits, conflict, auth, network, branch protection, unreachable). Embed iris's verbatim error or the call-peer
  error.
- **`completed-no-commits`** — orchestrator ran clean; nothing to push.
- **`stood-down`** — pre-flight gate failed (source tree missing, etc.).

Zora's stuck-commits triage flow in `dispatch-team` Step 2c reads iris's `stuck_commits.md` directly on every tick — so
even if your orchestrator returned `completed-locally-push-failed`, zora has the diagnostic data without you having to
relay it. But your status field still must NOT lie about whether the push landed; she uses it to decide whether to fire
the next cadence dispatch.

### 10. Report

Return a structured summary to the caller:

- **Pre-scan SHA** and **post-scan SHA** (the latter from iris's push confirmation, OR the local HEAD if there was
  nothing to push)
- **Per-skill counts:** files reformatted, link fixes applied, verify findings logged, consistency findings logged
- **Per-category breakdown** of where findings landed:
  - How many in Cat A (agent identity)
  - How many in Cat B (local dev tooling)
  - How many in Cat C (project / OSS)
- **Iris's push outcome** — success (with commit range) or failure (with the verbatim error she surfaced)
- **Pointer to deferred-findings memory** if any new entries landed there from `docs-verify` / `docs-consistency`

For scheduled / heartbeat-fired invocations the report goes to the standard log; for on-demand invocations the caller
gets it in the A2A response.

## When to invoke

- **On demand** — the user or a sibling agent sends "clean up docs", "do a full docs sweep", "fix the documentation",
  "docs cleanup". This is the primary trigger.
- **After a major refactor or release** — a good rhythm for surfacing accumulated drift.

## Failure handling

- **Source tree missing** (step 1): log + stop. Iris owns clone/sync.
- **Dirty tree on entry** (step 1): log + stop. Don't try to stash or reset.
- **Skill failure inside Tier 1** (steps 4–5): record what succeeded, surface the partial state. Don't proceed to push;
  better to leave the partial commits unpushed for a human to inspect than to publish a half-finished sweep.
- **Iris unreachable** (step 9): if `call-peer` to iris fails (peer not in cache → run `discover-peers` first; peer
  unreachable → surface and stop). Local commits stay in the workspace until iris is back; the next `docs-cleanup` run
  will re-detect them and re-attempt the delegation.

## Out of scope for this skill

- **Doing the actual docs work** — that's the subordinate skills' job. This skill orchestrates them.
- **Pushing the batch yourself** — iris's domain. Don't reach for `git-push` even though the skill is available; the
  contract is kira-commits / iris-pushes.
- **Cutting a release after the cleanup** — that's iris's `release` skill, invoked separately. If you want a cleanup +
  release sequence, that's two A2A calls (cleanup → confirm iris pushed → ask iris to release), not one skill.
- **Filing GitHub issues** for Tier 2 findings — explicitly out per CLAUDE.md scope ruling. Findings go to memory.

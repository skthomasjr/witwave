---
name: code-cleanup
description:
  Top-level code-hygiene orchestrator. Runs Tier 1 (`code-format`) for mechanical reformatting + Tier 2 (`code-verify`)
  for comment-vs-code semantic checks. Commits the auto-fixable changes per language and delegates the push to iris via
  `call-peer`. The single entry point for "ask nova to clean up the code". Tier 3 (`code-document`) is NOT included by
  default — it runs on a separate cadence with its own discipline. Trigger when the user says "code cleanup", "clean up
  code", "do a code-hygiene sweep", or similar.
version: 0.1.0
---

# code-cleanup

The single entry point for a code-hygiene maintenance pass. Coordinates the Tier 1 + Tier 2 skills, batches commits per
language, and hands the push off to iris (the team's git plumber).

`code-document` (Tier 3 — author missing comments) is **not part of this orchestrator** by default. Authoring runs on a
slower cadence and requires its own grounding discipline; bundling it here would force every cleanup pass to either
include or skip authoring, when the right answer is to keep them on independent clocks. Callers who want both should
invoke `code-cleanup` and then `code-document` separately, or trigger them via different cadences.

## Division of labour

You are the code-hygiene domain owner. Iris is the git plumber who publishes the work. This skill encodes that contract:

- **Nova does:** source verify, identity pin, run Tier 1 + Tier 2 code skills, commit the resulting changes locally per
  language.
- **Iris does:** receive a `call-peer` request from nova, run her `git-push` skill on the shared workspace checkout,
  return success or surface the failure.

Same shape as the kira-commits / iris-pushes contract for docs.

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
  memory and **stop**. Iris owns clone/sync.
- The second should print nothing. A non-empty working tree means uncommitted changes from a previous run or another
  agent — **stop and log**, don't try to stash or reset.

### 2. Pin git identity

Invoke the `git-identity` skill. Idempotent — safe to run even if identity is already set. Without it, the per-language
commits in step 4 fail with "Please tell me who you are."

### 3. Capture the pre-cleanup ref

```sh
PRE_CLEANUP_SHA=$(git -C <checkout> rev-parse HEAD)
```

Used at step 6 to compute what landed and to phrase the push delegation cleanly.

### 4. Run Tier 1 — `code-format`

Invoke the `code-format` skill. It runs each language's formatter, applies auto-fixes, and produces ONE COMMIT PER
LANGUAGE for the files modified. Capture its summary: per-language file counts + commit SHAs + remaining diagnostics
that weren't auto-fixable.

(`code-format` is internally responsible for committing each language's batch; this orchestrator doesn't double-commit.
It only sequences the skills.)

### 5. Run Tier 2 — `code-verify`

Invoke the `code-verify` skill. Read-only: it logs comment-vs-code mismatches to memory without touching files.

No commit produced — Tier 2 is memory-log only.

### 6. Decide whether to push

Compare HEAD to `PRE_CLEANUP_SHA`:

```sh
git -C <checkout> log --oneline ${PRE_CLEANUP_SHA}..HEAD
```

- **No commits** → nothing to push. Skip directly to step 8 with the report.
- **One or more commits** → proceed to step 7.

### 7. Delegate the push to iris via `call-peer`

This is the explicit handoff. You committed; iris pushes. Use the `call-peer` skill with `iris` as the target peer.

The prompt to iris should be unambiguous and self-contained:

> _"code-cleanup batch ready to publish. <N> commit(s) on `<branch>` since `<PRE_CLEANUP_SHA>`. Subjects:_
>
> - _<commit subject 1>_
> - _<commit subject 2>_
>
> _Please run `git-push` to land them on origin/<branch>."_

Capture iris's reply:

- **Success** — extract the pushed commit range from her message; carry forward to the report in step 8.
- **Failure** (auth, conflict on rebase retry, branch protection, stuck-commits state, etc.) — surface the failure
  verbatim. Do NOT try to push yourself; iris's domain handles git posture.
- **Iris unreachable / call-peer error / timeout** — treat as a failure equivalent to iris reporting failure; surface
  the call-peer error verbatim and do NOT silently mark this run as successful.

**Caller-return semantics — load-bearing.** Whatever your overall orchestrator status is, it MUST reflect iris's
outcome:

- **`completed-and-pushed`** — code-format + code-verify ran; commits made; iris confirmed the push landed.
- **`completed-locally-push-failed`** — code-format + code-verify ran; commits made; iris reported failure of any kind
  (stuck-commits, conflict, auth, network, branch protection, unreachable). Embed iris's verbatim error or the call-peer
  error.
- **`completed-no-commits`** — orchestrator ran clean; nothing to push.
- **`stood-down`** — pre-flight gate failed (source tree missing, etc.).

Zora's stuck-commits triage flow in `dispatch-team` Step 2c reads iris's `stuck_commits.md` directly on every tick — so
even if your orchestrator returned `completed-locally-push-failed`, zora has the diagnostic data without you having to
relay it. But your status field still must NOT lie about whether the push landed; she uses it to decide whether to fire
the next cadence dispatch.

### 8. Report

Return a structured summary to the caller:

- **Pre-cleanup SHA** and **post-cleanup SHA**
- **Per-language counts** from `code-format`: files reformatted, lint auto-fixes applied, remaining diagnostics
- **Per-language counts** from `code-verify`: files scanned, comments examined, mismatches logged
- **Iris's push outcome** — success (with commit range) or failure (with the verbatim error she surfaced)
- **Pointer to deferred-findings memory** if any new entries landed

For scheduled / heartbeat-fired invocations the report goes to the standard log; for on-demand invocations the caller
gets it in the A2A response.

## When to invoke

- **On demand** — the user or a sibling agent sends "code cleanup", "clean up code", "do a code-hygiene sweep". This is
  the primary trigger.
- **After a major refactor or release** — surfaces accumulated formatting drift and any comment-vs-code mismatches that
  crept in.

## Failure handling

- **Source tree missing** (step 1): log + stop. Iris owns clone/sync.
- **Dirty tree on entry** (step 1): log + stop. Don't stash or reset.
- **Skill failure inside Tier 1** (step 4): record what succeeded, surface the partial state. Don't proceed to push;
  better to leave partial commits unpushed for human inspection than publish a half-finished sweep.
- **Iris unreachable** (step 7): if `call-peer` to iris fails (peer not in cache → run `discover-peers` first; peer
  unreachable → surface and stop). Local commits stay in the workspace until iris is back; the next `code-cleanup` run
  will re-detect them and re-attempt the delegation.

## Out of scope for this skill

- **Doing the actual code work** — that's the subordinate skills' job. This skill orchestrates them.
- **Running `code-document`** — Tier 3 has its own discipline and cadence; not part of routine cleanup.
- **Pushing the batch yourself** — iris's domain. Don't reach for `git-push` even though the skill is available.
- **Filing GitHub issues for diagnostics** — explicitly out per CLAUDE.md scope ruling. Findings go to memory.
- **Cutting a release after the cleanup** — that's iris's `release` skill, invoked separately.

---
name: docs-scan
description:
  Tier 1 docs orchestrator — quick mechanical pass. Verifies the source tree, pins git identity, runs each focused Tier
  1 docs skill (validate + links), commits the resulting changes per category, and delegates the push to iris via
  `call-peer`. For the full Tier 1 + Tier 2 sweep that also runs `docs-verify` and `docs-consistency`, use
  `docs-cleanup` instead. Trigger when the user says "scan docs", "check documentation", "run docs maintenance", or
  similar.
version: 0.1.0
---

# docs-scan

The umbrella skill kira invokes to do a complete pass over the documentation surface. It is the only docs skill the
heartbeat fires; all other docs-\* skills are subordinates that report findings + apply their own auto-fixes, with
`docs-scan` deciding how to commit and publish them.

The procedure is intentionally linear: each focused skill runs to completion, the working tree is staged after each, and
a separate commit is made per category (so `git log` stays bisectable and each fix is individually revertable).

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

- The first command confirms the checkout exists. If it doesn't, log "source tree absent at scan time" to your
  deferred-findings memory and **stop**. Do not try to clone or sync — that's iris's responsibility (see your CLAUDE.md
  → Responsibilities → 1).
- The second command should print nothing. If the working tree isn't clean (uncommitted changes from a previous run, or
  someone else editing on the shared volume), **stop and log**. Don't try to stash or reset; you don't know what the
  changes represent.

### 2. Pin git identity

Invoke the `git-identity` skill. It's idempotent — safe to run even if identity is already set. Without it, the
per-category commits later would fail with "Please tell me who you are."

### 3. Capture the pre-scan ref

```sh
PRE_SCAN_SHA=$(git -C <checkout> rev-parse HEAD)
```

Used at step 8 to compute the diff range that landed (the report back to the caller cites this).

### 4. Run docs-validate

Invoke the `docs-validate` skill. Capture its summary (files fixed, remaining diagnostics).

If any files were modified:

```sh
git -C <checkout> add -A
git -C <checkout> commit -m "docs: apply markdownlint + prettier auto-fixes"
```

If nothing changed, **don't commit** — empty commits are noise.

### 5. Run docs-links

Invoke the `docs-links` skill. Capture its summary (auto-fixes applied, ambiguous findings logged).

If any files were modified:

```sh
git -C <checkout> add -A
git -C <checkout> commit -m "docs: fix broken internal links and anchors"
```

Don't commit if nothing changed.

### 6. Decide whether to push

Compare HEAD to `PRE_SCAN_SHA`:

```sh
git -C <checkout> log --oneline ${PRE_SCAN_SHA}..HEAD
```

- **No commits since PRE_SCAN_SHA** → nothing to push. Report "scan clean, no changes" to the caller and exit.
- **One or more commits** → proceed to push.

### 7. Delegate the push to iris

Don't push yourself — the contract per CLAUDE.md is kira-commits / iris-pushes. Invoke the `call-peer` skill with `iris`
as the target peer and a self-contained prompt:

> _"docs-scan batch ready to publish. <N> commit(s) on `<branch>` since `<PRE_SCAN_SHA>`. Subjects:_
>
> - _<commit subject 1>_
> - _<commit subject 2>_
>
> _Please run `git-push` to land them on origin/<branch>."_

Capture iris's reply. Success = pushed commit range; failure = verbatim error from her side. Don't improvise around her
decision (no `git-push` from kira even when the skill is no longer installed; if iris is down, the local commits stay
unpushed until next attempt).

### 8. Report

Return a structured summary to the caller:

- Pre-scan SHA
- Post-scan SHA
- Per-category commit list (subject lines + short SHAs)
- Counts: files validated, files link-fixed, ambiguous findings logged
- Pointer to the deferred-findings memory file if any new entries landed there

For scheduled (heartbeat-fired) invocations the report goes to the standard log; for on-demand invocations the caller
gets it in the A2A response.

## When to invoke

- **Scheduled** — every 6 hours via heartbeat (per kira's CLAUDE.md → Cadence section).
- **Reactive** — on every push to `main` that touches `*.md` files (continuation-fired).
- **On-demand** — the user or a sibling agent sends "scan docs", "check documentation", or similar via A2A.

## Out of scope for this skill

- Doing the actual validation / link-fixing work — that's the subordinate skills' job.
- Pulling source — iris owns that. If the tree isn't ready, kira stands down.
- Committing without batching by category (one big mixed commit is harder to revert than per-category commits).
- Filing GitHub issues for ambiguous findings — explicitly out per your CLAUDE.md scope ruling. Findings go to memory;
  the user reviews them on their own cadence.
- Force-anything during push — `git-push` refuses; if you've reached the point where force feels necessary, the right
  answer is to stop and ask.

---
name: docs-consistency
description:
  Cross-check Category C documentation for internal agreement — version numbers across files, claims about what each
  subproject does vs its parent's description, root README links to docs/ files (and vice versa) that all resolve,
  per-subproject README claims about commands matching the actual CLI surface. Memory-log mismatches; never auto-fix
  (the right resolution is always a judgment call). Trigger when the user says "check doc consistency", "are the docs in
  agreement?", or as a step inside `docs-cleanup`.
version: 0.1.0
---

# docs-consistency

Find places where two or more Category C documentation files disagree with each other. Out of scope: Category A (agent
identity under `.agents/**`) and Category B (repo-root `CLAUDE.md`, `AGENTS.md`, `.claude/skills/**`) — those are agent
prompts, not human-facing docs, and consistency among them is a different kind of concern handled separately.

This skill is a **read-only logger.** Like `docs-verify`, the right resolution to a consistency mismatch is always a
human judgment call: when the root README says version 0.12 and the chart says 0.13, the fix is "update one to match the
other," but which one is correct depends on what the truth actually is. Findings go to your deferred-findings memory
file.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
```

Stand down + log if missing.

### 2. Enumerate Category C docs

Same enumeration as `docs-verify` — re-use the same path-pattern filter. Cat A and Cat B are excluded.

### 3. Run the consistency checks

For each check below, walk the Cat C corpus and look for disagreements. Report mismatches with all source files cited so
the human reviewing knows which docs say what.

#### Check A — Version-number agreement

Extract version-shaped tokens (`v?\d+\.\d+\.\d+(-\w+)?`) from each Cat C file. Cluster by context:

- "Latest release" claims (root README, install scripts, brew formula references, CHANGELOG) — should all reference the
  most recent stable tag.
- Tool pins (markdownlint, prettier, npx versions) — should match what `.github/workflows/*.yml` actually pins.
- API version refs (`v1alpha1`, `v1beta1`) — should agree across charts/CRDs/operator code refs.

When two Cat C files disagree on the same logical version, that's a finding.

#### Check B — Subproject README ↔ root README claims

Root `README.md` describes the subproject inventory at a high level (e.g. "the `ww` CLI lives at `clients/ww/`"). Each
subproject README describes its own role.

For each subproject README under `clients/`, `charts/`, `tools/`, `helm/`, `operator/`, `harness/`, `backends/`:

- Does the root README mention this subproject?
- If yes, does the root's one-line description align with the subproject's own opening paragraph?
- Are the subproject's stated capabilities a subset of (or at least consistent with) what the root claims it does?

Mismatches go in memory with both excerpts side-by-side.

#### Check C — Cross-doc link integrity (Cat C only)

`docs-links` already validates internal `[text](path)` markdown links across the whole corpus. This check is the
higher-level companion: walk Cat C files and verify that **named-but-not- linked** references resolve too. E.g. prose
like "see the bootstrap doc for setup" or "as covered in CONTRIBUTING.md" — do those referenced files exist and are they
current?

This is heuristic — search for sentence patterns like "see X", "covered in X", "as documented in X", "the X doc", and
verify X resolves to a Cat C file path. Heuristic matches need a sanity check before flagging (no false positives on
common phrases).

#### Check D — Command-surface agreement

Per-subproject READMEs that describe a CLI surface (notably `clients/ww/README.md` and `clients/ww/WALKTHROUGH.md`)
should agree with the root README's CLI examples and with the actual cobra command tree.

For each `ww <verb>` example in Cat C docs:

- Does that subcommand exist in `clients/ww/cmd/`?
- Are the flags shown in the example real flags?

Cross-doc disagreement (one doc shows `--gitsync`, another shows `--git-sync`) is a finding regardless of which is
correct.

### 4. Log findings to memory

Append to `/workspaces/witwave-self/memory/agents/<your-name>/project_doc_findings.md` under a new section.
Each finding's heading carries a **status marker** matching the team-wide schema (parallel to evan's
`bug-work` format) so zora's backlog counter reads every peer's findings file uniformly:

- **`[pending]`** — default for newly-detected disagreements. Real cross-doc mismatch awaiting a human
  decision on which file to update.
- **`[flagged: <reason>]`** — used when the disagreement has a *specific* judgment-call obstacle worth
  recording inline (e.g., `[flagged: both-files-may-be-historically-correct-need-current-spec]`).
- **`[fixed: <SHA>]`** — when one or both files get reconciled later, mutate to record the resolving
  commit. zora's counter treats `[fixed:]` as closed; `[pending]` and `[flagged:]` as open backlog.

Format:

```markdown
## YYYY-MM-DD — docs-consistency

### <check name> — <brief summary> [pending]

- **Disagreement:** files A says X, file B says Y
  - `<file-A>:<line>`: "<excerpt>"
  - `<file-B>:<line>`: "<excerpt>"
- **Likely truth:** <one-line — code/release-tag evidence if available; "needs human ruling" otherwise>
- **Recommended action:** <which file to update, or flag for human if both could be wrong>
```

**Existing narrative-format entries** (from runs before 2026-05-07) stay as-is — don't re-mark retroactively.
Only new sections written from this skill onward use the marker schema; zora's interim per-peer adapter handles
the mixed state during the transition.

### 5. Report

Return a structured summary:

- Total Cat C files scanned
- Per-check counts: how many disagreements found in each (version, subproject↔root, prose-references, command-surface)
- Pointer to the deferred-findings memory entry just written

## When to invoke

- Inside `docs-cleanup` (the orchestrator).
- On demand: "are the docs in agreement?", "check doc consistency", "find conflicting claims".
- After a release or a CLI rename — that's when version drift and command-surface drift are most likely.

## Out of scope for this skill

- **Cat A / Cat B** — explicitly off-limits per the doc-categories policy. Consistency between agent identity files
  (e.g. iris's CLAUDE.md vs kira's) is a different kind of check that needs a different posture.
- **Auto-fixing** — every disagreement is a judgment call. Findings land in memory; humans decide.
- **External-resource consistency** (e.g. README claims about Discord channels, blog posts, third-party docs) — out of
  scope; these change for reasons unrelated to this repo.
- **Style / voice consistency** — that's editorial work, not factual consistency. Out of scope.

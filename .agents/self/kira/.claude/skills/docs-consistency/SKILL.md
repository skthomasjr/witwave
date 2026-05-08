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

Find places where two or more documentation files disagree with each other. Primary scope is Category C (project /
OSS docs); a single named exception covers Cat-A `TEAM.md` because it pairs with Cat-C `bootstrap.md` as the team's
canonical roster narrative (Check E). General Cat-A consistency (per-agent CLAUDE.md / SKILL.md / agent-card.md
agreement) and Category B (repo-root `CLAUDE.md`, `AGENTS.md`, `.claude/skills/**`) remain out of scope — those are
agent prompts, not human-facing docs, and consistency among them is a different kind of concern handled separately.

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

#### Check E — Team-roster consistency: TEAM.md ↔ bootstrap.md

Two narrative-of-the-team files — Cat-A `.agents/self/TEAM.md` (the roster, topology, mission per agent) and Cat-C
`docs/bootstrap.md` (the from-scratch deploy walkthrough) — must agree on which agents currently exist on the team
and how each one is deployed. This is the **only** check in `docs-consistency` that reaches into Cat A; it does so
read-only against `TEAM.md` specifically, not against per-agent CLAUDE.md / agent-card.md / SKILL.md (those stay
out of scope under the doc-categories policy).

Why this check exists: when the human adds a new agent (or a manager dispatches one), `TEAM.md` updates immediately
because it's the visible roster, but `bootstrap.md` lags — historically it has been 2-3 agents behind reality.
Stale bootstrap means a new operator following the doc end-to-end ends up with a partial team. This check catches
that drift so kira can flag the missing/extra steps.

**Inputs.** Read both files fully. From `TEAM.md` extract the "current team" / active-roster section — the named
agents, their one-line role descriptions, and the order they're presented. From `bootstrap.md` extract every
"Step N — Deploy <name>" heading and the `ww agent create <name>` invocation under it.

**Verifications.**

1. **Roster membership match.** The set of agents in `TEAM.md`'s current-team list MUST equal the set of agents
   with a "Deploy <name>" step in `bootstrap.md`. Mismatch directions:
   - Agent in `TEAM.md` but missing in `bootstrap.md` → `[pending]`, action = "add Step N — Deploy <name>".
   - Agent in `bootstrap.md` but missing in `TEAM.md` → `[pending]`, action = "either add to roster or remove
     stale step". (Likely the agent was decommissioned but the step survived.)
2. **Deploy-shape uniformity.** Across all "Deploy <name>" steps in `bootstrap.md`, the `ww agent create` invocations
   should share the same skeleton — same `--namespace`, `--workspace`, `--with-persistence`, `--backend`,
   `--harness-env CONVERSATIONS_AUTH_DISABLED=true`, `--backend-env claude:CONVERSATIONS_AUTH_DISABLED=true`,
   `--backend-secret-from-env CLAUDE_CODE_OAUTH_TOKEN`, and `--gitsync-bundle ...:.agents/self/<name>` patterns.
   An agent step that omits one of these (or uses a divergent value, e.g. a different namespace) is a finding.
   This catches half-pasted bootstrap steps before they bite a new operator.
3. **Verification step counts.** The "Verify the team" closing step at the end of `bootstrap.md` typically claims
   "expect N rows in `ww agent list`" or similar — N must equal the active-roster count. Off-by-one here is a
   common drift symptom when a new agent is added.
4. **Topology diagram drift** (best-effort). If `TEAM.md` includes an ASCII team-topology diagram, every named
   node in the diagram should appear in the current-team list. Nodes-not-in-roster is a finding; this is the
   tail end of "we added someone and forgot to update the diagram."

**Live-cluster cross-check** (optional / out-of-scope today). Eventually this check could call
`kubectl get pods -n witwave-self -l app.kubernetes.io/managed-by=witwave-operator` and compare the deployed-pod
set against the roster. Today kira's runtime doesn't have cluster RBAC, so don't attempt it; record the finding
as `[flagged: live-check-unavailable]` if you want to track the gap. The file-vs-file checks above are the
primary signal.

### 4. Log findings to memory

Append to `/workspaces/witwave-self/memory/agents/<your-name>/project_doc_findings.md` under a new section. Each
finding's heading carries a **status marker** matching the team-wide schema (parallel to evan's `bug-work` format) so
zora's backlog counter reads every peer's findings file uniformly:

- **`[pending]`** — default for newly-detected disagreements. Real cross-doc mismatch awaiting a human decision on which
  file to update.
- **`[flagged: <reason>]`** — used when the disagreement has a _specific_ judgment-call obstacle worth recording inline
  (e.g., `[flagged: both-files-may-be-historically-correct-need-current-spec]`).
- **`[fixed: <SHA>]`** — when one or both files get reconciled later, mutate to record the resolving commit. zora's
  counter treats `[fixed:]` as closed; `[pending]` and `[flagged:]` as open backlog.

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

**Existing narrative-format entries** (from runs before 2026-05-07) stay as-is — don't re-mark retroactively. Only new
sections written from this skill onward use the marker schema; zora's interim per-peer adapter handles the mixed state
during the transition.

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

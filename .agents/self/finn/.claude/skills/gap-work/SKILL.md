---
name: gap-work
description:
  Find and fill functionality gaps — what's missing relative to what should be there. Single end-to-end pass per
  run: enumerate eleven gap-source categories, persist candidates, validate via gauntlet, fill-or-flag per the
  caller's risk tier (1-10), commit per gap, delegate push + CI watch to iris, fix-forward on test/CI failure
  (revert is the bounded fallback). State lives in commits and `project_finn_findings.md` memory only — no
  GitHub issues, no labels, no multi-session funnel. Trigger when the user says "fill gaps", "find gaps", "do
  gap work", "find missing functionality", or specifies tier/sections (e.g. "fill gaps in operator tier 5").
version: 0.1.0
---

# gap-work

Single-pass **find AND fill** for missing functionality in the witwave-ai/witwave repo.

State lives in two places only: **commits** (the gaps you filled) and your `project_finn_findings.md` memory file
(the gaps that needed human judgement, were above the run's risk tier, or hit the fix-bar). No GitHub issues,
no labels, no multi-session funnel. The pass IS supposed to fill what it can — discovery-only is not the
team-deployed pattern.

## Inputs

Parse from the caller's prompt:

- **`tier`** — integer 1-10. Default `1` if unspecified. Refuse cleanly if outside 1-10. Tier controls which
  gaps are eligible for auto-fill; gaps above the tier ceiling get logged as `[flagged: above-tier]` for the
  next higher-tier run (or human review).
- **`sections`** — comma-separated list of section names or aliases. Default `all-day-one` if unspecified.
  Refuse cleanly on unknown sections.
- **`focus`** *(optional)* — one of `operator-parity`, `e2e-tests`, `cli-ux`, `dashboard-catchup`, or empty.
  Biases the run toward that subsystem class — e.g. `focus=operator-parity` skips the unrelated nine gap
  sources and just walks the operator↔helm-chart comparison. Default empty: all eleven sources.

Parse permissively. `gap-work tier=3 sections=operator,clients/ww`, `find gaps in harness tier 5`, `fill
gaps`, `do gap work focus=operator-parity` — all valid.

## Sections

Same 17-section partition evan uses, plus the same out-of-scope rules (test code, generated/vendored,
markdown). When you scan section S, you read the source under S, BUT you also read the project-level
documentation surface (`AGENTS.md`, root `CLAUDE.md`, root `README.md`, per-subproject READMEs, `docs/`,
`CHANGELOG.md`) to identify doc-promises and convention drift relating to S.

Aliases: `all-python`, `all-go`, `all-backends`, `all-tools`, `all-charts`, `all-day-one` — same as evan.

## Risk tier ladder

The risk tier is the load-bearing safety knob — gates which gaps are eligible for auto-fill on this run.
Gaps detected ABOVE the run's tier are logged as `[flagged: above-tier-N]` for the next higher-tier dispatch
or human review.

| Tier  | Fix-bar                                  | What you fill                                                                                                | What you flag                                              |
| ----- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------- |
| **1** | **Pure cosmetic / orphan removal.** Zero behaviour change. | Stale TODOs whose referenced issue/commit landed (`grep` + `gh issue view <NNNN>` returns closed → remove TODO). Dead helper-module re-exports for symbols that no longer exist (remove the re-export). Obviously orphaned env-var doc lines (env doc'd, not read anywhere, not in any chart values, not in any CR — remove the doc line, deferring to kira; OR add a single `os.environ.get` read so the doc becomes true. Pick whichever has lower blast radius.) | Anything that adds new logic, branches, or business rules. |
| **2** | **Sibling-pattern copy-paste.** Net change ≤10 lines, no new public surface. | A peer's identity surface is missing a file every other peer has (e.g. one peer doesn't have `self-tidy/SKILL.md` while every other peer does → copy the byte-identical file in). Helper-module export missing for a symbol referenced by a sibling. | Any test net-new (tier 3+). Any new endpoint (tier 5+).   |
| **3** | **Mirror an existing test's structure.** Add tests that copy a sibling's shape. | Add a happy-path test for an exported function whose sibling already has identical-shape tests. Add `defer resp.Body.Close()` on an HTTP call that's missing it. Add a missing `--help` example to a cobra command that mirrors a sibling command's `Long:` example. | Tests that need new fixtures or new mocking surfaces (tier 5+). |
| **4** | **Convention enforcement.** Walk an existing convention; fill the one place it's missing. | Add bearer-auth gate to a sibling endpoint that was missed (every other harness route has the gate; this one doesn't). Add the `defer fwd.Close()` to a port-forward call site that omitted it. | Any change to the convention itself.                      |
| **5** | **Function-level reasoning.** Implement a clearly-scoped TODO based on context. | Implement a TODO that says "validate `X != ''`" — fill in based on how the sibling validators work. Add input validation that mirrors a sibling function's pattern. Add the missing operator-CR-field-to-pod-env wiring when both sides exist but the bridge is absent. | Multi-step features (tier 7+). Architectural decisions (tier 9+). |
| **6** | **Cross-function within a file or package.** Fill a multi-call-site gap. | Implement a missing helper function whose call sites already exist (referenced but `// TODO: implement`). Wire a feature flag through 2-3 functions in the same file when the flag plumbing is in place but the conditional path is missing. | Anything touching more than ~3 files. Cross-package refactors (tier 7+). |
| **7** | **Multi-file, doc-promised, sibling-implemented.** | Add the missing `/api/sessions/<id>/stream` endpoint that `AGENTS.md` claims exists, when at least one sibling backend already implements it (copy-with-adapt across 2-4 files). Add a missing helm-chart values pathway that the operator already supports. | Anything that needs to invent a new API surface or change an existing one. |
| **8** | **Multi-file, doc-promised, no sibling.** Implement from spec. | Implement a feature claimed in `AGENTS.md` that no peer has yet built — but only when the spec is concrete enough (specific function signatures, env vars, behavior). Add a comprehensive test suite for an under-tested module. | Anything that requires a design decision (e.g. "should X be sync or async?"). |
| **9** | **Architectural / cross-cutting.** | Standardise an auth pattern across N harness routes. Implement a new MCP tool that's been promised. Refactor convention drift across the codebase. | Cross-team coordination work. Breaking changes. |
| **10** | **High-blast-radius autonomous additions.** | Net-new feature implementations in spec-driven areas. Major test infrastructure additions. | Anything that fundamentally changes the team's contracts or external-facing API. (At this tier, defer to feature-work agent or human ruling.) |

**Important:** at every tier, the five gates from CLAUDE.md still apply. The tier filters CANDIDATES; the
gates filter FILLS. A tier-7 candidate that fails the local-test gate still gets `[flagged: local-test-failed]`
and lands in memory rather than auto-merging.

## The intentional-design gauntlet (5 concerns specific to gaps)

Used in step 3 (validate). For each candidate, walk these. **When in doubt, drop the candidate** — false
positives that escape this step waste effort everywhere downstream.

1. **Doc explicitly aspirational?** Comments / readmes / changelog entries sometimes describe a future
   intent ("we plan to support X") rather than a current promise. Look for hedge language ("future",
   "planned", "TODO", "deferred to v2", "out of scope today"). If the doc itself flags the gap as
   intentional → drop the candidate.
2. **Adjacent code path covers the case?** A function with no nil-check might have a caller that already
   guarantees non-nil. A missing endpoint might be served by a sibling that handles the URL prefix. Read
   ±20 lines and the caller before declaring a gap.
3. **Sibling pattern available?** For tier-3+ fills, there must be a sibling implementation to copy from.
   No sibling = drop the candidate (or escalate to next-higher tier where new-design is allowed).
4. **Net-new content vs delta-fill?** A test that imports + checks-existence is delta-fill (small). A test
   that mocks a database is net-new (large). For tier-N, the fill must fit within tier-N's blast budget.
5. **Reversibility check.** If this fill turns out to be wrong, can it be cleanly reverted in one commit?
   Adding a function: yes. Refactoring 12 call sites: no — drop or escalate.

## Toolchain

Most gap detection is read+grep, not lint. The toolchain here is lighter than evan's bug-class analyzers:

| Tool / approach                  | Used for                                                                                              |
| -------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `git -C <checkout> grep`         | All ten code-side gap sources (TODO/FIXME/XXX, env-var refs, function refs, issue-ref scanning).      |
| `gh issue view <NNNN> --json state` | Cross-reference inline `#NNNN` refs against open/closed issue state.                                   |
| `find` + manual structure walk   | Sibling-pattern detection (e.g. "every peer has X file; finn doesn't").                                |
| `go list -test` + `grep -l`      | Untested-Go-symbol detection: enumerate exported symbols, grep for their use in `*_test.go`.          |
| `python -c "import ast; ..."`    | Untested-Python-public-symbol detection: AST-walk for `def` / `class` not prefixed `_`, grep tests.   |
| Manual spec-vs-code matching     | Operator↔helm parity: walk operator controllers, for each `Spec.Foo` mapped to pod state, verify a chart values path produces the same effect. Out of grep's reach — needs semantic reading. |
| `helm template`                  | Verify a chart's rendered output against operator behaviour for the same input.                        |

`helm` and `gh` are pre-installed in the backend image. `go list`, `python3 ast`, `git grep` are all there
too.

## Memory format

The deferred-findings file is `/workspaces/witwave-self/memory/agents/finn/project_finn_findings.md`. Each
run appends a date-stamped section. Same `[pending]` / `[fixed: <SHA>]` / `[flagged: <reason>]` marker schema
the rest of the team uses (consistent with the team-wide reconcile).

Run-section template:

```markdown
## YYYY-MM-DD HH:MM UTC — gap-work run (tier=N, sections=..., focus=...)

**Status: in-progress.** Pre-sweep SHA: `<sha>`.

### <gap source category>

- **<file>:<line>** `<gap class>` — <one-line description> [pending]
- ...

### <next category>

- ...
```

After step 6 finalises, mutate `**Status: in-progress.**` → `**Status: complete.**` and append a one-line
summary: `"M total candidates: F filled, G flagged, D dropped at gauntlet."`

The `MEMORY.md` index in your namespace must point at `project_finn_findings.md`. Create it on first run.

`[flagged: above-tier-N]` markers are the most common flag class — they're how a tier-1 run defers
tier-3+ candidates without losing them. The next higher-tier dispatch reads them and re-attempts.

## Process (8 steps)

Read these from CLAUDE.md before starting:

- **`<checkout>`** — local working-tree path
- **`<branch>`** — default branch (`main`)

### 0. Verify the source tree

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

If the working tree is missing or dirty: log to deferred-findings memory and stand down. Don't try to clone
or sync — that's iris's responsibility.

Pin git identity by invoking the `git-identity` skill (idempotent).

### 0.5. Recover stuck commits from a prior incomplete run

Same shape as evan's recovery step. If `git rev-list --count origin/main..HEAD > 0`, delegate a recovery
push to iris and wait for her report. Iris green → proceed. Iris red → batch-revert the stuck batch and
proceed. Iris push-failure → STOP.

After recovery, capture the post-recovery ref:

```sh
PRE_SWEEP_SHA=$(git -C <checkout> rev-parse HEAD)
```

### 1. Find — enumerate all eleven gap sources

For each section in the resolved input, walk every gap source per the categories below. Each source
produces one or more candidates. Concatenate all hits into the **candidate list**.

#### Gap-source categories (eleven, walked per section)

1. **Doc-vs-code promises.** Read `AGENTS.md`, root `CLAUDE.md`, root `README.md`, the section's own
   README, relevant `docs/*.md`, `CHANGELOG.md`. Extract concrete claims (file paths, command names, env
   vars, endpoints, function names). For each claim, verify by `git -C <checkout> grep` or path-existence
   check. Mismatch = candidate.
2. **Untested public/exported APIs.** Enumerate exported Go symbols (capitalised first letter) and public
   Python symbols (no leading underscore) in section files. For each, grep for invocations in
   `*_test.go` / `tests/**/*.py`. Zero invocations = candidate.
3. **TODO / FIXME / XXX / HACK markers.** `git -C <checkout> grep -n 'TODO\|FIXME\|XXX\|HACK' <section>`.
   Each match = candidate; tag with the marker class.
4. **Inline `#NNNN` issue refs.** `git -C <checkout> grep -nE '#[0-9]{2,4}' <section>`. For each, run
   `gh issue view <NNNN> --json state,title`. Closed issues whose code reference is now stale = candidate
   (remove the ref OR fill the unfulfilled work, depending on tier).
5. **Architectural gaps.** For sibling-pattern detection: enumerate `.agents/self/*/` directory shapes;
   any peer missing a file the others have = candidate. For backends: enumerate `backends/*/main.py`
   route tables; any backend missing a route the others have = candidate.
6. **Missing error handling.** Go: grep for `_ = somefunc(...)` where somefunc returns an `error`. Python:
   grep for `except: pass` and `except Exception:` blocks with no logger call. Each match = candidate.
7. **Convention drift.** For each established team convention (bearer-auth on harness routes,
   `defer x.Close()` on resource opens, `--help` examples on cobra commands), enumerate the convention's
   instances; any place that should follow it but doesn't = candidate.
8. **Configuration claims vs operator behavior.** Read `charts/witwave-operator/values.yaml` and operator
   controller code (`operator/internal/controller/*.go`). For each documented values key, verify the
   operator code reads it. Unread keys = candidate.
9. **Environment-variable claims.** Grep for env-var-shaped tokens in `AGENTS.md`, root `CLAUDE.md`,
   READMEs, `charts/*/values.yaml`. For each, `git -C <checkout> grep` for `os.environ.get('<NAME>')` or
   `os.Getenv("<NAME>")`. Unread = candidate.
10. **Helper modules with unfinished public surface.** For helper packages (e.g. `shared/`,
    `clients/ww/internal/`), grep for `// exposed for X` / `# exposed for` / "convenience for" comments;
    for each, verify the named symbol exists.
11. **Feature-parity drift between paired surfaces.**
    - **Operator ↔ Helm chart**: walk `operator/internal/controller/*.go`. For each
      `Spec.Foo`-driven pod-spec mutation (env, secret, mount, sidecar), verify the chart can produce the
      same pod-spec mutation from a `values.yaml` pathway. Operator-only behavior = candidate (chart-side
      gap).
    - **CLI ↔ Dashboard**: walk `clients/ww/cmd/*.go` for cobra commands; cross-reference against
      `clients/dashboard/src/views/*.vue` for parallel UX. CLI-only commands = candidate (dashboard-side
      gap, lower priority — see CLAUDE.md priority subsystems).

Each candidate carries: section, file, line, source category, claim/excerpt, and an estimated tier
(`tier-est=N`) based on the fix-bar table.

### 1.5. Persist the candidate list to memory IMMEDIATELY

Before any per-candidate work in steps 2-5, write the full raw candidate list to memory using the
run-section template above. Every candidate starts at `[pending]`. This is durability — if the LLM call
gets killed mid-run, the candidates survive in the file rather than being silently lost.

### 2. Filter by run tier

Walk the candidate list. For each candidate where `tier-est > run-tier`, mark it `[flagged: above-tier-N]`
where N is the estimated tier. Don't drop — leave it in memory for higher-tier dispatches.

### 3. Validate the remaining candidates (gauntlet)

For each candidate at-or-below the run tier, walk the 5-concern gauntlet from CLAUDE.md (doc-aspirational,
adjacent-handler, sibling-pattern, blast-budget, reversibility). On any drop, mark `[flagged: gauntlet:
<concern>]`. On pass, candidate moves to step 4.

### 4. Per-candidate fix-bar

For each candidate that survived the gauntlet, walk the per-tier fix-bar (the table above). Each rule that
fails marks the candidate `[flagged: fix-bar: <rule>]`.

### 5. Fill (one commit per gap)

For each candidate that survived gauntlet AND fix-bar, apply the fill. Run scoped tests
(`go test ./<section>/...`, `pytest tests/<section>/`, etc.) after the edit. If scoped tests fail:
`[flagged: local-test-failed: <test-name>]`, undo the edit, move to next candidate.

If scoped tests pass, commit per candidate with a focused message:

```
fix(gap, <category>): <one-line description>

Filled: <file>:<line> — <gap excerpt>
Source: <category, e.g. doc-vs-code-promise>
Tier: <N>
Fill rationale: <one paragraph — what was missing, what was filled, what verified>
```

Mark the candidate `[fixed: <SHA>]` in memory.

### 5.7. Mutate marker post-commit

After each successful commit, immediately update the candidate's marker in `project_finn_findings.md`
from `[pending]` to `[fixed: <SHA>]`. Per-candidate mutation, not end-of-run, so an interrupted run
leaves the file consistent.

### 6. Step 6 — finalise memory + summary

Mutate the run-section header from `Status: in-progress` to `Status: complete`. Append the one-line
summary (`M candidates: F filled, G flagged, D dropped at gauntlet`).

### 7. Delegate push + CI watch to iris

Same iris-delegation pattern evan uses. Send call-peer to iris with the commit range. Iris pushes,
watches CI, reports back.

- Iris reports green → run is done; return summary to caller (zora or user).
- Iris reports red on the FIRST commit → fix-forward semantics: read the failing-job log, attempt one
  in-place adjustment, iris-push again. If still red after one fix-forward attempt → batch-revert the
  whole gap-work batch via iris.
- Iris reports red on a LATER commit → identify the breaking commit; revert just that one, leave the
  others.

Never expand scope mid-run. If the gap-work batch hits red CI, the resolution is revert (this batch)
+ flag (re-attempt the offending fill at next dispatch).

## Out of scope for this skill

- **Bug fixes** — evan's lane.
- **Doc edits** — kira's lane. (You may add code that makes a doc claim true; you don't edit the doc.)
- **Net-new features without an existing claim** — feature-work's lane (future).
- **Tier-elevation decisions** — zora's lane. You execute at the tier she dispatches; you don't unilaterally
  promote a candidate from tier-3 to tier-5 within a run.

## When to invoke

- A2A from zora: cadence-mandated dispatches with `tier=<N>` per zora's polish-tier policy.
- A2A from another peer: rare; e.g. evan finds a missing-test-coverage finding during bug-work and dispatches
  finn to fill the test.
- Direct user invocation: "fill gaps", "do gap work tier 5", "find gaps in operator focus=operator-parity".

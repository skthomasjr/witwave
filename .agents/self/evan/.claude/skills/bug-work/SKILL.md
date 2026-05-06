---
name: bug-work
description:
  Single-pass **find AND fix** for correctness bugs in one or more sections of the witwave-ai/witwave repo. Runs
  analyzers (go vet / staticcheck SA / errcheck / ineffassign for Go; ruff B-class for Python; hadolint bug-class for
  Dockerfiles; shellcheck bug-class for shell; actionlint bug-class for workflows; controller-gen drift for operator),
  validates each candidate through an eight-concern intentional-design gauntlet at the configured depth, reasons
  about candidates as a set (common root causes, conflicts), decides fix-vs-flag per a strict fix-bar (depth,
  function-body containment, blast radius, test coverage, analyzer signal strength), **commits the safe fixes one bug
  at a time**, logs the rest to deferred-findings memory, delegates the push to iris via call-peer, watches CI, and
  reverts the entire batch if any workflow goes red. The verb is "work" — engaging with a problem, finding AND
  fixing AND triaging — to slot cleanly alongside future product-engineering siblings (`risk-work`, `gap-work`,
  `feature-work`). Distinct from nova's `code-cleanup` and kira's `docs-cleanup`, which are hygiene-class work
  (formatting drift, lint compliance) — bugs are not hygiene, they're product-engineering defects. Trigger when the
  user says "work bugs", "work the bugs", "fix bugs", "find and fix bugs", "do bug work", "find bugs", "scan for
  bugs", or "look for bugs", or specifies a section or depth (e.g. "fix bugs in operator depth 7").
version: 0.3.0
---

# bug-work

Single-pass **find AND fix** for correctness bugs. One run = one or more sections = one session. State lives in two
places only: **commits** (the bugs you fixed) and your `project_evan_findings.md` memory file (the bugs that needed
human judgement before fixing). No GitHub issues. No labels. No multi-session funnel.

The verb is "work" because that's what evan does: he engages with each candidate — investigates it, validates it
against intentional-design context, decides whether to fix it directly or surface it for human review, and either
way takes the next correct action. The naming is forward-compatible with future product-engineering siblings:
`risk-work`, `gap-work`, `feature-work` slot in alongside `bug-work` cleanly. (nova's `code-cleanup` and kira's
`docs-cleanup` use a different verb because they ARE hygiene work — tidying formatting drift and lint compliance is
genuinely "cleanup" in the literal sense; bugs aren't dirt.)

Discovery-only is NOT the pattern — that's the heavyweight local pipeline at
`<repo>/.claude/skills/bug-{discover,refine,approve,implement}` and is explicitly not the team's deployed-agent
shape. The pass IS supposed to fix what it can.

The full process design lives in `<repo>/.agents/self/evan/.claude/CLAUDE.md` under "The bug-work process (7 steps)"
and "Sections" / "Depth scale". This skill is the procedural walkthrough; the design rationale is in CLAUDE.md.

## Inputs

Parse from the user's prompt:

- **`depth`** — integer 1-10. Default `3` if unspecified. Refuse cleanly (return an error message) if outside 1-10.
- **`sections`** — comma-separated list of section names or aliases. Default `all-day-one` if unspecified. Aliases:
  `all-python`, `all-go`, `all-backends`, `all-tools`, `all-day-one`. Refuse cleanly if a section name doesn't match
  the 17-section list in CLAUDE.md, or if a v2-deferred section (`clients/dashboard`, `charts/witwave`,
  `charts/witwave-operator`) is requested before its toolchain has landed.

The user prompt forms can be free-form: `bug-work depth=5 sections=harness,shared`, or `find bugs in operator depth
7`, or just `find bugs`. Parse permissively; reject only if the values themselves are invalid.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path (Primary repository → Local checkout).
- **`<branch>`** — default branch.

### 0. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

If the working tree is missing or dirty: log to your deferred-findings memory and stand down for this run. Don't try
to clone or sync — that's iris's responsibility.

Pin your git identity by invoking the `git-identity` skill (idempotent).

### 0.5. Recover stuck commits from a prior incomplete run

If a previous bug-work run died mid-flight (timeout, pod crash, OOM kill) after Step 5 (commits) but before Step 7
(push), local commits will be sitting in the working tree, unpushed. The PVC kept them across the pod restart, but
they need to land on `main` before this run produces more.

```sh
STUCK_COMMITS=$(git -C <checkout> rev-list --count origin/main..HEAD)
```

If `STUCK_COMMITS == 0`: nothing to recover, proceed to step 1.

If `STUCK_COMMITS > 0`:

1. **Identify the stuck commits:**

   ```sh
   git -C <checkout> log origin/main..HEAD --oneline
   ```

2. **Delegate recovery push + CI watch to iris** via `call-peer`. Frame it as recovery, not fresh fixing — iris should
   know these commits are from a prior incomplete run, not from work she's seen before:

   > Hi iris — evan here. I'm starting a new bug-work run, but I found N stuck commits in my local checkout from a
   > prior run that didn't complete cleanly. Please run `git-push` to land them, then watch the CI workflows. Report
   > back the per-workflow conclusion. If anything goes red, I'll handle the batch-revert; don't take action.
   >
   > Stuck commits:
   >
   > - `<SHA1>` `<subject>`
   > - `<SHA2>` `<subject>`
   > - …

3. **Wait for iris's report.**

   - **All green** → recovery complete. Proceed to step 1; the new run starts from a clean main.
   - **Any red** → batch-revert the stuck commits (see step 7's batch-revert procedure), delegate the revert push to
     iris, log the failure to deferred-findings memory with a "recovered batch failed CI" note, then proceed to
     step 1. The reverted candidates re-surface naturally on a future run.
   - **iris reports push failure** (rebase conflict she can't resolve) → STOP. Don't proceed with a new scan; surface
     the situation in the run summary. The stuck commits stay stuck until the conflict is resolved manually.

4. **Either way, continue:** once recovery is resolved (pushed clean, reverted clean, or stopped on conflict), proceed
   to step 1. The recovery is not the new run — it's preflight.

This step is the self-healing guard against mid-run deaths. Without it, stuck commits would accumulate forever in the
local tree, eventually conflicting with new fixes or just rotting.

Capture the pre-sweep ref AFTER recovery so it reflects the post-recovery main:

```sh
PRE_SWEEP_SHA=$(git -C <checkout> rev-parse HEAD)
```

### 1. Scan

For each section in the resolved input, run the analyzers that match the file types in the section's tree. Bug-class
filters per analyzer:

| Tool          | Invocation (bug-class only)                                                                                        |
| ------------- | ------------------------------------------------------------------------------------------------------------------ |
| `ruff`        | `ruff check --select B --no-fix <section>` — bugbear only; never `--select E,W,F`                                  |
| `go vet`      | `cd <section> && go vet ./...`                                                                                     |
| `staticcheck` | `cd <section> && staticcheck -checks=SA* ./...` — only SA-prefix; skip ST/S/QF                                     |
| `errcheck`    | `cd <section> && errcheck ./...`                                                                                   |
| `ineffassign` | `cd <section> && ineffassign ./...`                                                                                |
| `hadolint`    | `hadolint --no-fail --ignore=DL3008 --ignore=DL3015 --ignore=DL3018 --ignore=DL3059 --ignore=DL4001 <Dockerfile>` |
| `shellcheck`  | `shellcheck --severity=warning --include=SC2086,SC2046,SC2155,SC2207,SC1090,SC2236,SC2068,SC2206,SC2128,SC2178 <script.sh>` |
| `actionlint`  | `actionlint <workflow.yml>` (actionlint is mostly correctness already)                                             |
| `controller-gen` (operator only) | `cd <checkout>/operator && make manifests && cd <checkout> && git diff --exit-code operator/config/crd/bases/` (uses the project's existing `manifests` target — `controller-gen rbac:roleName=manager-role crd webhook paths=./...` — so we stay in lockstep with however the operator regenerates CRDs in CI). The chart-side mirror at `charts/witwave-operator/crds/` is sync'd by a separate script + CI guard, not by this drift check. |

Concatenate all hits into the **candidate list**. Each candidate carries: section, file, line, rule, message, raw
analyzer output. Order doesn't matter at this stage.

If a section's toolchain isn't installed in this image (v2 sections requested before their tooling lands), skip with a
clear "section `<name>` requires toolchain not yet available" entry in the run summary. Don't improvise a partial
scan.

### 2. Validate per candidate (depth-gated)

For each candidate, walk the **intentional-design gauntlet** at the configured depth's intensity. The gauntlet is
eight concerns; depth controls **how hard you hunt** — what you READ per candidate, and how many of the eight
concerns you check. Higher depth = wider candidate pool (you find more bugs by reading more) AND tighter validation
(more concerns checked before declaring something a bug):

| Depth | What you read per candidate                              | Concerns checked                                                                                       |
| ----- | -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| 1-2   | Just the cited line                                      | None — trust the analyzer's signal directly. Bugs found at this depth are the no-brainer wins.         |
| 3-4   | ±20-line context window                                  | #1 (`#NNNN` ref) and #2 (adjacent handler) only                                                        |
| 5-6   | Full function body + immediate caller                    | #1, #2, #3 (synchronization), #4 (defensive checks earlier on call path)                               |
| 7-8   | Full source file                                         | All eight: #1 `#NNNN` refs, #2 adjacent handlers, #3 synchronization, #4 earlier defensive checks, #5 documented tradeoffs, #6 idempotent operations, #7 still-present-in-current-code, #8 stale line numbers |
| 9-10  | Full subsystem (file + callers + callees) + READMEs      | All eight + adversarial "what could go wrong" pass + web-search any unfamiliar APIs                    |

A candidate that survives the gauntlet at the configured depth proceeds to step 3, regardless of which depth the
caller picked. **Validation rigor scales with depth, but auto-fix doesn't depend on depth — that's the per-candidate
fix-bar in step 4.**

The eight concerns in detail (full text in CLAUDE.md → "The bug-work process" → "Step 2"):

1. Inline `#NNNN` reference within ±20 lines documents the choice as intentional → drop.
2. Adjacent existing handler within ±10 lines (`else`, `finally`, early-return guard, broader `except`) → drop.
3. Synchronization already in place (lock, single-threaded asyncio loop, GIL atomicity for ref rebinds and
   single-list-index assignments) → drop the race-condition candidate.
4. Defensive check earlier on the call path validates the input the candidate claims is unvalidated → drop.
5. Documented design tradeoff (a comment near the cited code explains the choice) → drop.
6. The "double X" the candidate flags is idempotent in the underlying API (`context.CancelFunc`, `set.discard`,
   k8s `client.Delete`, etc.) → drop.
7. Bug no longer present (code already fixed since the analyzer cached its output, or the analyzer's line reference
   doesn't match what's actually there) → drop.
8. Line reference stale from a refactor → re-locate and re-validate, or drop if not findable.

**When in doubt, drop the candidate.** False positives that escape this step waste effort across step 3, step 4, and
the human reading deferred-findings.

### 3. Reason about candidates as a set

Before deciding fix-vs-flag per candidate, look at the surviving set:

- **Common root cause** — group two or more findings that stem from the same underlying issue. They should fix
  together as one commit, not split.
- **Conflicts** — two candidates touching the same code in incompatible ways. Pick the better fix; drop the other.
- **Cascading risk** — fix for A changes the risk profile of B. Order so A's fix lowers B's risk; defer B if A's fix
  raises B's risk.
- **Ordering** — within the surviving set, order by safety: smallest blast radius first, fewest dependencies first.

### 4. Decide fix vs. flag (per candidate)

Apply the **fix-bar**. ALL must hold to fix; otherwise flag.

The fix-bar is **depth-independent.** Whether a fix is safe to land is a per-candidate question — analyzer signal,
function-body containment, blast radius, test coverage. It doesn't matter how hard you looked to find the candidate.
A high-confidence errcheck hit at depth 1 is just as fixable as one at depth 8.

1. **Function-body contained.** No public API change (Go exported symbols, Python public names). No type-signature
   change. No shared-state writes other callers depend on.
2. **Blast radius.** Read the function's callers and callees once. If the fix could plausibly break a caller (changing
   return-value semantics callers rely on), flag.
3. **Test coverage.** Tests exist for the affected file/path (`<file>_test.go`, `tests/test_<module>.py`,
   `<dir>/test_*.py`). **No tests covering the path → flag-only by default.**
4. **Analyzer signal strength.** High-signal: `errcheck`, `ineffassign`, `staticcheck SA1xxx-SA5xxx` (most), `ruff B*`
   (most), `actionlint` core rules, `hadolint DL3022/DL3025/DL4006`. Ambiguous: `staticcheck SA9xxx` (debug-only),
   anything where the analyzer message is "may", "likely", "potentially". Default: high-signal only auto-fixes.

Bin to **fix** or **flag**.

At depth 9-10, every committed fix also gets a regression test that fails on the pre-fix code and passes on the
fixed code (see step 5.7 below). That's a depth-driven *commit shape* requirement — not a fix-bar gate.

### 5. Fix each fixable candidate

For each candidate in the fix bin, processed in the order from step 3:

1. **Read the code in full.** Function body + immediate callers + immediate callees. At depth 9-10, full subsystem.
2. **Web-search the API if unfamiliar.** If the fix involves an API or framework behaviour you can't characterise
   from surrounding code, do a targeted web search before writing the fix. Confirm actual behaviour matches your
   assumption. If the search reveals the fix is more complex than the analyzer suggested, drop the candidate to
   flag-only with a "needs unfamiliar API confirmation" note.
3. **Write the fix.** Minimal scope. Use the analyzer's suggestion if obvious; otherwise apply the smallest
   well-grounded fix.
4. **Run scoped tests locally:**
   - Go: `cd <checkout>/<section> && go test ./...`
   - Python: `cd <checkout> && pytest <section>/`
   - If tests fail → REVERT the working-tree change (`git -C <checkout> checkout -- <file>`), move the candidate to
     flag-only with a "fix broke local tests: <test name>" note, continue to next candidate.
5. **Verify the bug condition is gone.** Re-read the changed code. The analyzer rule that originally flagged would no
   longer fire. The fix is complete (no half-measures, no `TODO`). No adjacent regressions in the surrounding 20 lines.
6. **Commit (one bug per commit):**

   ```sh
   git -C <checkout> add <file>
   git -C <checkout> commit -m "fix(<section>): <one-line bug description>

   <2-4 lines: what was wrong, why it's wrong, what the fix does. Reference
   the analyzer rule (e.g. \"errcheck flagged at <file>:<line>: error from
   <call> not handled\"). Reference the test name that exercises the path.>
   "
   ```

   No unrelated changes in the same commit.

7. **At depth 9-10**, also write a regression test that fails on the pre-fix code and passes on the fixed code, in the
   same commit. Skip this beat at depth 1-8.

### 6. Log flag-only findings

Ensure your memory namespace exists (idempotent):

```sh
mkdir -p /workspaces/witwave-self/memory/agents/evan
```

Then append to `/workspaces/witwave-self/memory/agents/evan/project_evan_findings.md`. Group by sweep run (newest
first); within a run, group by section; within a section, order by severity:

1. Data loss / corruption
2. Crashes (null deref, panic, unrecoverable error path)
3. Logic errors producing wrong output
4. Resource leaks (file handles, goroutines, contexts)
5. Edge cases / latent

Format:

```markdown
## YYYY-MM-DD HH:MM UTC — bug-work run (depth=N, sections=...)

### <section name>

- **<file>:<line>** `<analyzer rule>` — <one-line summary of what>
  - Why: <one-line summary of why it's a bug>
  - Suggested fix: <one-line summary of approach>
  - Why flagged not fixed: <one of: function-body not contained, blast radius unclear, no test coverage, ambiguous analyzer rule, fix broke local tests "<test name>", fix needs unfamiliar API confirmation>
```

If `project_evan_findings.md` doesn't exist yet, create it with a header explaining what it contains. Update the
`MEMORY.md` index in your namespace to point to it.

### 7. Push + watch CI (via iris)

If `PRE_SWEEP_SHA` equals current `HEAD` (no commits produced), skip this entire step. Report "no fixes committed
this run; M findings logged to memory" and exit cleanly.

Otherwise: this step is **fully delegated to iris** via `call-peer`. **Iris owns all git and GitHub authority for the
team** — push posture (race handling, conflict surfacing, no-force rules), and `gh`-API operations including the CI
watch. Other agents (kira, nova, evan, future siblings) commit locally and delegate publishing through iris, full
stop. The pattern is parallel to kira-commits / iris-pushes and nova-commits / iris-pushes; for evan, the delegation
extends one step further to include the CI watch because the trunk-based-dev contract ("if you break main, fix or
revert immediately") couples the watch to the push as a single workflow.

This is the right architecture regardless of credentials: keeping iris as the single GitHub-API gateway reduces the
team's credential blast radius (only iris needs a working PAT), keeps each agent focused on its domain (evan does
correctness, not gh-CLI plumbing), and scales cleanly when future agents join the team.

1. **Delegate push + CI watch to iris** via `call-peer`. Send a self-contained prompt of the form:

   > Hi iris — evan here. I just landed N bug-fix commits in the local checkout. Please:
   >
   > 1. Run `git-push` to publish them.
   > 2. Watch the CI workflows that trigger on this push. Report each workflow's conclusion (green / red) and its
   >    `gh` run URL.
   > 3. **If any workflow goes red**, do NOT take action — report the failure back to me and I'll handle the
   >    batch-revert. (Trunk-based-dev contract: I committed; I'm responsible for reverting if CI fails.)
   >
   > Commits:
   >
   > - `<SHA1>` `fix(<section>): <subject>`
   > - `<SHA2>` `fix(<section>): <subject>`
   > - …
   >
   > Pre-sweep SHA was `<PRE_SWEEP_SHA>`.

   Wait for her reply. Capture the push outcome and the per-workflow CI outcomes.

2. **If iris reports push failure** (rebase conflict she couldn't resolve, etc.): STOP. Don't improvise. Surface the
   situation in the run summary. The next bug-work run will re-attempt the delegation naturally.

3. **If iris reports push success and all CI workflows green**: done. Capture per-workflow conclusion + duration in
   the run summary.

4. **If iris reports any CI workflow went red**: batch-revert (next step). Iris reports; evan acts on the report.

   ```sh
   git -C <checkout> revert --no-commit ${PRE_SWEEP_SHA}..HEAD
   git -C <checkout> commit -m "Revert evan bug-work batch (CI red on <workflow-name>)

   Auto-revert: one or more bug-work commits broke <workflow-name>. Per
   trunk-based dev contract (\"if you break main, fix or revert immediately\")
   the entire batch reverts at once. The candidates re-surface on the next
   sweep run with the test failure noted in deferred-findings.

   Failing run: <gh-run-url>
   "
   ```

   Delegate the revert push to iris via `call-peer` (same flow). Append a "batch reverted" entry to the deferred-
   findings memory listing each reverted commit's bug + the failing workflow URL, so the candidates re-evaluate next
   run with the test failure as context.

   If iris's revert push also fails, surface the situation and stop. Don't loop.

### 8. Report

Return a structured summary to the caller:

- Pre / post SHAs
- Sections scanned, depth used
- Per-section: candidates considered, candidates dropped at gauntlet, candidates fixed, candidates flagged
- Total commits produced (or 0)
- Iris's push outcome
- CI watch outcome (per workflow)
- Pointer to `project_evan_findings.md` for flag-only details
- If batch-reverted, the failing workflow URL + the revert commit SHA

## Out of scope for this skill

- **GitHub issues.** No filing, no labels, no comments. State lives in commits + memory only.
- **Multi-session funnel.** No "approved" / "pending" / "in-progress" intermediate states living across runs. Every
  run is a fresh evaluation.
- **Surgical CI revert.** v1 is batch-revert only. Bisect-style "which specific commit broke CI" is a v2 polish.
- **Helm chart bugs / TS-Vue dashboard bugs.** Sections deferred to v2 until the toolchain lands.
- **Style / complexity / dead code / type drift / security CVEs / feature gaps.** Lens is correctness only — logic
  defects.
- **Pattern invention.** If a fix requires a pattern that doesn't already exist somewhere in the file or package, flag
  instead. Pattern invention without architecture context is exactly the false-positive failure mode.

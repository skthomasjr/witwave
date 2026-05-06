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

### 1.5. Persist the candidate list to memory IMMEDIATELY

Before doing any per-candidate work in steps 2-5, dump the full raw candidate list to your deferred-findings memory
file. This is a durability guard against a failure mode observed 2026-05-06 evening: a wide low-depth pass found ~37
candidates, then the LLM session ended mid-loop (likely context exhaustion from per-candidate fix-bar reads) before
any commits or memory writes happened. **Findings were lost.** This step makes the candidate list survive that
failure mode by writing it to disk before any per-candidate work begins.

Ensure the memory directory exists:

```sh
mkdir -p /workspaces/witwave-self/memory/agents/evan
```

Append a new "in-progress" run section to
`/workspaces/witwave-self/memory/agents/evan/project_evan_findings.md`:

```markdown
## YYYY-MM-DD HH:MM UTC — bug-work run STARTED (depth=N, sections=...)

**Status: in-progress.** Pre-sweep SHA: `<sha>`. Will be finalised at step 6.

### Raw candidates discovered (M total)

#### <section name>

- **<file>:<line>** `<analyzer rule>` — <one-line analyzer message>  [pending]
- **<file>:<line>** `<analyzer rule>` — <one-line analyzer message>  [pending]
- ...

#### <next section name>

- ...
```

Each candidate gets a `[pending]` marker. As the run progresses, step 5 will mutate `[pending]` → `[fixed: <SHA>]` for
committed fixes, and step 6 will mutate `[pending]` → `[flagged: <reason>]` for the rest. If the run dies between
steps 1.5 and 6, the leftover `[pending]` markers tell the next run (or a human reader) exactly which candidates
weren't processed.

This step is mandatory regardless of candidate count. Even an empty sweep (zero candidates) writes a one-line
"empty sweep" entry to memory so the run is auditable from the memory file alone.

**Cost:** writing the candidate list is O(M) file IO and a few hundred to a few thousand bytes. The cost of NOT
writing is losing every finding when a session dies — observed once, will happen again at higher candidate volumes.

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
   - If tests pass → continue to step 5 (verify) below.
   - If tests fail → **fix-forward, ONCE.** Read the failure output (failing test name, assertion, traceback).
     Adjust the fix in-place (correct the code change; do NOT yet revert). Re-run the same scoped tests.
     - **Pass on retry → continue to step 5.** That adjusted code becomes the commit.
     - **Still fails on retry → revert.** `git -C <checkout> checkout -- <file>`. Move candidate to flag-only with
       reason `fix-forward-failed: <one-line summary of why the second attempt didn't work>`. Move on to the next
       candidate.

   The bound is **exactly one fix-forward attempt per candidate.** Catches the common case (small mistake, easy to
   adjust) without spiralling. A failing second attempt is the signal that something deeper is going on; revert and
   surface for human review rather than chasing it autonomously.
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

7. **Update the candidate's marker in memory.** Mutate `[pending]` → `[fixed: <SHA>]` in
   `project_evan_findings.md` for this candidate. This makes partial progress durable: if the run dies between
   candidates, the next run / human reader knows exactly which candidates landed and which still need work. **Do
   this immediately after the commit, not at end of run** — the whole point is per-candidate durability.

8. **At depth 9-10**, also write a regression test that fails on the pre-fix code and passes on the fixed code, in the
   same commit. Skip this beat at depth 1-8.

### 6. Finalise flag-only findings in memory

By this point the in-progress run section in
`/workspaces/witwave-self/memory/agents/evan/project_evan_findings.md` (created in step 1.5) has every candidate
marked `[fixed: <SHA>]`, `[pending]`, or `[flagged]`. Step 5.7 already mutates each fixed candidate's marker.
This step finalises the rest:

1. Walk every `[pending]` marker that survived step 5 (because step 4 binned the candidate to flag, or step 5 had
   to drop the candidate due to test failure / unfamiliar-API confirmation needed).
2. Mutate `[pending]` → `[flagged: <reason>]` with one of the reasons enumerated below, plus a short note
   describing the suggested fix and why-it's-a-bug.
3. Mutate the run section's `**Status: in-progress.**` header to `**Status: complete.**`. Add a one-line summary:
   "M total candidates: F fixed, G flagged, D dropped at gauntlet."

Group flagged candidates within each section by severity:

1. Data loss / corruption
2. Crashes (null deref, panic, unrecoverable error path)
3. Logic errors producing wrong output
4. Resource leaks (file handles, goroutines, contexts)
5. Edge cases / latent

The finalised candidate entry under each section reads:

```markdown
- **<file>:<line>** `<analyzer rule>` — <one-line summary of what>  [flagged: <reason>]
  - Why: <one-line summary of why it's a bug>
  - Suggested fix: <one-line summary of approach>
```

Where `<reason>` is one of: `function-body-not-contained`, `blast-radius-unclear`, `no-test-coverage`,
`ambiguous-analyzer-rule`, `fix-broke-local-tests "<test name>"`, `fix-needs-unfamiliar-api-confirmation`,
`gauntlet-dropped` (for candidates that didn't survive step 2's intentional-design gauntlet at depth ≥3).

The `MEMORY.md` index in your namespace must point at `project_evan_findings.md`. Create it (and the index entry)
on first run — see step 1.5's mkdir-p; same scaffolding applies here.

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
   > 3. **If any workflow goes red**, also fetch the failing job's log via `gh run view <run-id> --log-failed` and
   >    include the relevant excerpt in your report. Don't take remediation action yourself — I'll decide whether
   >    to fix-forward or batch-revert based on the failure.
   >
   > Commits:
   >
   > - `<SHA1>` `fix(<section>): <subject>`
   > - `<SHA2>` `fix(<section>): <subject>`
   > - …
   >
   > Pre-sweep SHA was `<PRE_SWEEP_SHA>`.

   Wait for her reply. Capture the push outcome, per-workflow CI outcomes, and (for any red workflow) the failing
   job's log excerpt.

2. **If iris reports push failure** (rebase conflict she couldn't resolve, etc.): STOP. Don't improvise. Surface the
   situation in the run summary. The next bug-work run will re-attempt the delegation naturally.

3. **If iris reports push success and all CI workflows green**: done. Capture per-workflow conclusion + duration in
   the run summary.

4. **If iris reports any CI workflow went red**: **fix-forward, ONCE.** Don't reflexively batch-revert. The
   trunk-based-dev contract is "if you break main, fix or revert immediately" — fix is the preferred move; revert
   is the fallback when fix doesn't work. Procedure:

   1. **Read the failure log excerpt** iris included in her report. Identify what broke. Common shapes:

      - Test failure (assertion, traceback) — likely caused by one of evan's commits
      - Lint / format failure — likely caused by one of evan's commits OR a pre-existing-on-main issue
        surfaced by the path filter triggering
      - Build / compile failure — likely caused by one of evan's commits, possibly a pre-existing
        toolchain regression
      - Drift / sync check failure (e.g., embedded chart drift) — almost always pre-existing on main,
        not caused by evan

   2. **Decide if the failure is in scope for evan to fix-forward.** Yes if it's a small, targeted change
      that's clearly remediable from the failure log alone. No if the fix would require redesigning the original
      bug fix or reading large swaths of unrelated code.

   3. **If in scope: write the fix-forward commit.** Apply the fix (could be: re-running a sync script, adjusting
      a quoted variable, reverting one specific commit while keeping others). Run scoped local tests on the
      fix-forward to make sure IT doesn't break tests too.

      - Local tests pass → ask iris to push the fix-forward commit + re-watch CI on the new state.
        - All CI green → DONE. Original batch + fix-forward all stay landed. Update memory: log the
          fix-forward as `[ci-fix-forward: <commit-SHA>]` under the run section.
        - CI still red → fall back to batch-revert (next branch). Don't recurse on fix-forward.
      - Local tests fail → fall back to batch-revert.

   4. **If out of scope OR fix-forward attempt failed: batch-revert.**

      ```sh
      git -C <checkout> revert --no-commit ${PRE_SWEEP_SHA}..HEAD
      git -C <checkout> commit -m "Revert evan bug-work batch (CI red on <workflow-name>)

      Fix-forward attempted: <one-line summary or 'not in scope: <reason>'>.
      Batch-revert per trunk-based-dev fallback. The candidates re-surface
      on the next sweep run with the test failure noted in deferred-findings.

      Failing run: <gh-run-url>
      "
      ```

   The bound: **exactly one fix-forward attempt per CI failure event.** Catches the common case (small targeted
   adjustment unblocks main without losing the batch). Prevents the spiral case (a failed fix-forward followed by
   another failed fix-forward followed by ...). When in doubt, revert and let the next run re-discover the
   candidates.

   If batch-reverting: delegate the revert push to iris via `call-peer` (same flow). Append a "batch reverted" entry
   to deferred-findings memory listing each reverted commit's bug + the failing workflow URL, so the candidates
   re-evaluate next run with the test failure as context.

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

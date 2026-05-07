---
name: bug-work
description:
  Find and fix correctness bugs (logic defects only) across one or more sections at a caller-specified depth (1-10).
  Single end-to-end pass per run: scan → persist candidate list → validate → reason as set → fix-or-flag → commit per
  bug → delegate push + CI watch to iris → fix-forward on test/CI failure (revert is the bounded fallback). State
  lives in commits and `project_evan_findings.md` memory only — no GitHub issues, no labels, no multi-session funnel.
  Trigger when the user says "work bugs", "fix bugs", "find and fix bugs", "do bug work", "find bugs", "scan for
  bugs", or specifies depth/sections (e.g. "fix bugs in operator depth 7").
version: 0.4.0
---

# bug-work

Single-pass **find AND fix** for correctness bugs in the witwave-ai/witwave repo.

State lives in two places only: **commits** (the bugs you fixed) and your `project_evan_findings.md` memory file (the
bugs that needed human judgement before fixing). No GitHub issues, no labels, no multi-session funnel. The pass IS
supposed to fix what it can — discovery-only is not the team-deployed pattern.

## Inputs

Parse from the user's prompt:

- **`depth`** — integer 1-10. Default `3` if unspecified. Refuse cleanly if outside 1-10.
- **`sections`** — comma-separated list of section names or aliases. Default `all-day-one` if unspecified. Refuse
  cleanly if a section name doesn't match the list below or if a v2-deferred section is requested.

Parse permissively. `bug-work depth=5 sections=harness,shared`, `find bugs in operator depth 7`, `fix bugs`, and
plain `work bugs` are all valid.

## Sections

The repo is partitioned into 17 addressable units of code. Day-one toolchain (14 sections) covers Python, Go,
Dockerfile, shell, and GitHub Actions. Three more are scaffolded but their toolchain hasn't shipped yet.

### Day-one toolchain

| Section                  | Files in tree                          | Toolchain                                                                |
| ------------------------ | -------------------------------------- | ------------------------------------------------------------------------ |
| `harness`                | Python + Dockerfile                    | `ruff` (B) + `hadolint` (bug-class)                                      |
| `shared`                 | Python                                 | `ruff` (B)                                                               |
| `backends/claude`        | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `backends/codex`         | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `backends/gemini`        | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `backends/echo`          | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `tools/kubernetes`       | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `tools/helm`             | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `tools/prometheus`       | Python + Dockerfile                    | `ruff` (B) + `hadolint`                                                  |
| `operator`               | Go + Dockerfile + kubebuilder markers  | `go vet` + `staticcheck` (SA) + `errcheck` + `ineffassign` + `hadolint` + `controller-gen` drift |
| `clients/ww`             | Go (+ Dockerfile if present)           | `go vet` + `staticcheck` (SA) + `errcheck` + `ineffassign` + `hadolint`  |
| `helpers/git-sync`       | Dockerfile only                        | `hadolint`                                                               |
| `scripts`                | Shell                                  | `shellcheck` (bug-class)                                                 |
| `workflows`              | GitHub Actions YAML                    | `actionlint`                                                             |

### Deferred to v2

`clients/dashboard` (TS/Vue + Dockerfile), `charts/witwave`, `charts/witwave-operator`. If the caller specifies one,
refuse cleanly with `"section <name> requires toolchain not yet installed in this image"` and log the request.

### Aliases

- `all-python` → `harness`, `shared`, all four backends, all three tools (9 sections)
- `all-go` → `operator`, `clients/ww`
- `all-backends` → all four backends
- `all-tools` → all three tools
- `all-day-one` → every section in the day-one table above (14)

Aliases compose with explicit sections: `all-go,scripts` is valid.

### Out of scope (no section)

- **Markdown** — kira's domain.
- **TOML / JSON** — parse errors only; nothing bug-class.
- **Lockfiles / `requirements.txt` / `go.mod`** — needs an external tracker we don't have.
- **Generated / vendored** — `**/zz_generated.*`, `**/vendor/**`, `clients/ww/dist/**`,
  `clients/ww/internal/operator/embedded/**`, `clients/dashboard/dist/**`, controller-gen output. Touching these
  creates per-pass revert cycles.
- **Test code** — `tests/`, `**/*_test.go`, `**/test_*.py`. Tests verify your fixes; you don't add bugs to them.

## Depth scale

Depth = **how hard you hunt for bugs.** Higher depth = more LLM time per candidate, deeper analysis, wider net that
catches subtler bugs analyzers don't surface alone. **Every depth fixes** — the fix-bar is depth-independent.

| Depth   | What you read per candidate                                                            | Concerns checked from the gauntlet                       | Candidate pool                                                              |
| ------- | -------------------------------------------------------------------------------------- | -------------------------------------------------------- | --------------------------------------------------------------------------- |
| **1-2** | Just the cited line                                                                    | None — trust the analyzer                                | Bare analyzer hits — the no-brainer wins                                    |
| **3-4** | ±20-line context window                                                                | #1 (`#NNNN` ref), #2 (adjacent handler)                  | Drops obvious false positives                                               |
| **5-6** | Full function body + immediate caller                                                  | #1, #2, #3 (synchronization), #4 (defensive earlier)     | Adds candidates analyzers don't see — logic bugs spotted by reading intent  |
| **7-8** | Full source file                                                                       | All 8                                                    | Cross-function patterns the per-line analyzers miss                         |
| **9-10**| Full subsystem (file + callers + callees) + READMEs/AGENTS.md + adversarial pass       | All 8 + adversarial + web-search any unfamiliar APIs     | Subtle architectural / cross-file / cross-package bugs                      |

**Polish trajectory.** Run depth 1-2 wide first (cheap, catches mechanical wins everywhere), then depth 5-6 on the
same scope (function-level reasoning catches the next tier), then 7-8 (cross-function patterns), then 9-10
(architectural). Each tier's candidate pool shrinks because the previous tier exhausted the cheap finds.

**Defaults** (caller can override):

- First-touch wide pass: depth 1-2.
- Routine on-demand: depth 3-4.
- After 1-3 has been run: depth 5-6.
- Pre-release sweep: depth 7-8.
- Critical / post-incident: depth 9-10.

## The intentional-design gauntlet (8 concerns)

Used in step 2 (validate). For each candidate, walk the concerns at the depth-table's intensity. **When in doubt,
drop the candidate** — false positives that escape this step waste effort everywhere downstream.

1. **Inline `#NNNN` reference within ±20 lines.** GitHub-issue refs in the witwave codebase document intentional
   choices. A nearby `#NNNN` usually means the candidate is a misread.
2. **Adjacent handler within ±10 lines.** `else` branch, `finally` block, early-return guard, broader `except` two
   lines below — these are the patterns most often missed.
3. **Synchronization in place.** Lock (`async with _lock`, `threading.Lock`), single-threaded asyncio loop, GIL
   atomicity for ref rebinds and single-list-index assignments. If only one path writes, it isn't a race.
4. **Defensive check earlier on the call path.** For "missing nil-check" / "missing validation" candidates, read what
   calls the function. If the caller validates, the internal gap is fine.
5. **Documented design tradeoff.** A comment near the cited code explains the choice (e.g. CLI falling back to
   anonymous when credentials aren't found, watch handler returning empty on transient errors). Drop the candidate.
6. **Idempotent operations.** "Double cancel/delete/cleanup" is usually safe (`context.CancelFunc`, `set.discard`,
   k8s `client.Delete`). Drop the candidate.
7. **Bug still present in current code.** Operate on `HEAD`. If the analyzer's line reference doesn't match what's
   actually there, drop.
8. **Stale line numbers.** Re-locate if findable; drop if not.

## The fix-bar (4 rules, depth-independent)

Used in step 4 (decide fix vs. flag). All four must hold to fix; otherwise the candidate goes to flag bin.

1. **Function-body contained.** No public API change, no type-signature change, no shared-state writes other callers
   depend on.
2. **Blast radius.** Read callers and callees once. If the fix could plausibly break a caller (changing return-value
   semantics callers rely on), flag.
3. **Test coverage.** Tests exist for the affected file/path (`<file>_test.go`, `tests/test_<module>.py`,
   `<dir>/test_*.py`). **No tests covering the path → flag.** Exception: candidates matching the **safe-pattern
   catalogue** below — those bypass the test-coverage gate because the fix is canonical enough that lack of tests
   doesn't make it unsafe.
4. **Analyzer signal strength.** High-signal: `errcheck`, `ineffassign`, `staticcheck SA1xxx-SA5xxx`, `ruff B*`,
   `actionlint` core, `hadolint DL3022/DL3025/DL4006`. Ambiguous: `staticcheck SA9xxx`, anything with hedge words
   ("may", "likely", "potentially"). Default: high-signal only auto-fixes; ambiguous flags.

The fix-bar is depth-independent — a high-confidence errcheck hit at depth 1 is just as fixable as one at depth 8.
At depth 9-10, every committed fix also gets a regression test (see step 5.7); that's a commit-shape requirement,
not a fix-bar gate.

## Safe-pattern catalogue (waives the test-coverage gate)

A curated set of (analyzer rule + fix template) pairs where the canonical fix literally cannot change runtime
behavior — only traceback presentation, build hardening posture, or cosmetic noise. Candidates matching ALL of:
analyzer rule, surrounding-context shape, AND fix template — bypass the test-coverage gate in fix-bar rule 3.
Untested-path findings that match the catalogue are auto-fixable; non-matching findings still flag.

The catalogue is **deliberately narrow.** New entries require a clear safety justification: the fix must
demonstrably not change observable behavior of the code, only its diagnostics / hardening / presentation.
Pattern invention beyond these entries is still out of scope (see "Out of scope" → "Pattern invention").

| ID | Analyzer rule + context shape | Fix template | Why it bypasses test-coverage |
|----|------------------------------|--------------|-------------------------------|
| **SP-1** | `ruff B904` raise-without-from in `except X: raise Y(...)`. Y is an established exception type already raised elsewhere in the codebase; the except branch's intent is "translate exception type" not "augment with context." | `except X as exc:` + `raise Y(...) from None` (suppresses chain noise). Use `from exc` instead if a comment in the surrounding code says context is wanted. | Behavior-preserving from caller perspective — only changes traceback presentation. Callers see the same Y exception either way; can't break tests. |
| **SP-2** | `hadolint DL4006` missing pipefail on `RUN ... \| ...`. The Dockerfile has no existing `SHELL` directive. | Insert `SHELL ["/bin/bash", "-o", "pipefail", "-c"]` once at top of file, after the FROM/ENV blocks and before the first RUN. Single insertion covers every DL4006 in the file. | Hardening — surfaces silent pipe failures that were previously swallowed. The "global blast radius" is the point; if any subsequent RUN was depending on pipe-failure silence, that's also a bug. Image still builds; test-coverage gap doesn't apply because the change can only make existing-silent-bugs loud. |
| **SP-3** | `actionlint SC2035` `<cmd> *.glob` in a workflow `run:` block where the cwd contains files with controlled prefixes (e.g. `goreleaser` output dir, version-named archives). | Insert `--` separator: `<cmd> -- *.glob`. **Never** use `./` prefix (would change output bytes). | `--` ends option parsing; `sha256sum` and similar commands produce byte-identical output. Critical for SLSA-subject preservation; the `--` form is the canonical "I know what I'm doing" idiom. |
| **SP-4** | `actionlint SC2034` unused `<var>` in `for <var> in $(seq 1 N); do ... done` (count-controlled retry idiom). | Rename the loop variable to `_<var>` (e.g. `_i`). | Underscore prefix is the standard "unused by design" signal — same as Python and Go. Pure variable rename in an unused position; provably can't affect runtime behavior. |
| **SP-5** | `actionlint SC2016` single-quoted `$<expr>` where `<expr>` is a known false-positive class — bcrypt header (`$2y$`, `$2a$`, `$2b$`, `$05$`/`$10$`/`$12$` cost prefix), regex anchors, version strings (`$RELEASE_VERSION` in templating), Helm template expressions. | Add `# shellcheck disable=SC2016` comment immediately above the affected line, with a one-word reason in a trailing comment (e.g. `# shellcheck disable=SC2016  # bcrypt literal`). | Single-quoting is correct for these contexts; double-quoting would corrupt the literal. The fix is the disable comment, not the code change. Zero runtime effect. |

When a candidate matches a catalogue entry, evan applies the canonical fix in step 5 with the same per-candidate
process (read body + run scoped tests + commit + mutate marker). The local-test gate still runs — if scoped tests
exist on adjacent code paths, they still execute and a failure still triggers fix-forward then revert. The
catalogue waives the *no-test-coverage-on-this-specific-line* trigger, not the local-test execution itself.

Out-of-catalogue judgment-call findings continue to flag with their existing reasons. The user can review
deferred-findings memory and either:
- Add a new pattern to the catalogue (manual edit to this SKILL.md, then evan picks it up via gitSync), or
- Manually instruct evan to fix specific candidates via "fix-from-queue" trigger, supplying the fix template
  inline. The user's review is the human-in-the-loop validation that the depth-bar (and test-coverage gate) was
  supposed to provide.

## Toolchain invocations (bug-class filters)

| Tool          | Invocation                                                                                                          |
| ------------- | ------------------------------------------------------------------------------------------------------------------- |
| `ruff`        | `ruff check --select B --no-fix <section>` — bugbear only; never `--select E,W,F`                                   |
| `go vet`      | `cd <section> && go vet ./...`                                                                                      |
| `staticcheck` | `cd <section> && staticcheck -checks=SA* ./...` — only SA-prefix; skip ST/S/QF                                      |
| `errcheck`    | `cd <section> && errcheck ./...`                                                                                    |
| `ineffassign` | `cd <section> && ineffassign ./...`                                                                                 |
| `hadolint`    | `hadolint --no-fail --ignore=DL3008 --ignore=DL3015 --ignore=DL3018 --ignore=DL3059 --ignore=DL4001 <Dockerfile>`   |
| `shellcheck`  | `shellcheck --severity=warning --include=SC2086,SC2046,SC2155,SC2207,SC1090,SC2236,SC2068,SC2206,SC2128,SC2178 <script.sh>` |
| `actionlint`  | `actionlint <workflow.yml>`                                                                                         |
| `controller-gen` (operator only) | `cd <checkout>/operator && make manifests && cd <checkout> && git diff --exit-code operator/config/crd/bases/` — uses the project's existing `manifests` target so the drift check stays in lockstep with how CI regenerates CRDs |

## Memory format

The deferred-findings file is `/workspaces/witwave-self/memory/agents/evan/project_evan_findings.md`. Each run
appends a date-stamped section. Each candidate carries one of three status markers:

- `[pending]` — written by step 1.5 immediately after scan. Default state.
- `[fixed: <SHA>]` — written by step 5.7 right after the commit lands. Per-candidate, not at end of run.
- `[flagged: <reason>]` — written by step 6 for candidates that failed the fix-bar or the local-test gate.

Reasons in `[flagged: <reason>]`: `function-body-not-contained`, `blast-radius-unclear`, `no-test-coverage`,
`ambiguous-analyzer-rule`, `fix-broke-local-tests "<test name>"`, `fix-needs-unfamiliar-api-confirmation`,
`gauntlet-dropped`, `fix-forward-failed: <one-line>`.

Run-section template:

```markdown
## YYYY-MM-DD HH:MM UTC — bug-work run (depth=N, sections=...)

**Status: in-progress.** Pre-sweep SHA: `<sha>`.

### <section name>

- **<file>:<line>** `<analyzer rule>` — <one-line analyzer message>  [pending]
- ...
```

After step 6 finalises, mutate `**Status: in-progress.**` → `**Status: complete.**` and append a one-line summary:
`"M total candidates: F fixed, G flagged, D dropped at gauntlet."`

The `MEMORY.md` index in your namespace must point at `project_evan_findings.md`. Create it on first run.

## Process (8 steps)

Read these from CLAUDE.md before starting:

- **`<checkout>`** — local working-tree path
- **`<branch>`** — default branch (`main`)

### 0. Verify the source tree

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

If the working tree is missing or dirty: log to deferred-findings memory and stand down. Don't try to clone or sync —
that's iris's responsibility.

Pin git identity by invoking the `git-identity` skill (idempotent).

### 0.5. Recover stuck commits from a prior incomplete run

```sh
STUCK=$(git -C <checkout> rev-list --count origin/main..HEAD)
```

If `STUCK == 0`: proceed to step 1.

If `STUCK > 0`: previous run died after step 5 (commits) but before step 7 (push). Delegate a recovery push + CI
watch to iris (use the same call-peer template as step 7, framed as recovery). Wait for her report.

- **Iris reports green** → recovery complete, proceed to step 1.
- **Iris reports red** → batch-revert the stuck batch (same procedure as step 7), log the failure, proceed to step 1.
- **Iris reports push failure (unresolvable conflict)** → STOP. Don't scan on top of an unstable tree.

After recovery resolves, capture the post-recovery ref:

```sh
PRE_SWEEP_SHA=$(git -C <checkout> rev-parse HEAD)
```

### 1. Scan

For each section in the resolved input, run the analyzers from the toolchain table above on the file types that
section contains. If a section's toolchain isn't installed in this image (v2 sections requested early), skip with a
clear summary entry.

Concatenate all hits into the **candidate list**. Each candidate carries: section, file, line, rule, message, raw
analyzer output.

### 1.5. Persist the candidate list to memory IMMEDIATELY

Before any per-candidate work in steps 2-5, write the full raw candidate list to memory using the run-section
template above. This is a durability guard against mid-loop LLM termination — once written to disk, the candidate
list survives a session death.

```sh
mkdir -p /workspaces/witwave-self/memory/agents/evan
```

Mandatory regardless of candidate count. Even an empty sweep writes a one-line "empty sweep" entry so the run is
auditable from memory alone.

### 2. Validate per candidate (depth-gated)

For each candidate, walk the **intentional-design gauntlet** at the configured depth's intensity (see depth-scale
table). Drop candidates that any concern catches.

When in doubt, drop. False positives that escape this step waste effort across step 3, step 4, and the human reading
deferred-findings.

### 3. Reason about candidates as a set

Look at the surviving set:

- **Common root cause** — group findings stemming from one underlying issue; fix as one commit.
- **Conflicts** — two candidates touching the same code in incompatible ways; pick the better fix.
- **Cascading risk** — fix for A changes risk for B; order so A's fix lowers B's risk; defer B if A's fix raises it.
- **Order by safety** — smallest blast radius first, fewest dependencies first.

### 4. Decide fix vs. flag (per candidate)

Apply the fix-bar (4 rules above). Bin to **fix** or **flag**.

### 5. Fix each fixable candidate

For each candidate in the fix bin, processed in step-3 order:

1. **Read the code in full** — body + immediate callers + immediate callees. At depth 9-10, full subsystem.
2. **Web-search the API if unfamiliar.** Subtle Go context propagation, asyncio cancellation, controller-runtime
   queue behaviour, Helm template lookup ordering — confirm before writing the fix. If the search reveals the fix
   is more complex than the analyzer suggested, drop to flag with `fix-needs-unfamiliar-api-confirmation`.
3. **Write the fix.** Minimal scope; analyzer's suggestion if obvious, otherwise smallest well-grounded fix.
4. **Run scoped tests locally:**
   - Go: `cd <checkout>/<section> && go test ./...`
   - Python: `cd <checkout> && pytest <section>/`

   **If tests pass** → continue.

   **If tests fail** → fix-forward, ONCE. Read the failure (test name, assertion, traceback). Adjust the fix
   in-place; don't yet revert. Re-run the same scoped tests.
   - Pass on retry → that adjusted code becomes the commit.
   - Still fails on retry → revert (`git -C <checkout> checkout -- <file>`); flag with
     `fix-forward-failed: <one-line>`. Move on.

   Bound: exactly one fix-forward attempt per candidate. Catches small mistakes; prevents bad-fix-begets-worse-fix
   spirals.
5. **Verify the bug is gone.** Re-read the changed code. The analyzer rule that flagged would no longer fire. No
   half-measures, no `TODO`. No adjacent regressions in the surrounding 20 lines.
6. **Commit (one bug per commit):**

   ```sh
   git -C <checkout> add <file>
   git -C <checkout> commit -m "fix(<section>): <one-line bug description>

   <2-4 lines: what was wrong, why it's wrong, what the fix does. Reference the
   analyzer rule (e.g. \"errcheck flagged at <file>:<line>\"). Reference the
   test name that exercises the path.>
   "
   ```

   No unrelated changes.
7. **Mutate the marker in memory IMMEDIATELY.** `[pending]` → `[fixed: <commit-SHA>]`. Per-candidate, not at end of
   run.
8. **At depth 9-10**, also write a regression test that fails on the pre-fix code and passes on the fixed code, in
   the same commit.

### 6. Finalise the run section in memory

Walk every `[pending]` marker still present (candidates that step 4 binned to flag, or step 5 dropped). Mutate to
`[flagged: <reason>]` using the reason vocabulary. Append the descriptive sub-bullets:

```markdown
- **<file>:<line>** `<analyzer rule>` — <one-line summary of what>  [flagged: <reason>]
  - Why: <one-line summary of why it's a bug>
  - Suggested fix: <one-line summary of approach>
```

Within each section, order flagged candidates by severity: data loss / corruption first, then crashes, then logic
errors, then resource leaks, then edge cases.

Mutate the run-section header `**Status: in-progress.**` → `**Status: complete.**` and append the summary line.

### 7. Push + watch CI (via iris)

If `PRE_SWEEP_SHA == HEAD` (no commits produced): skip this step. Report "no fixes committed; M findings logged" and
exit cleanly.

Otherwise: this step is **fully delegated to iris** via `call-peer`. Iris owns all git and GitHub authority for the
team — push posture and `gh`-API operations including the CI watch. Single round-trip handles both, parallel to
kira-commits / iris-pushes and nova-commits / iris-pushes.

Send iris this prompt (substitute the bracketed values):

> Hi iris — evan here. I just landed N bug-fix commits in the local checkout. Please:
>
> 1. Run `git-push` to publish them.
> 2. Watch the CI workflows that trigger on this push. Report each workflow's conclusion (green/red) and run URL.
> 3. **For any red workflow**, also fetch the failing job's log via `gh run view <run-id> --log-failed` and include
>    the relevant excerpt in your report. Don't take remediation action yourself — I'll decide whether to
>    fix-forward or batch-revert.
>
> Commits:
> - `<SHA1>` `fix(<section>): <subject>`
> - …
>
> Pre-sweep SHA was `<PRE_SWEEP_SHA>`.

Wait for her reply.

**Iris reports push failure** (rebase conflict she couldn't resolve): STOP. Surface the situation. The next bug-work
run will re-attempt the delegation naturally.

**Iris reports push success + all CI green**: done. Capture per-workflow conclusion + duration in the run summary.

**Iris reports any CI red**: **fix-forward, ONCE.** Don't reflexively batch-revert. The trunk-based-dev contract is
"if you break main, fix or revert immediately" — fix is preferred, revert is the fallback.

1. **Read iris's failure log excerpt.** Common shapes: test failure, lint/format failure, build/compile failure,
   drift/sync check failure (often pre-existing on `main` and merely surfaced by your push triggering a path-filtered
   workflow).
2. **In scope to fix-forward?** Yes if the fix is small, targeted, clearly remediable from the failure log alone
   (re-run a sync script, quote a shell variable, drop a stray import, adjust an exception chain). No if it would
   require redesigning the original bug fix or reading large swaths of unrelated code.
3. **In scope** → write a fix-forward commit. Run scoped local tests on it (same as step 5.4). Then ask iris to
   push the fix-forward + re-watch CI.
   - All CI green → DONE. Original batch + fix-forward all stay landed. Log
     `[ci-fix-forward: <commit-SHA>]` under the run section.
   - CI still red → fall back to batch-revert (don't recurse on fix-forward).
   - Local tests fail on the fix-forward → fall back to batch-revert.
4. **Out of scope OR fix-forward attempt failed** → batch-revert:

   ```sh
   git -C <checkout> revert --no-commit ${PRE_SWEEP_SHA}..HEAD
   git -C <checkout> commit -m "Revert evan bug-work batch (CI red on <workflow>)

   Fix-forward attempted: <one-line summary or 'not in scope: <reason>'>.
   Batch-revert per trunk-based-dev fallback. Candidates re-surface on the
   next sweep run with the failure noted in deferred-findings.

   Failing run: <gh-run-url>
   "
   ```

   Delegate the revert push to iris. Append a "batch reverted" entry to deferred-findings memory listing each
   reverted commit + the failing workflow URL. If iris's revert push also fails, surface the situation and stop.
   Don't loop.

Bound: **exactly one fix-forward attempt per CI failure event.** Same shape as step 5's local-test fix-forward —
catches small targeted adjustments; prevents spirals.

### 8. Report

Return a structured summary:

- Pre / post SHAs
- Sections scanned, depth used
- Per-section: candidates considered, dropped at gauntlet, fixed (with SHAs), flagged
- Total commits produced (or 0)
- Iris's push outcome
- CI watch outcome (per workflow)
- Pointer to `project_evan_findings.md` for flag-only details
- If batch-reverted: failing workflow URL + revert commit SHA
- If fix-forwarded: fix-forward commit SHA + post-fix-forward CI conclusion

## Out of scope for this skill

- **GitHub issues, labels, comments.** State lives in commits + memory only.
- **Multi-session funnel.** No "approved"/"in-progress" intermediate states across runs. Each run is fresh.
- **Surgical CI revert.** v1 is batch-revert only. Bisect-style identification of the breaking commit is a v2 item.
- **Style / complexity / dead code / type drift / security CVEs / feature gaps.** Lens is correctness only.
- **Pattern invention.** If a fix needs a pattern that doesn't already exist in the file or package, flag instead.

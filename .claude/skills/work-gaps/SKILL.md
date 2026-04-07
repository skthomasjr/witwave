---
name: work-gaps
description: >-
  Fix open enhancement issues one at a time — triage, prioritize, verify, and resolve each gap
  with full context before touching a single line of code
argument-hint: ""
---

Work through all open enhancement issues systematically — get oriented, plan the safest fix order,
verify each gap still exists, then fix them one at a time.

Steps:

1. Load all open gap issues from GitHub:

   Run `/github-issue list type/enhancement` and collect every open gap issue. Read each issue body in
   full. For each gap, note:

   - Issue number and title
   - Priority (`priority/p0` through `priority/p3`)
   - The file and line number
   - What the gap is (plain-language summary)
   - The acceptance criteria (what "done" looks like)

2. Build a complete picture of the codebase before planning anything:

   Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the system's purpose and
   architecture. Then read every source file referenced across all open gap issues. Do not start
   planning until you have read all of them.

3. Triage and sequence the gaps — lowest blast radius first:

   Rank the gaps in the order they should be fixed. Prefer this ordering:

   - Pure additions with no behavior change (add a log line, add a metric, add a missing env var default)
   - Self-contained refactors within a single function or file
   - Changes that touch shared utilities or cross-component paths (higher blast radius — verify carefully)
   - Gaps that require design decisions beyond the scope of a single edit — post a comment and defer

   Within a tier, fix higher-priority gaps first (`p0` before `p1`, etc.).

   Present the planned order with one sentence per gap explaining why it is ranked where it is, then
   proceed immediately to step 4.

4. For each gap, in order:

   **a. Check the issue comment thread.**

   Run `/github-issue view <number>` and read the full body and comment thread. If there is an open
   question that has not been answered:

   - If the issue is already claimed by this agent, unclaim it: update the body
     to set `Claimed by: none` and `Status: status/approved`, then run
     `gh issue edit <number> --body "<updated body>" --add-label "status/approved" --remove-label "status/in-progress"`
   - Skip this gap and move on to the next one. Do not re-claim it until the
     question is resolved.

   **b. Verify the gap still exists.**

   Read the current version of the file(s) cited in the issue. Confirm the problematic code is still
   present and unchanged. If the gap has already been addressed:

   - Run `/github-issue close <number> "Already resolved — closing as implemented"`
   - Move on to the next gap.

   **c. Classify the fix.**

   Determine whether this gap is self-contained (can be improved with a targeted change) or requires
   broader design decisions. If the latter:

   - Run `/github-issue comment <number> "Design decision required — <describe what needs to be decided
     and why it cannot be resolved with a local change>. Leaving open for review."`
   - Move on to the next gap. Do not attempt a fix. Do not proceed to step 4d.

   **d. Claim the issue.**

   Run `/github-issue claim <number> <agent-name>` to mark it in-progress, where `<agent-name>` is the
   value of `$AGENT_NAME` if set, otherwise `local-agent`.

   **e. Fully understand the gap before writing any fix.**

   - Read every file in the call chain — not just the file with the gap.
   - Understand what the correct behavior should be according to the issue's acceptance criteria.
   - Before touching any code, run `/github-issue comment <number> "Plan: <exact change>, in
     <file>:<line> — satisfies acceptance criteria by <reason>"`.
   - If you are not confident in the improvement, post a comment describing what is unclear and what
     information is needed, then move on. Do not attempt a fix under uncertainty.

   **f. Fix the gap.**

   Make the smallest change that satisfies all acceptance criteria. Do not refactor surrounding code,
   add unrelated comments, or clean up other issues. One gap, one fix.

   **g. Verify the fix.**

   - Re-read the changed file(s) to confirm the improvement is correct and nothing was accidentally
     broken.
   - Check that all acceptance criteria from the issue are now met.
   - If tests exist for the affected code, run them:

     ```bash
     cd <repo-root> && python -m pytest <relevant-test-path> -v
     ```

   - If no tests exist, trace the execution path manually and confirm the fix
     handles the edge cases described in the issue.

   **h. Close the issue.**

   Run `/github-issue close <number> "<one sentence describing what was improved and where>"`.

   **i. Commit and push the fix.**

   Stage and commit only the files changed by this fix:

   ```bash
   git add <changed files>
   git commit -m "Fix <short description> (#<number>)"
   ```

   Then push. If the push fails due to a diverged remote, rebase and retry once:

   ```bash
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

   If the push still fails after the rebase, post a comment on the issue:
   `/github-issue comment <number> "Fix committed locally but push failed — manual push required"`,
   then continue to the next gap.

5. After all gaps are resolved (or deferred), report a summary:

   - Gaps fixed (issue number, title, file changed)
   - Gaps closed as already implemented (issue number, reason)
   - Gaps deferred as requiring design decisions (issue number, comment posted)
   - Any gaps skipped and why

6. Reflect on the fix process. If you encountered any ambiguity, missing context, or steps that slowed
   you down — note them and create a GitHub Issue using `/github-issue create task status/approved` with
   type `type/code-quality` describing the specific improvement and which step in this skill it affects.

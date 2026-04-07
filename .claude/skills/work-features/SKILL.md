---
name: work-features
description: >-
  Fix open feature implementation issues one at a time — triage, prioritize,
  verify, and resolve each type/feature issue with full context before touching
  a single line of code
---

Work through all open feature implementation issues systematically — get
oriented, plan the safest fix order, verify each issue still exists, then
fix them one at a time.

Feature implementation issues (`type/feature`) are created by `evaluate-features`
from approved `feature` proposals. Each one is a discrete, scoped unit of work
for a specific theme and slice within a broader feature.

Steps:

1. Load all open feature implementation issues from GitHub:

   Run `/github-issue list type/feature` and collect every open issue. Read
   each issue body in full. For each issue, note:

   - Issue number and title
   - Priority (`priority/p0` through `priority/p3`)
   - The file and line number
   - Feature (`**Feature:** #<number>`), Theme, and Slice
   - What the work is (plain-language summary)
   - The acceptance criteria (what "done" looks like)
   - Dependencies (`**Depends on:**`) — do not start an issue whose dependency
     is still open

2. Build a complete picture of the codebase before planning anything:

   Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the
   system's purpose and architecture. Then read every source file referenced
   across all open feature implementation issues. Do not start planning until
   you have read all of them.

3. Triage and sequence the issues — lowest risk first:

   Rank the issues in the order they should be fixed. Prefer this ordering:

   - Issues with no dependencies and self-contained scope (single file or
     function)
   - Issues that unblock other issues (dependencies)
   - Issues that touch shared utilities or cross-component paths (higher blast
     radius — verify carefully)
   - Issues that require SDK or external API research before implementation

   Within a tier, fix higher-priority issues first (`p0` before `p1`, etc.).
   Skip any issue whose `Depends on:` issue is still open.

   Present the planned order with one sentence per issue explaining why it is
   ranked where it is, then proceed immediately to step 4.

4. For each issue, in order:

   **a. Check the issue comment thread.**

   Run `/github-issue view <number>` and read the full body and comment thread.
   If there is an open question that has not been answered:

   - If the issue is already claimed by this agent, unclaim it: update the body
     to set `Claimed by: none` and `Status: status/approved`, then run
     `gh issue edit <number> --body "<updated body>" --add-label "status/approved" --remove-label "status/in-progress"`
   - Skip this issue and move on to the next one. Do not re-claim it until the
     question is resolved.

   **b. Verify the issue still exists.**

   Read the current version of the file(s) cited in the issue. Confirm the
   work has not already been done. If the issue has already been implemented:

   - Run `/github-issue close <number> "Already implemented — closing as implemented"`
   - Move on to the next issue.

   **c. Claim the issue.**

   Run `/github-issue claim <number> <agent-name>` to mark it in-progress,
   where `<agent-name>` is the value of `$AGENT_NAME` if set, otherwise
   `local-agent`.

   **d. Fully understand the issue before writing any code.**

   - Read the parent `feature` issue (the `**Feature:** #<number>` field) to
     understand the broader goal and acceptance criteria.
   - Read every file in the call chain — not just the file cited in the issue.
   - Trace the execution path from entry point to the change point.
   - Understand what the correct behavior should be according to this issue's
     acceptance criteria.
   - If the fix requires a third-party SDK or library, search the codebase for
     how it is already used, then read the relevant SDK docs or type stubs to
     confirm the correct API.
   - Before touching any code, run `/github-issue comment <number> "Plan: <exact
     change>, in <file>:<line> — satisfies acceptance criteria by <reason>"`.
   - If you are not confident in the implementation, post a comment describing
     what is unclear and what information is needed, then move on to the next
     issue. Do not attempt a fix under uncertainty.

   **e. Implement the issue.**

   Make the smallest change that satisfies all acceptance criteria. Do not
   refactor surrounding code, add comments, or clean up unrelated issues. One
   issue, one change.

   **f. Verify the fix.**

   - Re-read the changed file(s) to confirm the implementation is correct and
     nothing was accidentally broken.
   - Check that all acceptance criteria from the issue are now met.
   - If tests exist for the affected code, run them:

     ```bash
     cd <repo-root> && python -m pytest <relevant-test-path> -v
     ```

   - If no tests exist, trace the execution path manually and confirm the
     change handles the edge cases described in the issue.

   **g. Close the issue.**

   Run `/github-issue close <number> "<one sentence describing what was implemented and where>"`.

   **h. Commit and push the fix.**

   Stage and commit only the files changed by this issue:

   ```bash
   git add <changed files>
   git commit -m "Implement <short description> (#<number>)"
   ```

   Then push. If the push fails due to a diverged remote, rebase and retry once:

   ```bash
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

   If the push still fails after the rebase, post a comment on the issue:
   `/github-issue comment <number> "Implemented locally but push failed — manual push required"`,
   then continue to the next issue.

5. After all issues are resolved, report a summary:

   - Issues implemented (issue number, title, file changed, feature and theme)
   - Issues closed as already implemented (issue number, reason)
   - Issues skipped (issue number, reason — blocked dependency or open question)

6. Reflect on the process. If you encountered any ambiguity, missing context,
   or steps that slowed you down — note them and create a GitHub Issue using
   `/github-issue create task status/approved` with type `type/code-quality`
   describing the specific improvement and which step in this skill it affects.

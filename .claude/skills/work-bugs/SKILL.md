---
name: work-bugs
description: >-
  Fix open bug issues one at a time — triage, prioritize, verify, and resolve
  each bug with full context before touching a single line of code
---

Work through all open bug issues systematically — get oriented, plan the safest
fix order, verify each bug still exists, then fix them one at a time.

Steps:

1. Load all open bug issues from GitHub:

   Run `/github-issue list type/bug` and collect every open bug. Read each issue
   body in full. For each bug, note:

   - Issue number and title
   - Priority (`priority/p0` through `priority/p3`)
   - The file and line number
   - What the bug is (plain-language summary)
   - The acceptance criteria (what "done" looks like)

2. Build a complete picture of the codebase before planning anything:

   Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the
   system's purpose and architecture. Then read every source file referenced
   across all open bug issues. Do not start planning until you have read all of
   them.

3. Triage and sequence the bugs — lowest risk first:

   Rank the bugs in the order they should be fixed. Prefer this ordering:

   - Pure deletions or dead-code removals (no behavior change possible)
   - Self-contained fixes in a single function or file
   - Fixes that touch shared utilities (higher blast radius — test carefully)
   - Fixes that require SDK or API research before implementation
   - Latent bugs with no current user impact (fix last or defer)

   Within a tier, fix higher-priority bugs first (`p0` before `p1`, etc.).

   Present the planned order with one sentence per bug explaining why it is
   ranked where it is, then proceed immediately to step 4.

4. For each bug, in order:

   **a. Check the issue comment thread.**

   Run `/github-issue view <number>` and read the full body and comment thread.
   If there is an open question — either posted by a previous run of this skill
   or by anyone else — that has not been answered:

   - If the issue is already claimed by this agent, unclaim it: update the body
     to set `Claimed by: none` and `Status: status/approved`, then run
     `gh issue edit <number> --body "<updated body>" --add-label "status/approved" --remove-label "status/in-progress"`
   - Skip this bug and move on to the next one. Do not re-claim it until the
     question is resolved.

   **b. Verify the bug still exists.**

   Read the current version of the file(s) cited in the issue. Confirm the
   problematic code is still present and unchanged. If the bug has already been
   fixed:

   - Fetch the current issue body: `gh issue view <number> --json body --jq '.body'`
   - Update the body's `**Status:**` line to `status/implemented`.
   - Apply the updated body and relabel in one call:
     `gh issue edit <number> --body "<updated body>" --add-label "status/implemented" --remove-label "status/pending,status/approved,status/in-progress,status/needs-more-info"`
   - Run `/github-issue close <number> "Already resolved — closing as implemented"`
   - Move on to the next bug.

   **c. Claim the issue.**

   Run `/github-issue claim <number> <agent-name>` to mark it in-progress,
   where `<agent-name>` is the value of `$AGENT_NAME` if set, otherwise
   `local-agent`.

   **d. Fully understand the bug before writing any fix.**

   - Read every file in the call chain — not just the file with the bug.
   - Trace the execution path from entry point to the failure point.
   - Understand what the correct behavior should be according to the issue's
     acceptance criteria.
   - If the fix requires a third-party SDK or library, search the codebase for
     how it is already used, then read the relevant SDK docs or type stubs to
     confirm the correct API.
   - Before touching any code, run `/github-issue comment <number> "Plan: <exact change>, in <file>:<line> — satisfies acceptance criteria by <reason>"`.
   - If you are not confident in the fix, post a comment describing what is
     unclear and what information is needed, then move on to the next bug. Do
     not attempt a fix under uncertainty.

   **e. Fix the bug.**

   Make the smallest change that satisfies all acceptance criteria. Do not
   refactor surrounding code, add comments, or clean up unrelated issues. One
   bug, one fix.

   **f. Verify the fix.**

   - Re-read the changed file(s) to confirm the fix is correct and nothing was
     accidentally broken.
   - Check that all acceptance criteria from the issue are now met.
   - If tests exist for the affected code, run them:

     ```bash
     cd <repo-root> && python -m pytest <relevant-test-path> -v
     ```

   - If no tests exist, trace the execution path manually and confirm the fix
     handles the edge cases described in the issue.

   **g. Close the issue.**

   Run `/github-issue close <number> "<one sentence describing what was fixed and where>"`.

   **h. Commit and push the fix.**

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
   then continue to the next bug.

5. After all bugs are resolved, report a summary:

   - Bugs fixed (issue number, title, file changed)
   - Bugs closed as already implemented (issue number, reason)
   - Any bugs skipped and why

6. Reflect on the fix process. If you encountered any ambiguity, missing
   context, or steps that slowed you down — note them and create a GitHub Issue
   using `/github-issue create task status/approved` with type
   `type/code-quality` describing the specific improvement and which step in
   this skill it affects.

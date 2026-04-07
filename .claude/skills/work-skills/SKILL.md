---
name: work-skills
description: >-
  Fix open skill issues one at a time — triage, prioritize, verify, and resolve
  each skill document bug with full context before touching a single line of
  markdown
---

Work through all open skill issues systematically — get oriented, plan the safest
fix order, verify each issue still exists, then fix them one at a time.

Skill fixes are changes to markdown workflow documents, not source code. The
goal is correct logic, valid cross-skill references, accurate label names, and
consistent conventions — not code correctness.

Steps:

1. Load all open skill issues from GitHub:

   Run `/github-issue list type/skill` and collect every open skill issue. Read
   each issue body in full. For each issue, note:

   - Issue number and title
   - Priority (`priority/p0` through `priority/p3`)
   - The file and line number
   - What the problem is (plain-language summary)
   - The acceptance criteria (what "done" looks like)

2. Build a complete picture of the skill layer before planning anything:

   Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand
   conventions — especially label names, status values, placeholder names, and
   cross-skill patterns. Then read every skill file referenced across all open
   skill issues. Do not start planning until you have read all of them.

3. Triage and sequence the issues — lowest risk first:

   Rank the issues in the order they should be fixed. Prefer this ordering:

   - Pure text corrections with no behavior change (wrong label name, typo in
     command, stale placeholder)
   - Logic fixes within a single skill (contradictory steps, missing skip-resume
     instructions, silent skip conditions)
   - Cross-skill reference fixes (broken subcommand calls, wrong skill name)
   - Convention alignment across multiple skills (higher blast radius — verify
     all affected skills before editing)

   Within a tier, fix higher-priority issues first (`p0` before `p1`, etc.).

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

   Read the current version of the skill file(s) cited in the issue. Confirm the
   problematic instruction is still present and unchanged. If the issue has
   already been fixed:

   - Fetch the current issue body: `gh issue view <number> --json body --jq '.body'`
   - Update the body's `**Status:**` line to `status/implemented`.
   - Apply the updated body and relabel in one call:
     `gh issue edit <number> --body "<updated body>" --add-label "status/implemented" --remove-label "status/pending,status/approved,status/in-progress,status/needs-more-info"`
   - Run `/github-issue close <number> "Already resolved — closing as implemented"`
   - Move on to the next issue.

   **c. Claim the issue.**

   Run `/github-issue claim <number> <agent-name>` to mark it in-progress,
   where `<agent-name>` is the value of `$AGENT_NAME` if set, otherwise
   `local-agent`.

   **d. Fully understand the issue before writing any fix.**

   - Read every skill file in the call chain — not just the file with the bug.
   - Trace the workflow step-by-step from the instruction that is wrong through
     any downstream skills it affects.
   - Understand what the correct behavior should be according to the issue's
     acceptance criteria.
   - If the fix touches a cross-skill reference, verify the target subcommand
     exists in the target skill before editing the calling skill.
   - Before editing any file, run `/github-issue comment <number> "Plan: <exact
     change>, in <file>:<line> — satisfies acceptance criteria by <reason>"`.
   - If you are not confident in the fix, post a comment describing what is
     unclear and what information is needed, then move on to the next issue. Do
     not attempt a fix under uncertainty.

   **e. Fix the issue.**

   Make the smallest edit that satisfies all acceptance criteria. Do not rewrite
   surrounding prose, restructure steps, or clean up unrelated instructions. One
   issue, one fix.

   **f. Verify the fix.**

   - Re-read the changed skill file(s) to confirm the fix is correct and nothing
     was accidentally broken.
   - Check that all acceptance criteria from the issue are now met.
   - If the fix changes a cross-skill reference, verify the target skill still
     has the referenced subcommand.
   - If the fix changes a label name or status value, verify the label exists in
     the repo:

     ```bash
     gh label list --repo skthomasjr/autonomous-agent --json name --jq '.[].name'
     ```

   **g. Close the issue.**

   Run `/github-issue close <number> "<one sentence describing what was fixed and where>"`.

   **h. Commit and push the fix.**

   Stage and commit only the skill files changed by this fix:

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
   then continue to the next issue.

5. After all issues are resolved, report a summary:

   - Issues fixed (issue number, title, file changed)
   - Issues closed as already implemented (issue number, reason)
   - Any issues skipped and why

6. Reflect on the fix process. If you encountered any ambiguity, missing
   context, or steps that slowed you down — note them and create a GitHub Issue
   using `/github-issue create task status/approved` with type
   `type/code-quality` describing the specific improvement and which step in
   this skill it affects.

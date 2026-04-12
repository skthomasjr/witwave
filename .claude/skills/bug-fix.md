---
name: bug-fix
description: Fix all approved bugs one at a time, committing and pushing each fix. Trigger when the user says "fix bugs", "fix approved bugs", "run bug fix", or "start bug fixing".
version: 1.0.0
---

# bug-fix

Fix all approved bugs, one at a time. Each bug is fully resolved, verified, committed, and pushed before moving to the next.

## Instructions

Repeat the following steps until all approved bugs have been processed.

**Step 1: Select the next bug to fix.**

First, check for any open bugs labeled `bug` and `in-progress`. If one exists, resume it — it was already selected and started in a previous run. Do not start a new bug until the in-progress one is resolved.

If no in-progress bug exists, retrieve all open bugs labeled `bug` and `approved`. If none remain, stop — all approved bugs have been fixed.

From the approved bugs, select the one to fix next using this priority order:
- Bugs with no `blocked-by` dependencies come before blocked bugs
- Among unblocked bugs: high priority before medium before low
- Among equal priority: prefer the bug with the most isolated fix (lowest blast radius)
- Among equal blast radius: prefer the bug whose suggested fix is most clearly correct

Once selected, mark the bug as in-progress: add the `in-progress` label, remove the `approved` label, and update the bug status to `in-progress`.

**Step 2: Understand the affected code.**

Read the full source file(s) identified in the bug record — not just the affected lines. Understand what calls the affected code and what it calls. If the bug touches shared utilities or cross-cutting logic, read those files too.

**Step 3: Fix the bug.**

Apply the fix. Use the suggested fix from the bug record unless your code review reveals a better or more correct approach. If you deviate, note why.

Do not fix more than one bug per commit. Do not make unrelated changes.

**Step 4: Verify the fix.**

Re-read the changed code. Confirm:
- The bug condition is gone — the code now behaves correctly
- The fix does not introduce a regression in adjacent code paths
- The fix is complete — no half-measures or TODOs left behind

If the fix is wrong or incomplete, revise it before continuing.

**Step 5: Commit and push.**

Stage only the files changed for this bug. Write a commit message that describes what was fixed and why, referencing the bug number. Push the commit.

**Step 6: Close the bug.**

Close the bug (this removes the `in-progress` label automatically) and leave a comment documenting:
- What was changed and where (file and line)
- Whether the fix followed the suggested approach or deviated, and why
- Confirmation that the fix was verified
- The name and version of this skill (bug-fix v1.0.0)

Return to Step 1.

---
name: bug-implement
description:
  Implement all approved bugs one at a time, committing and pushing each fix. Trigger when the user says "implement
  bugs", "fix bugs", "implement approved bugs", "fix approved bugs", "run bug implement", or "start bug fixing".
version: 1.2.0
---

# bug-implement

Fix all approved bugs, one at a time. Each bug is fully resolved, verified, committed, and pushed before moving to the
next.

## Instructions

Repeat the following steps until all approved bugs have been processed.

**Step 1: Select the next bug to fix.**

First, check for any open bugs labeled `bug` and `approved` whose status is `in-progress`. If one exists, resume it — it
was already selected and started in a previous run. Do not start a new bug until the in-progress one is resolved.

If no in-progress bug exists, retrieve all open bugs labeled `bug` and `approved`. If none remain, stop — all approved
bugs have been fixed.

From the approved bugs, select the one to fix next using this priority order:

- Bugs with no `blocked-by` dependencies come before blocked bugs
- Among unblocked bugs: high priority before medium before low
- Among equal priority: prefer the bug with the most isolated fix (lowest blast radius)
- Among equal blast radius: prefer the bug whose suggested fix is most clearly correct

Once selected, remove the `approved` label, add the `in-progress` label, and update the bug status to `in-progress`.

**Step 2: Understand the affected code.**

Read the full source file(s) identified in the bug record — not just the affected lines. Understand what calls the
affected code and what it calls. If the bug touches shared utilities or cross-cutting logic, read those files too.

**Step 3: Research if needed.**

If the correct behavior or fix approach is unclear — for example, the bug involves an unfamiliar API, a subtle language
or framework behavior, or a fix pattern you are not confident about — do a targeted web search before writing code.
Search for the specific behavior, error, or pattern in question. If a search confirms the right approach, proceed. If
the search reveals the fix is more complex than suggested, note what you found and adjust the approach accordingly.

**Step 4: Fix the bug.**

Apply the fix. Use the suggested fix from the bug record unless your code review reveals a better or more correct
approach. If you deviate, note why.

Do not fix more than one bug per commit. Do not make unrelated changes.

**Step 5: Verify the fix.**

Re-read the changed code. Confirm:

- The bug condition is gone — the code now behaves correctly
- The fix does not introduce a regression in adjacent code paths
- The fix is complete — no half-measures or TODOs left behind

If the fix is wrong or incomplete, revise it before continuing.

**Step 6: Commit and push.**

Stage only the files changed for this bug. Write a commit message that describes what was fixed and why, referencing the
bug number. Push the commit.

**Step 7: Close the bug.**

Leave a comment on the bug documenting:

- What was changed and where (file and line)
- Whether the fix followed the suggested approach or deviated, and why
- Confirmation that the fix was verified
- The name and version of this skill (see the frontmatter of this file)

Then remove the `in-progress` label and close the bug.

Return to Step 1.

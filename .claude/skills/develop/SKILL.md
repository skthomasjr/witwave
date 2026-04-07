---
name: develop
description: Continuous improvement loop — fix bugs and risks, evaluate for new ones, repeat until clean
---

Run a continuous fix loop until the codebase is clean of bugs and risks.

**Looping philosophy:** Fix everything that can be fixed autonomously. For anything that genuinely requires
human input, post a GitHub Issue comment explaining what decision is needed and skip it — do not stop the
loop. Keep iterating until a full pass through all steps produces no new issues and no fixable issues
remain. There is no need to count or track progress between iterations — just keep going until the work
is done.

Steps:

1. Run `/fix-bugs`. Work through every open bug issue until none remain.

2. Run `/evaluate-skills`. Note how many new bug issues were filed.

3. Run `/evaluate-bugs`. Note how many new bug issues were filed.

4. Run `/evaluate-risks`. Note how many new risk issues were filed.

5. Run `/fix-risks`. Work through every open risk issue until none remain (or are deferred with a comment).

6. If any new issues were filed in steps 2, 3, or 4, go to step 1.

7. Once the loop exits cleanly (no new issues filed in steps 2, 3, or 4), and `/evaluate-gaps` has
   not yet run this session, run `/evaluate-gaps` once, then `/fix-gaps` once. Then go to step 1
   to verify no new bugs or risks were introduced by the gap fixes. Skip step 7 on that return pass
   — evaluate-gaps runs exactly once per develop session regardless of how many times step 1 is
   reached afterward.

8. Report that the codebase is clean — no open bugs or risks remain, and the highest-value gaps have
   been addressed.

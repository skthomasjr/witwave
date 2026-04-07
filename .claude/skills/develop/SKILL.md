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

1. Run `/work-skills`, then `/evaluate-skills`. If new skill issues were filed, go to step 1.

2. Run `/work-bugs`, then `/evaluate-bugs`. If new bug issues were filed, go to step 1.

3. Run `/work-risks`, then `/evaluate-risks`. If new risk issues were filed, go to step 1.

4. Once steps 1, 2, and 3 all complete without filing any new issues, run `/work-gaps`, then
   `/evaluate-gaps`. If new gap issues were filed, go to step 1.

5. Report that the codebase is clean — no open bugs, skill issues, risks, or gaps remain.

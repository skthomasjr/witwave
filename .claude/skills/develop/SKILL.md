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

4. Once steps 1, 2, and 3 all complete without filing any new issues, run `/evaluate-gaps`,
   then `/work-gaps`.

5. Run `/evaluate-features`, then `/work-features`.

6. Report a summary of the run. Track the step number reached each iteration
   and the reason for any restart. Format the report as both a path trace and
   a pass table:

   **Path trace** — shows the sequence of steps and where restarts occurred:

   ```
   Run path:  1 → 2 → 3 → 1 → 2 → 3 → 4 → 5 → 6
                    ↑               ↑
               new risks        new bugs
   ```

   **Pass table** — one row per pass through the loop:

   | Pass | Skills | Bugs | Risks | Gaps | Features | Restart? |
   |------|--------|------|-------|------|----------|----------|
   | 1    | 1      | 3    | 2     | —    | —        | yes (risks filed) |
   | 2    | 0      | 1    | 0     | —    | —        | yes (bugs filed) |
   | 3    | 0      | 0    | 0     | 3    | 2        | no → done |

   **Issues resolved: 14  |  Blocked: 3 (awaiting human input)**

   Show both. Keep the prose minimal — the visuals tell the story.

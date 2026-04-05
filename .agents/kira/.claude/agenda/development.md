---
name: Development
description:
  Works through TODO.md in priority order — Bugs first, then Reliability, then Code Quality, then Enhancements — one
  item per run.
schedule: "*/30 * * * *"
enabled: true
---

Resolve the highest-priority unchecked item in `<repo-root>/TODO.md`.

Priority order — move to the next section only when the current section has no remaining unchecked `- [ ]` items:

1. **Bugs** — must be fully clear before any Reliability work begins
2. **Reliability** — must be fully clear before any Code Quality work begins
3. **Code Quality** — must be fully clear before any Enhancements work begins
4. **Enhancements**

Steps:

1. Run `/todo lock fixing kira` to acquire the lock. If the skill reports the file is locked by another agent, abort —
   do not proceed.
2. Read `<repo-root>/TODO.md`. Find the active section using the priority order above. If all sections are fully clear
   (no unchecked `- [ ]` items), run `/todo unlock kira` to release the lock, then stop — do not proceed further this
   run.
3. From the unchecked `- [ ]` items in the active section, select the lowest risk item — prefer items isolated to a
   single file, with no side effects on other components, requiring the smallest change.
4. If the selected item contains a GitHub issue number (e.g. `[#42]`), claim it using
   `/github-issue claim <number> kira` to mark it in-progress. If the item has no issue number, skip this step.
5. Read `<repo-root>/README.md` and `<repo-root>/CLAUDE.md` to understand the purpose, architecture, and intended
   behavior of the system. Use this as the lens for the fix.
6. If the selected item includes a feature ID prefix (e.g. `[F-002]`), read `<repo-root>/docs/features-proposed.md` and
   locate that feature's row. Use the Value, Implementation, and Risk fields as the authoritative description of what to
   build and how — do not deviate from the specified implementation without good reason.
7. Read the relevant source file(s) for the selected item. Understand the surrounding code and how it fits into the
   broader system before making any changes.
8. Implement the fix. Make the minimal change necessary — do not refactor surrounding code, add features, or change
   unrelated behavior.
9. Review the change critically before considering it done. Re-read the modified file in full. Ask: Does this fix
   actually solve the problem as described? Could it introduce a regression elsewhere? Are there edge cases the fix
   doesn't handle? Does it interact with any other component in an unexpected way? If you find a problem, revise the
   fix. Do not mark the item complete until you are confident the change is correct and does not introduce new issues.
10. Run `/lint <file1> [file2 ...]` on all files you modified. If lint reports errors it cannot auto-fix, fix them
    manually before proceeding. Do not mark the item complete while lint errors remain.
11. Evaluate whether the change warrants any documentation updates. Read `README.md`, `CLAUDE.md`, and any relevant
    files under `docs/` and ask: does this change introduce new behavior, configuration, environment variables, files,
    or conventions that a developer or agent would need to know about? If so, make the minimal necessary updates —
    correct stale references, add new env vars or config options, update architecture descriptions. Do not rewrite or
    expand documentation beyond what the change directly warrants.
12. Mark the resolved item as complete in `TODO.md` by changing its `- [ ]` to `- [x]`. Keep the full line intact — do
    not delete it, truncate it, or remove it from the list. Do not modify any other items.
13. If the completed item contained a GitHub issue number (e.g. `[#42]`), close it using
    `/github-issue close <number> "Resolved by kira"`. If the item had no issue number, skip this step.
14. If the completed item had a feature ID prefix (e.g. `[F-007]`), check whether all `TODO.md` items with that same
    feature ID are now marked `[x]`. If they are all complete, run `/features graduate F-XXX` (substituting the actual
    feature ID) to move it from `features-proposed.md` to `features-completed.md`.
15. Run `/todo unlock kira` to release the lock.
16. Run `/todo cleanup` to remove completed items and stamp fully-cleared sections.
17. Re-read `<repo-root>/TODO.md` and check whether all sections are fully clear (no remaining unchecked `- [ ]` items).
    If they are all clear, run `/delegate iris /run-agenda work-evaluation` to trigger a fresh evaluation immediately —
    do not wait for the next scheduled run.
18. Do not do anything else.

---
name: Development
description:
  Works through TODO.md in priority order — Bugs first, then Reliability, then Code Quality, then Enhancements — one
  item per run.
schedule: "*/10 * * * *"
enabled: true
---

Resolve the highest-priority unchecked item in `~/workspace/TODO.md`.

Priority order — move to the next section only when the current section has no remaining unchecked `- [ ]` items:

1. **Bugs** — must be fully clear before any Reliability work begins
2. **Reliability** — must be fully clear before any Code Quality work begins
3. **Code Quality** — must be fully clear before any Enhancements work begins
4. **Enhancements**

Steps:

1. Run `/todo lock fixing kira` to acquire the lock. If the skill reports the file is locked by another agent, abort —
   do not proceed.
2. Read `~/workspace/TODO.md`. Find the active section using the priority order above. If all
   sections are fully clear (no unchecked `- [ ]` items), run `/todo unlock kira` to release the lock, then stop — do
   not proceed further this run.
3. From the unchecked `- [ ]` items in the active section, select the lowest risk item — prefer items isolated to a
   single file, with no side effects on other components, requiring the smallest change.
4. Read `~/workspace/README.md` and `~/workspace/CLAUDE.md` to
   understand the purpose, architecture, and intended behavior of the system. Use this as the lens for the fix.
5. If the selected item includes a feature ID prefix (e.g. `[F-002]`), read
   `~/workspace/docs/features-proposed.md` and locate that feature's row. Use the Value,
   Implementation, and Risk fields as the authoritative description of what to build and how — do not deviate from the
   specified implementation without good reason.
6. Read the relevant source file(s) for the selected item. Understand the surrounding code and how it fits into the
   broader system before making any changes.
7. Implement the fix. Make the minimal change necessary — do not refactor surrounding code, add features, or change
   unrelated behavior.
8. Review the change critically before considering it done. Re-read the modified file in full. Ask: Does this fix
   actually solve the problem as described? Could it introduce a regression elsewhere? Are there edge cases the fix
   doesn't handle? Does it interact with any other component in an unexpected way? If you find a problem, revise the
   fix. Do not mark the item complete until you are confident the change is correct and does not introduce new issues.
9. Run `/lint <file1> [file2 ...]` on all files you modified. If lint reports errors it cannot auto-fix, fix them
   manually before proceeding. Do not mark the item complete while lint errors remain.

10. Evaluate whether the change warrants any documentation updates. Read `README.md`, `CLAUDE.md`, and any relevant
    files under `docs/` and ask: does this change introduce new behavior, configuration, environment variables, files,
    or conventions that a developer or agent would need to know about? If so, make the minimal necessary updates —
    correct stale references, add new env vars or config options, update architecture descriptions. Do not rewrite or
    expand documentation beyond what the change directly warrants.
11. Mark the resolved item as complete in `TODO.md` by changing its `- [ ]` to `- [x]`. Keep the full line intact — do
    not delete it, truncate it, or remove it from the list. Do not modify any other items.
12. If the completed item had a feature ID prefix (e.g. `[F-007]`), check whether all `TODO.md` items with that same
    feature ID are now marked `[x]`. If they are all complete, run `/features graduate F-XXX` (substituting the actual
    feature ID) to move it from `features-proposed.md` to `features-completed.md`.
13. Run `/todo unlock kira` to release the lock.
14. Run `/todo cleanup` to remove completed items and stamp fully-cleared sections.
15. Re-read `~/workspace/TODO.md` and check whether all sections are fully clear (no remaining
    unchecked `- [ ]` items). If they are all clear, run `/delegate iris /run-agenda work-evaluation` to trigger a fresh
    evaluation immediately — do not wait for the next scheduled run.
16. Do not do anything else.

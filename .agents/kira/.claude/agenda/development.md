---
name: Development
description:
  Works through approved GitHub Issues in priority order — Bugs first, then Reliability, then Code Quality, then
  Enhancements — one item per run.
schedule: "*/30 * * * *"
enabled: true
---

Resolve the highest-priority approved GitHub Issue.

Priority order — work the highest priority type that has approved issues before moving to the next:

1. `type/bug` — must be fully clear before any `type/reliability` work begins
2. `type/reliability` — must be fully clear before any `type/code-quality` work begins
3. `type/code-quality` — must be fully clear before any `type/enhancement` work begins
4. `type/enhancement`

Within each type, work `priority/p0` before `priority/p1`, `priority/p1` before `priority/p2`, and so on.

Steps:

1. Use `/github-issue list status/approved` to find all open approved issues. If none exist, stop — there is nothing
   to do this run.
2. From the results, determine the active type using the priority order above. Within that type, select the
   lowest-numbered priority label. From the matching issues, prefer the one with the smallest scope — isolated to a
   single file, minimal side effects, smallest change.
3. Fetch the full issue body using `gh issue view <number> --json body --jq '.body'` to read the description,
   file reference, and acceptance criteria.
4. Claim the issue using `/github-issue claim <number> kira` to mark it in-progress. If the claim fails because the
   issue is already claimed, return to step 1 and select the next candidate.
5. Read `<repo-root>/README.md` and `<repo-root>/CLAUDE.md` to understand the purpose, architecture, and intended
   behavior of the system. Use this as the lens for the fix.
6. If the issue body contains a feature ID (e.g. `[F-002]`), read `<repo-root>/docs/features-proposed.md` and locate
   that feature's row. Use the Value, Implementation, and Risk fields as the authoritative description of what to build
   and how — do not deviate from the specified implementation without good reason.
7. Read the relevant source file(s). Understand the surrounding code and how it fits into the broader system before
   making any changes.
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
12. Close the issue using `/github-issue close <number> "Resolved by kira"`.
13. If the issue body contains a feature ID (e.g. `[F-007]`), run
    `gh search issues "[F-007]" --state open --repo skthomasjr/autonomous-agent` to check whether any issues for that
    feature are still open. If none remain open, run `/features graduate F-XXX` to move it from
    `features-proposed.md` to `features-completed.md`.
14. Check whether any approved issues remain open. If none remain across all types, run
    `/delegate iris /run-agenda work-evaluation` to trigger a fresh evaluation immediately — do not wait for the next
    scheduled run.
15. Do not do anything else.

---
name: Work Evaluation
description:
  Evaluates the source codebase and updates TODO.md with current bugs, reliability issues, and code quality findings.
schedule: "0 * * * *"
enabled: true
---

Review the source code in `~/workspace` and update
`~/workspace/TODO.md`.

Steps:

1. Run `/todo lock reviewing iris` to acquire the lock. If the skill reports the file is locked by another agent, abort
   — do not proceed.
2. Read `~/workspace/README.md` and `~/workspace/CLAUDE.md` to
   understand the purpose, architecture, and intended behavior of the system. Use this as the lens for the entire review
   — evaluate code against what it is supposed to do, not just whether it is technically correct in isolation.
3. Read all source files under `~/workspace/` using the glob pattern `**/*.py` and also read
   `Dockerfile`.
4. Perform a deep review of the code. For each file, read and reason about it carefully. Evaluate across four
   dimensions:

   - **Bugs & correctness** — trace execution paths, check error handling, race conditions, resource leaks, unbounded
     growth, incorrect assumptions, missing edge cases, deprecated API usage, and security concerns
   - **Reliability & architecture** — assess whether the overall design is sound: are components correctly coupled, are
     failure boundaries well-placed, would a component failure cascade or be contained, are there architectural
     decisions that make the system fragile or hard to recover? Flag anything that could cause incorrect behavior or
     instability at a structural level
   - **Simplification** — identify code that is more complex than it needs to be: unnecessary abstractions, convoluted
     logic that could be expressed more directly, duplicated code, dead code, or anything that works but is harder to
     understand than it should be
   - **Maintainability & flexibility** — flag anything that makes the codebase hard to change: hardcoded values that
     should be configurable, tight coupling that would make future changes ripple unexpectedly, missing seams for
     extensibility, or patterns that would force large rewrites for small feature changes

   Do not skim. Consider how components interact with each other, not just in isolation.

5. **Prometheus metrics review** — check whether any unchecked `- [ ]` item in `TODO.md` already proposes adding a new
   Prometheus metric (look for keywords: `metric`, `counter`, `gauge`, `histogram`, `prometheus`). If no such item
   exists, perform the following analysis and add one Enhancements item:

   a. Read `agent/main.py` and identify every metric currently defined (name, type, labels). b. Evaluate what the next
   most valuable metric would be. Prefer metrics that reveal agent health or behavior that is not already observable —
   consider in this order:

   - **Request/task metrics** — counters or histograms on task volume, latency, or outcome (success/error/timeout)
   - **Session metrics** — active sessions, session evictions, session reuse rate
   - **Queue/bus metrics** — messages queued, processing lag
   - **Business metrics** — agenda runs completed, agenda runs failed, heartbeat latency
   - **System metrics** — if none of the above adds meaningful new observability c. Choose the single metric with the
     highest signal-to-noise ratio given what is already instrumented. Do not propose a metric already covered by an
     existing definition in `main.py`. d. Add one Enhancements item describing: the metric name, type, labels, where in
     the code to define it (`main.py`) and where to increment or observe it (the specific file and call site), and why
     it is the most valuable next metric.

6. For each existing unchecked `- [ ]` item in `TODO.md`, verify it against the current code — confirm whether the issue
   still exists at the referenced file and line. Remove unchecked items that are no longer applicable. Keep items that
   are still valid, updating file/line references if they have shifted. Add any newly discovered issues using the
   following section mapping. Never remove or modify items already marked `- [x]` — those are a permanent record of
   completed work and must be preserved exactly as written.

   - **Bugs** — incorrect behavior, logic errors, crashes, data corruption
   - **Reliability** — architectural issues, incorrect coupling, missing failure boundaries, fragile design, anything
     structural that could cause instability or make the system hard to recover
   - **Code Quality** — simplification opportunities, unnecessary complexity, hardcoded values that should be
     configurable, tight coupling that would make future changes painful, maintainability or flexibility concerns

   Preserve the existing section structure. Do not create new sections.

7. Review the quality of all TODO entries — existing and new. Each item must: reference a specific file and line number,
   describe the problem clearly and concisely, be free of spelling and grammatical errors, and be placed in the correct
   section. Fix any entries that fall short of this standard.
8. If the review reveals that `README.md` or `CLAUDE.md` contain clearly incorrect or outdated information (e.g., wrong
   file paths, removed files, stale instructions), make the minimal necessary corrections. Do not rewrite or expand
   them.
9. If there are files or directories that should be ignored by git but are not covered by `.gitignore`, add them. Check
   for common runtime artifacts: `*.log`, `__pycache__`, `.env`, and any agent runtime directories that should not be
   committed.
10. Run `/lint-markdown` to fix any markdown violations introduced in the files you modified.
11. Run `/todo unlock iris` to release the lock.
12. Do not touch files under `docs/`. Do not add commentary or explanations outside the files being updated. Do not do
    anything else.

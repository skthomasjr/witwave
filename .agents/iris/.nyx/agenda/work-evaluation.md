---
name: Work Evaluation
description:
  Evaluates the source codebase and creates GitHub Issues for bugs, reliability issues, and code quality findings.
schedule: "0 * * * *"
enabled: false
---

Review the source code in the repo root and create GitHub Issues for findings.

Steps:

1. Read `<repo-root>/README.md` and `<repo-root>/CLAUDE.md` to understand the purpose, architecture, and intended
   behavior of the system. Use this as the lens for the entire review — evaluate code against what it is supposed to do,
   not just whether it is technically correct in isolation.
2. Read all source files under `<repo-root>/` using the glob pattern `**/*.py` and also read `Dockerfile`.
3. Perform a deep review of the code. For each file, read and reason about it carefully. Evaluate across four
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

4. **Prometheus metrics review** — use `/github-issue list type/enhancement` to check whether any open issue already
   proposes adding a new Prometheus metric (look for keywords: `metric`, `counter`, `gauge`, `histogram`, `prometheus`).
   If no such issue exists, perform the following analysis and create one enhancement issue:

   a. Read `agent/main.py` and identify every metric currently defined (name, type, labels). b. Evaluate what the next
   most valuable metric would be. Prefer metrics that reveal agent health or behavior that is not already observable —
   consider in this order:

   - **Request/task metrics** — counters or histograms on task volume, latency, or outcome (success/error/timeout)
   - **Session metrics** — active sessions, session evictions, session reuse rate
   - **Queue/bus metrics** — messages queued, processing lag
   - **Business metrics** — agenda runs completed, agenda runs failed, heartbeat latency
   - **System metrics** — if none of the above adds meaningful new observability

   c. Choose the single metric with the highest signal-to-noise ratio given what is already instrumented. Do not propose
   a metric already covered by an existing open issue or existing definition in `main.py`. d. Create one GitHub Issue
   describing: the metric name, type, labels, where in the code to define it (`main.py`) and where to increment or
   observe it (the specific file and call site), and why it is the most valuable next metric.

5. Check all currently open GitHub Issues of type `type/bug`, `type/reliability`, and `type/code-quality` using
   `/github-issue list`. For each open issue, verify it against the current code — confirm whether the problem described
   still exists at the referenced file and line. If a finding is no longer applicable, close the issue using
   `/github-issue close <number> "No longer applicable — resolved without a direct fix"`.

6. For each new finding from the code review, first check whether an open issue already covers it by running
   `gh search issues "<filename> <brief keyword>" --state open --repo $GH_REPO`. Only create an issue if no equivalent
   open issue exists. Create using `/github-issue create task status/approved`. The issue should be self-contained and
   readable without any other context. Include:

   - **Type** — `type/bug`, `type/reliability`, `type/code-quality`, or `type/enhancement`
   - **Priority** — default to `priority/p2` unless the finding is a crash, data corruption, or security issue
     (`priority/p0`) or a significant reliability risk (`priority/p1`)
   - **Created by** — `iris`
   - **File** — the specific file and line number
   - **Description** — a full paragraph explaining: what the problem is, why it exists, what could go wrong if left
     unaddressed, and any relevant context from the code review
   - **Acceptance criteria** — one or more specific, verifiable conditions that define done
   - **Notes** — any additional context: related files, execution paths, or prior decisions that are relevant

7. If the review reveals that `README.md` or `CLAUDE.md` contain clearly incorrect or outdated information (e.g., wrong
   file paths, removed files, stale instructions), make the minimal necessary corrections. Do not rewrite or expand
   them.
8. If there are files or directories that should be ignored by git but are not covered by `.gitignore`, add them. Check
   for common runtime artifacts: `*.log`, `__pycache__`, `.env`, and any agent runtime directories that should not be
   committed.
9. Run `/lint-markdown` to fix any markdown violations introduced in the files you modified.
10. Do not touch files under `docs/`. Do not add commentary or explanations outside the files being updated. Do not do
    anything else.

---
name: evaluate-gaps
description: >-
  Deep analysis of the source codebase to find enhancement opportunities — creates a GitHub Issue for each finding
---

Review the source code and create GitHub Issues for every enhancement opportunity found.

Steps:

1. Load all existing open enhancement issues from GitHub so you can avoid creating duplicates throughout the review.
   Run `/github-issue list type/enhancement` and keep the results in mind for every finding — if a finding is already
   covered by an open issue, skip it.

2. Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the purpose, architecture, and intended
   behavior of the system. Use this as the lens for the entire review — evaluate code against what it is supposed to
   do, not just whether it is technically correct in isolation.

3. Read all files under `<repo-root>/docs/`.

4. Discover the full structure of the repository:

   - List all top-level directories under `<repo-root>/`, skipping `.agents/`, `.claude/`, `.codex/`, `.nyx/`,
     `.git/`, and `node_modules/`.
   - For each directory found, identify what type of project it is by looking for: `**/*.py`, `**/*.ts`, `**/*.js`,
     `**/*.go`, `**/*.rs`, `**/*.java`, `**/*.cs`, `Dockerfile*`, `docker-compose*.yml`, `Makefile`.
   - Also read any dependency/manifest files present: `requirements.txt`, `pyproject.toml`, `package.json`,
     `package-lock.json`, `go.mod`, `Cargo.toml`, `pom.xml`, `*.csproj`.

5. Read every source file discovered in step 4 in full. Do not skim. After reading all files, build a mental model
   of how the components connect — data flow, call chains, shared state, error propagation paths — before drawing
   any conclusions.

6. Perform a deep code quality review. Focus exclusively on gaps — things that **work but could be meaningfully
   better**. A gap is not a bug, not a style issue, and not a hypothetical future feature. It must be a concrete
   improvement to code that is already running and must be fixable in a small, self-contained change.

   **Worth finding:**
   - Dead code, unreachable paths, or unused variables and imports
   - Duplicated logic in two or more places that should be consolidated
   - Inconsistent patterns across similar components (e.g. one logs errors, another silently swallows them)
   - Missing error handling at system boundaries (external calls, file I/O, network)
   - Hardcoded values that should be configurable via environment variables or config files
   - Missing Prometheus metrics on critical paths where behavior is currently unobservable
   - Missing or misleading log messages that would leave an operator guessing during an incident
   - Missing health or liveness signals for background tasks, scheduled jobs, or long-running loops
   - Silent successes or failures in background work that leave no trace in logs or metrics

   **Not a gap — skip these:**
   - Style, naming, or formatting preferences
   - Things that only matter for hypothetical future features
   - Refactors that change structure without improving behavior or observability
   - Functions "doing too many things" without a concrete behavioral problem
   - Tightly coupled components where no actual coupling defect exists today
   - Anything requiring significant redesign — gaps must be small and self-contained

7. From all the gaps identified, select only the **single highest-value finding per category** (code quality,
   observability, logging, etc.). Do not create issues for every gap found — pick the one that would deliver the
   most improvement in each category if fixed. Future runs will surface the next most valuable finding. For each
   selected finding, cross-reference against the list loaded in step 1. If not already covered, also run
   `/github-issue search "<filename> <brief keyword>"` as a secondary check. Only proceed if no equivalent open
   issue exists.

8. For each new gap, run `/github-issue create task status/approved` and provide:

   - **Type:** `type/enhancement`
   - **Priority:** `priority/p3` by default; `priority/p2` if the gap meaningfully increases risk of future bugs
     or makes the system hard to debug in production
   - **Created by:** `<agent-name>` (value of `$AGENT_NAME` if set, otherwise `local-agent`)
   - **File:** specific file and line number
   - **Description:** start with a plain-language summary (1-2 sentences): what is missing or incomplete and
     why it matters, written so anyone can understand it without opening the code. Then provide the technical
     detail: what the gap is, why it exists, what could go wrong or become harder if left unaddressed
   - **Acceptance criteria:** specific, verifiable conditions that define done
   - **Notes:** related files, execution paths, or relevant context

9. Report a summary of all issues created (or skipped as duplicates).

10. Reflect on the review process itself. If you encountered any friction, gaps, or opportunities to improve this
    skill — missing quality categories, steps that were unclear, search queries that produced poor results, or
    patterns that would have been caught earlier with a different approach — create a GitHub Issue using
    `/github-issue create task status/approved` with type `type/code-quality`, describing the specific improvement
    and which step in this skill it affects.

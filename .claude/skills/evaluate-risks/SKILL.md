---
name: evaluate-risks
description: >-
  Deep analysis of the source codebase to find security, reliability, and code-quality risks — creates a GitHub Issue for each finding
---

Review the source code and create GitHub Issues for every risk found — security issues, reliability concerns, and
code-quality problems that work today but are fragile or dangerous.

Steps:

1. Load all existing open risk issues from GitHub so you can avoid creating duplicates throughout the review.
   Run `/github-issue list type/reliability`, `/github-issue list type/code-quality`, and check for any security
   issues in `/github-issue list type/bug` (security bugs may be filed there). Keep all results in mind for every
   finding — if a finding is already covered by an open issue, skip it.

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
   of how the components connect — data flow, call chains, failure boundaries, retry paths, shared state — before
   drawing any conclusions.

6. Perform a deep risk review across three categories. Things that work under normal conditions but could
   fail, degrade, or cause harm. Look for:

   **Reliability risks:**
   - Missing timeouts on network calls, subprocesses, or external APIs
   - No retry logic where transient failures are likely
   - Failure in one component cascading to others with no isolation boundary
   - Unbounded growth — queues, lists, caches, or logs that grow without limit
   - No graceful degradation when a dependency is unavailable
   - Single points of failure with no fallback
   - Startup or shutdown sequences that could leave the system in a broken state
   - Assumptions about ordering or timing that could break under concurrency
   - Missing health checks or liveness signals that would hide a silent failure
   - Configuration that works in development but is unsafe or fragile in production

   **Security risks:**
   - Credentials, tokens, or secrets logged, exposed in error messages, or passed through untrusted channels
   - Input from external sources (A2A messages, env vars, config files) used without validation or sanitization
   - Path traversal or directory escape via user-controlled strings
   - Insecure defaults (open ports, world-readable files, no auth on endpoints)
   - Subprocess or shell calls constructed from external input (command injection)
   - Third-party dependencies with known vulnerabilities or pulled from untrusted sources

   **Code-quality risks:**
   - Logic that is correct today but will silently break if a dependency changes its behavior
   - Shared mutable state with no concurrency protection
   - Functions with too many responsibilities that make future changes error-prone
   - Missing validation at system boundaries that would allow bad state to propagate inward
   - Hardcoded values that should be configuration (URLs, timeouts, limits)
   - Deprecated API usage that will break on the next library version

7. For each risk found, cross-reference against the list loaded in step 1. If not already covered, also run
   `/github-issue search "<filename> <brief keyword>"` as a secondary check. Only proceed if no equivalent open
   issue exists.

8. For each new risk, run `/github-issue create task status/approved` and provide:

   - **Type:** `type/reliability` for reliability and security risks; `type/code-quality` for code-quality risks.
     Security risks that could cause data loss or unauthorized access should be `priority/p0`.
   - **Priority:** `priority/p1` by default; `priority/p0` if the risk could cause data loss, total outage,
     unrecoverable state, or a security breach; `priority/p2` if the risk is unlikely or only affects
     non-critical paths; `priority/p3` for cosmetic or very low-impact code-quality issues
   - **Created by:** `<agent-name>` (value of `$AGENT_NAME` if set, otherwise `local-agent`)
   - **File:** specific file and line number
   - **Description:** start with a plain-language summary (1-2 sentences): what could go wrong and under what
     conditions, written so anyone can understand it without opening the code. Then provide the technical
     detail: what the risk is, under what conditions it would trigger, and what the impact would be
   - **Acceptance criteria:** specific, verifiable conditions that define done
   - **Notes:** related files, execution paths, or relevant context

9. Report a summary of all issues created (or skipped as duplicates).

10. Reflect on the review process itself. If you encountered any friction, gaps, or opportunities to improve this
    skill — missing risk categories, steps that were unclear, search queries that produced poor results, or patterns
    that would have been caught earlier with a different approach — create a GitHub Issue using
    `/github-issue create task status/approved` with type `type/code-quality`, describing the specific improvement
    and which step in this skill it affects.

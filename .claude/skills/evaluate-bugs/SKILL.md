---
name: evaluate-bugs
description: Deep analysis of the source codebase to find bugs — creates a GitHub Issue for each finding
---

Review the source code and create GitHub Issues for every bug found.

Steps:

1. Load all existing open bug issues from GitHub so you can avoid creating duplicates throughout the review.
   Run `/github-issue list type/bug` and keep the results in mind for every finding — if a finding is already
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
     `package-lock.json`, `go.mod`, `Cargo.toml`, `pom.xml`, `*.csproj`. These reveal version mismatches,
     missing dependencies, and outdated packages that are a common source of bugs.
   - Read `<repo-root>/Dockerfile` and `<repo-root>/docker-compose.yml` in full.

5. Read every source file discovered in step 4 in full. Do not skim. After reading all files, build a mental model
   of how the components connect — data flow, call chains, shared state, error propagation paths — before drawing
   any conclusions.

6. Perform a deep bug review. Focus exclusively on bugs — trace execution paths and look for:

   - Incorrect logic or wrong assumptions about inputs, state, or ordering
   - Unhandled error cases or exceptions that could crash or corrupt state
   - Race conditions or concurrency hazards
   - Resource leaks (file handles, connections, subprocesses, memory)
   - Off-by-one errors, boundary conditions, empty or null inputs
   - Incorrect use of APIs or library interfaces (including version-specific behavior)
   - Security vulnerabilities (injection, credential exposure, path traversal, insecure defaults)
   - Silent failures where errors are swallowed without logging or propagation
   - Dependency version mismatches or known-broken version ranges
   - Cross-component bugs that only appear when two or more components interact
   - Dockerfile bugs: incorrect base image assumptions, missing packages, wrong file permissions, build steps
     that silently fail
   - docker-compose bugs: misconfigured volume mounts, missing or incorrect environment variables, port
     conflicts, missing `restart` policies that would leave a crashed agent unrecovered
   - Cross-file consistency: every env var referenced in `docker-compose.yml` should have a corresponding
     default in the Python source; every host path in a volume mount should exist in the repository

7. For each bug found, cross-reference against the list loaded in step 1. If not already covered, also run
   `/github-issue search "<filename> <brief keyword>"` as a secondary check. Only proceed if no equivalent open
   issue exists.

8. For each new bug, run `/github-issue create task status/approved` and provide:

   - **Type:** `type/bug`
   - **Priority:** `priority/p2` by default; `priority/p0` for crashes, data corruption, or security issues;
     `priority/p1` for significant reliability risks
   - **Created by:** `<agent-name>` (value of `$AGENT_NAME` if set, otherwise `local-agent`)
   - **File:** specific file and line number
   - **Description:** start with a plain-language summary (1-2 sentences): what is broken and what is the
     worst-case impact, written so anyone can understand it without opening the code. Then provide the technical
     detail: what the bug is, why it exists, what could go wrong if left unaddressed
   - **Acceptance criteria:** specific, verifiable conditions that define done
   - **Notes:** related files, execution paths, or relevant context

9. Report a summary of all issues created (or skipped as duplicates).

10. Reflect on the review process itself. If you encountered any friction, gaps, or opportunities to improve this
    skill — missing bug categories, steps that were unclear, search queries that produced poor results, or patterns
    that would have been caught earlier with a different approach — create a GitHub Issue using
    `/github-issue create task status/approved` with type `type/code-quality`, describing the specific improvement
    and which step in this skill it affects.

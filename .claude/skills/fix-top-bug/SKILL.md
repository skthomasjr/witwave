---
name: fix-top-bug
description: Find and fix the top N bugs in the codebase, commit, and push — no issues, no ceremony
argument-hint: "[N]"
---

Find and fix the top bugs in the codebase, commit each fix, and push.

No GitHub issues. No comments. No tracking. Just find them, fix them, ship them.

**This skill targets things that are already broken** — code producing wrong results, crashes, misleading output, missing docs. If the code works correctly today but is fragile or dangerous under future conditions, use `/fix-top-risk` instead.

## Argument

The optional argument is a number — how many bugs to fix in this run. Default is `1` if no argument is given.

Examples: `/fix-top-bug`, `/fix-top-bug 5`, `/fix-top-bug 20`

If fewer bugs exist than the requested count, fix all that exist and report the shortfall.

## Scope

Search only these locations — nothing else:

- `agent/` — nyx-agent infrastructure (router, scheduler, bus, backends)
- `a2-claude/` — Claude backend server
- `a2-codex/` — Codex backend server
- `ui/` — web UI (future container)
- Top-level config files: `docker-compose.active.yml`, `docker-compose.test.yml`
- Top-level docs: `README.md`, `AGENTS.md`, `CLAUDE.md`

Do **not** look in `.agents/` or `.claude/`. Agent-specific configuration and AI prompts are out of scope.

## The severity bar

Before fixing anything, ask: **does this defect have a real consequence that someone would care about?** This includes things a user or operator would notice, things an operator would be confused by during an incident, or things that silently produce wrong results.

**Fix it** — has a real consequence:
- Causes a crash, hang, or unhandled exception in normal operation
- Causes incorrect output or silent data loss on a reachable code path
- Causes a resource leak (task, file handle, thread) that accumulates over time in normal use
- A metric that is wrong (hardcoded value, never recorded, wrong label, or conditionally incorrect) on a reachable path
- A log message that would mislead or confuse an operator during an incident
- A doc error or gap that would leave a developer confused, cause a command to fail, or lead to misconfiguration
- Dead code or imports that exist on reachable paths and could mask real errors

**Do not fix** — stop and report "no actionable bugs found":
- Only triggered by deliberate misconfiguration or adversarial input
- An edge case that cannot be reached in normal operation

If nothing clears the **Fix it** bar, stop immediately and report: "No actionable bugs found — all identified issues are below the severity threshold."

## What counts as a bug

**In code and config:** a defect with real consequence — crash, data loss, incorrect output, resource leak, silent failure, wrong or conditionally-wrong metric, or a misleading/confusing operator-facing log on a reachable path.

**In docs:** wrong or missing information that would leave a developer confused, cause a command to fail, or lead to misconfiguration. A doc gap counts if a developer following the docs would end up in the wrong place.

When code and docs conflict, the code is the source of truth.

## Steps

1. **Parse the count.**

   Read the argument. If it is a positive integer, that is `N` (the number of bugs to fix). If omitted or not a number, `N = 1`.

2. **Understand the codebase.**

   Read `README.md` and `AGENTS.md` to orient yourself. Then read every source file in the scoped directories to build a complete picture of the system.

3. **Find up to N bugs.**

   Scan the entire scope and collect all bugs that clear the severity bar. Rank them:

   - Crashes or unhandled exceptions in hot paths
   - Silent data loss or incorrect behavior on a reachable path
   - Resource leaks that accumulate in normal operation
   - Logic errors that produce wrong results

   Take the top N. If fewer than N exist, note the shortfall — you will report it at the end.

4. **For each bug (in ranked order):**

   **a. Fully understand it before touching anything.**

   Trace the execution path from entry point to failure. Understand what the correct behavior should be. If a fix requires a third-party SDK or library, search the codebase for existing usage and read the relevant stubs to confirm the correct API.

   **b. Fix it.**

   Make the smallest change that corrects the bug. Do not refactor surrounding code, add comments, or clean up unrelated issues.

   **c. Verify the fix.**

   Re-read the changed file(s). Confirm the fix is correct and nothing adjacent was broken. If tests exist, run them:

   ```bash
   cd <repo-root> && python -m pytest -v
   ```

   **d. Commit and push.**

   Stage only the files changed by this fix and commit:

   ```bash
   git add <changed files>
   git commit -m "Fix <short description>"
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

5. **Report.**

   One paragraph per bug fixed: what the bug was, where it was, what the fix was, and why it ranked where it did. If fewer bugs were found than requested, state how many were found and fixed.

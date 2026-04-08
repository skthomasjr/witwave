---
name: fix-top-risk
description: Find and fix the top N risks in the codebase, commit, and push — no issues, no ceremony
argument-hint: "[N]"
---

Find and fix the top risks in the codebase, commit each fix, and push.

No GitHub issues. No comments. No tracking. Just find them, fix them, ship them.

**This skill targets things that work correctly today but are fragile or dangerous** — code that will fail, hang, leak, or break under conditions that could reasonably occur in production. If the code is already producing wrong results or has crashed, use `/fix-top-bug` instead.

## Argument

The optional argument is a number — how many risks to fix in this run. Default is `1` if no argument is given.

Examples: `/fix-top-risk`, `/fix-top-risk 5`, `/fix-top-risk 20`

If fewer risks exist than the requested count, fix all that exist and report the shortfall.

## Scope

Search only these locations — nothing else:

- `agent/` — nyx-agent infrastructure (router, scheduler, bus, backends)
- `a2-claude/` — Claude backend server
- `a2-codex/` — Codex backend server
- `ui/` — web UI (future container)
- Top-level config files: `docker-compose.active.yml`, `docker-compose.test.yml`
- Top-level docs: `README.md`, `AGENTS.md`, `CLAUDE.md`

Do **not** look in `.agents/` or `.claude/`. Agent-specific configuration and AI prompts are out of scope.

## The risk bar

Before fixing anything, ask: **could this cause harm — a security breach, a reliability failure, or a code-quality problem that will silently break something?** It must be something that works today but is fragile or dangerous.

**Fix it** — has real potential for harm:

**Reliability:**
- Missing timeout on a network call or subprocess that could hang forever
- Unbounded growth — a queue, list, cache, or log that grows without limit in normal use
- A failure in one component that silently cascades to others with no isolation
- A startup or shutdown sequence that could leave the system in a broken state
- Shared mutable state accessed concurrently without protection

**Security:**
- Credentials, tokens, or secrets logged or exposed in error messages
- Input from external sources used without validation that could allow injection or path traversal
- Insecure defaults — open endpoints with no auth, world-readable secrets

**Code quality:**
- Logic correct today but will silently break if a dependency changes behavior
- Missing validation at a system boundary that allows bad state to propagate inward
- Dead code or files that imply a capability the system doesn't have

**Do not fix** — stop and report "no actionable risks found":
- Risks that only trigger under deliberate misconfiguration or adversarial input beyond normal use
- Theoretical risks with no realistic trigger path

If nothing clears the bar, stop immediately and report: "No actionable risks found — all identified issues are below the risk threshold."

## What counts as a risk

A defect that works correctly today but could cause a security breach, reliability failure, data loss, or silent incorrect behavior under conditions that could reasonably occur in production. The closer to a normal operating path, the higher the risk.

## Steps

1. **Parse the count.**

   Read the argument. If it is a positive integer, that is `N` (the number of risks to fix). If omitted or not a number, `N = 1`.

2. **Understand the codebase.**

   Read `README.md` and `AGENTS.md` to orient yourself. Then read every source file in the scoped directories to build a complete picture of the system.

3. **Find up to N risks.**

   Scan the entire scope and collect all risks that clear the risk bar. Rank them:

   - Security risks (credentials exposed, input injection, insecure defaults)
   - Reliability risks (hangs, unbounded growth, cascading failures, broken shutdown)
   - Code-quality risks (silent breakage on dependency change, bad-state propagation)

   Take the top N. If fewer than N exist, note the shortfall — you will report it at the end.

4. **For each risk (in ranked order):**

   **a. Fully understand it before touching anything.**

   Trace the execution path from entry point to the risk. Understand what the correct behavior should be. If a fix requires a third-party SDK or library, search the codebase for existing usage and read the relevant stubs to confirm the correct API.

   **b. Fix it.**

   Make the smallest change that eliminates the risk. Do not refactor surrounding code, add comments, or fix unrelated issues.

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

   One paragraph per risk fixed: what the risk was, where it was, what the fix was, and why it ranked where it did. If fewer risks were found than requested, state how many were found and fixed.

---
name: fix-top-bug
description: Find the single most impactful bug in the codebase, fix it, commit, and push — no issues, no ceremony
argument-hint: ""
---

Find the single most impactful bug in the codebase, fix it, commit, and push.

No GitHub issues. No comments. No tracking. Just find it, fix it, ship it.

**This skill targets things that are already broken** — code producing wrong results, crashes, misleading output, missing docs. If the code works correctly today but is fragile or dangerous under future conditions, use `/fix-top-risk` instead.

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

1. **Understand the codebase.**

   Read `README.md` and `AGENTS.md` to orient yourself. Then read every source file in the scoped directories to build a complete picture of the system.

2. **Find the top bug.**

   Prioritize in this order:

   - Crashes or unhandled exceptions in hot paths
   - Silent data loss or incorrect behavior on a reachable path
   - Resource leaks that accumulate in normal operation
   - Logic errors that produce wrong results

   Apply the severity bar. If nothing clears it, stop and report "no actionable bugs found."

3. **Fully understand it before touching anything.**

   Trace the execution path from entry point to failure. Understand what the correct behavior should be. If a fix requires a third-party SDK or library, search the codebase for existing usage and read the relevant stubs to confirm the correct API.

4. **Fix it.**

   Make the smallest change that corrects the bug. Do not refactor surrounding code, add comments, or clean up unrelated issues.

5. **Verify the fix.**

   Re-read the changed file(s). Confirm the fix is correct and nothing adjacent was broken. If tests exist, run them:

   ```bash
   cd <repo-root> && python -m pytest -v
   ```

6. **Commit and push.**

   Stage only the changed files and commit with a concise message:

   ```bash
   git add <changed files>
   git commit -m "Fix <short description>"
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

7. **Report.**

   One paragraph: what the bug was, where it was, what the fix was, and why it was the top pick.

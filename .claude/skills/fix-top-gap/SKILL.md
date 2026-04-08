---
name: fix-top-gap
description: Find the single most valuable enhancement gap in the codebase, fix it, commit, and push — no issues, no ceremony
argument-hint: ""
---

Find the single most valuable enhancement gap in the codebase, fix it, commit, and push.

No GitHub issues. No comments. No tracking. Just find it, fix it, ship it.

## Scope

Search only these locations — nothing else:

- `agent/` — nyx-agent infrastructure (router, scheduler, bus, backends)
- `a2-claude/` — Claude backend server
- `a2-codex/` — Codex backend server
- `ui/` — web UI (future container)
- Top-level config files: `docker-compose.active.yml`, `docker-compose.test.yml`
- Top-level docs: `README.md`, `AGENTS.md`, `CLAUDE.md`

Do **not** look in `.agents/` or `.claude/`. Agent-specific configuration and AI prompts are out of scope.

## What counts as a gap

A gap is something that **works but could be meaningfully better** — not a bug, not a style issue, not a hypothetical future feature. It must be a concrete improvement to code that is already running.

**Worth fixing** — delivers real value in a running system:
- Dead code, unused imports, or unreachable paths cluttering the codebase
- Duplicated logic in two or more places that should be consolidated
- A missing Prometheus metric on a critical path where behavior is currently unobservable
- A missing or misleading log message that would leave an operator guessing during an incident
- A hardcoded value that should be an environment variable
- Inconsistent error handling across similar components (one logs, one silently swallows)
- A background task or scheduled job with no health or liveness signal

**Not worth fixing** — stop and report "no actionable gaps found":
- Style, naming, or formatting preferences
- Things that would only matter for hypothetical future features
- Refactors that change structure without improving behavior or observability
- Anything that requires significant redesign — gaps should be fixable in a small, self-contained change

If nothing clears the bar, stop immediately and report: "No actionable gaps found."

## The value bar

Pick the gap where fixing it delivers the most **observable improvement** — better debuggability in production, reduced risk of a future bug, or elimination of real cognitive overhead. A gap that makes an incident 10x faster to diagnose beats five minor cleanups.

## Steps

1. **Understand the codebase.**

   Read `README.md` and `AGENTS.md` to orient yourself. Then read every source file in the scoped directories to build a complete picture of the system.

2. **Find the top gap.**

   Look for:

   - Missing observability on critical paths (metrics, logs, health signals)
   - Duplicated logic that should be consolidated
   - Dead code or unused imports
   - Hardcoded values that should be configurable
   - Inconsistent patterns across similar components
   - Missing error handling at system boundaries (external calls, file I/O, network)

   Pick the single most valuable one. Apply the value bar. If nothing clears it, stop and report "no actionable gaps found."

3. **Fully understand it before touching anything.**

   Trace the code paths involved. Understand what the correct improvement looks like and that it won't break anything adjacent.

4. **Fix it.**

   Make the smallest change that delivers the improvement. Do not refactor surrounding code or fix unrelated issues.

5. **Verify the fix.**

   Re-read the changed file(s). Confirm the change is correct and nothing adjacent was broken. If tests exist, run them:

   ```bash
   cd <repo-root> && python -m pytest -v
   ```

6. **Commit and push.**

   Stage only the changed files and commit with a concise message:

   ```bash
   git add <changed files>
   git commit -m "Improve <short description>"
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

7. **Report.**

   One paragraph: what the gap was, where it was, what the fix was, and why it was the top pick.

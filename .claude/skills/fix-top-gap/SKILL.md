---
name: fix-top-gap
description: Find and fix the top N enhancement gaps in the codebase, commit, and push — no issues, no ceremony
argument-hint: "[N]"
---

Find and fix the top enhancement gaps in the codebase, commit each fix, and push.

No GitHub issues. No comments. No tracking. Just find them, fix them, ship them.

## Argument

The optional argument is a number — how many gaps to fix in this run. Default is `1` if no argument is given.

Examples: `/fix-top-gap`, `/fix-top-gap 5`, `/fix-top-gap 20`

If fewer gaps exist than the requested count, fix all that exist and report the shortfall.

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

Pick the gaps where fixing them delivers the most **observable improvement** — better debuggability in production, reduced risk of a future bug, or elimination of real cognitive overhead. A gap that makes an incident 10x faster to diagnose beats five minor cleanups.

## Steps

1. **Parse the count.**

   Read the argument. If it is a positive integer, that is `N` (the number of gaps to fix). If omitted or not a number, `N = 1`.

2. **Understand the codebase.**

   Read `README.md` and `AGENTS.md` to orient yourself. Then read every source file in the scoped directories to build a complete picture of the system.

3. **Find up to N gaps.**

   Scan the entire scope and collect all gaps that clear the value bar. Rank by observable improvement delivered:

   - Missing observability on critical paths (metrics, logs, health signals)
   - Duplicated logic that should be consolidated
   - Dead code or unused imports
   - Hardcoded values that should be configurable
   - Inconsistent patterns across similar components
   - Missing error handling at system boundaries (external calls, file I/O, network)

   Take the top N. If fewer than N exist, note the shortfall — you will report it at the end.

4. **For each gap (in ranked order):**

   **a. Fully understand it before touching anything.**

   Trace the code paths involved. Understand what the correct improvement looks like and that it won't break anything adjacent.

   **b. Fix it.**

   Make the smallest change that delivers the improvement. Do not refactor surrounding code or fix unrelated issues.

   **c. Verify the fix.**

   Re-read the changed file(s). Confirm the change is correct and nothing adjacent was broken. If tests exist, run them:

   ```bash
   cd <repo-root> && python -m pytest -v
   ```

   **d. Commit and push.**

   Stage only the files changed by this fix and commit:

   ```bash
   git add <changed files>
   git commit -m "Improve <short description>"
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

5. **Report.**

   One paragraph per gap fixed: what the gap was, where it was, what the fix was, and why it ranked where it did. If fewer gaps were found than requested, state how many were found and fixed.

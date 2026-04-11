---
name: bug-discovery
description: Analyze one or all components of the autonomous-agent platform for bugs and record findings as tracked issues.
version: 1.0.0
---

# bug-discovery

Analyze a specific component of the autonomous-agent platform for bugs.

## Instructions

The user will invoke this skill with a component name, e.g.:
- `/bug-discovery claude backend`
- `/bug-discovery orchestrator`
- `/bug-discovery codex`
- `/bug-discovery ui`

**Step 1: Identify the component(s).**

Read the Components table in `/Users/scott/Source/github.com/skthomasjr/autonomous-agent/README.md` to determine which directory corresponds to what the user said. Map natural language to the table — "Claude backend", "Claude agent", or just "Claude" all map to `a2-claude/`. "Orchestrator", "nyx", or "router" map to `agent/`. "UI", "interface", or "frontend" map to `ui/`.

If the user specifies "all" or does not specify a component, run this skill against every component in the Components table in sequence. Complete all steps for each component before moving to the next.

**Step 2: Understand the component's role in the overall architecture.**

Read the root `README.md` and the component's own `README.md` to understand what this component does, what calls it, what it calls, and how it fits into the overall system. Then read `AGENTS.md` for any additional architectural context. This understanding is required before analyzing code — bugs in isolation are often not bugs at all, and bugs that matter are the ones that break integration points. Pay particular attention to shared code and utilities called by multiple components, as bugs there have wider blast radius.

**Step 3: Read all source files in that directory.**

Read all source files in the identified directory. Do not skip any file.

**Step 4: Analyze for bugs.**

Always perform a full, independent analysis of the source — do not assume that previously filed issues represent all known bugs. Every invocation of this skill is a fresh evaluation.

Focus exclusively on real bugs — not style, not improvements, not missing features. Look for:

- Logic errors (wrong conditionals, off-by-one, incorrect operator)
- Race conditions or concurrency issues (async/await misuse, shared state without locking)
- Error handling gaps (exceptions swallowed silently, missing cleanup on failure paths)
- Resource leaks (file handles, connections, or processes not closed)
- Incorrect assumptions about external data (missing None checks, wrong field names, type mismatches)
- Security issues (injection, unvalidated input, insecure defaults)

**Step 5: Report findings.**

For each bug found, report:
- File and line number
- What the bug is
- Why it's a bug (what goes wrong)
- A suggested fix

For each bug found, record it as a tracked issue. Include the component, file and line number, what the bug is, what goes wrong at runtime, and the suggested fix. Mark each issue as pending. Do not fix the bug — only report and record it.

If a sufficiently similar issue already exists, do not file a duplicate. Instead, add a comment to the existing issue noting that the bug has been re-identified. The comment must include:
- The agent of record that re-identified it
- The name and version of this skill (see the frontmatter of this file)

Report bugs in descending order of severity — data loss, crashes, and security issues first; logic errors and incorrect behavior next; resource leaks and edge cases last.

If no bugs are found, say so clearly. Do not pad the output with non-bugs.

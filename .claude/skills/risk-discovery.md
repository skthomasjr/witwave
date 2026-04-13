---
name: risk-discovery
description: Analyze one or all components of the autonomous-agent platform for risks and record findings as tracked risks. Trigger when the user says "find risks", "discover risks", "look for risks", "scan for risks", "search for risks", or "run risk discovery" — with or without a component name.
version: 1.0.0
---

# risk-discovery

Analyze a specific component of the autonomous-agent platform for risks.

## Instructions

**Step 1: Identify the component(s).**

Read the Components table in `<repo-root>/README.md` to determine which directory corresponds to what the user said. Map natural language to the table — "Claude backend", "Claude agent", or just "Claude" all map to `a2-claude/`. "Orchestrator", "nyx", or "router" map to `agent/`. "UI", "interface", or "frontend" map to `ui/`.

If the user specifies "all" or does not specify a component, run this skill against every component in the Components table in sequence. Complete all steps for each component before moving to the next.

**Step 2: Understand the component's role in the overall architecture.**

Read the root `README.md` and the component's own `README.md` to understand what this component does, what calls it, what it calls, and how it fits into the overall system. Then read `AGENTS.md` for any additional architectural context. This understanding is required before analyzing code — risks in isolation are often not risks at all, and risks that matter are the ones that threaten integration points, shared state, or long-running operation. Pay particular attention to shared code and utilities called by multiple components, as risks there have wider blast radius.

**Step 3: Read all source files in that directory.**

Read all source files in the identified directory. Do not skip any file.

**Step 4: Analyze for risks.**

Always perform a full, independent analysis of the source — do not assume that previously filed risks represent all known risks. Every invocation of this skill is a fresh evaluation.

Focus exclusively on real risks — not bugs, not missing features, not style preferences. Look for code that works today but is fragile, insecure, hard to maintain, or likely to break under foreseeable conditions. Categorize each finding as one of:

- **Security** — credentials, secrets, or tokens in code or config; unvalidated external input; insecure defaults; overly permissive access
- **Reliability** — missing timeouts or retries on external calls; no circuit breaking; silent degradation under failure; assumptions about external service availability
- **Maintainability** — deeply coupled logic that makes changes dangerous; duplicated critical logic with no single source of truth; undocumented invariants that future developers are likely to violate
- **Performance** — unbounded growth (memory, queues, log files); blocking calls in async paths; operations that scale poorly with load
- **Observability** — silent failures with no logging or metrics; error paths that swallow context; conditions that would be impossible to diagnose in production

**Step 5: Report findings.**

For each risk found, report:
- File and line number
- Category (security, reliability, maintainability, performance, observability)
- What the risk is
- What condition would cause it to manifest
- What the impact would be if it manifests
- A suggested mitigation

For each risk found, record it as a tracked issue. Every issue **must** be filed with at minimum the labels `risk` and `pending`. Include the component, file and line number, category, condition, impact, and suggested mitigation. Do not mitigate the risk — only report and record it. An issue filed without labels is invalid — verify labels are present after filing.

If a sufficiently similar risk already exists, do not file a duplicate. Instead, add a comment to the existing risk noting that it has been re-identified. The comment must include:
- The agent of record that re-identified it
- The name and version of this skill (see the frontmatter of this file)

Report risks in descending order of severity — security issues first; reliability risks next; then maintainability, performance, and observability.

If no risks are found, say so clearly. Do not pad the output with non-risks.

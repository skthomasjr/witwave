---
name: gap-discover
description: Analyze one or all components of the autonomous-agent platform for gaps and record findings as tracked gaps. Trigger when the user says "find gaps", "discover gaps", "look for gaps", "scan for gaps", "search for gaps", or "run gap discover" — with or without a component name.
version: 1.1.0
---

# gap-discover

Analyze a specific component of the autonomous-agent platform for gaps.

## Instructions

**Step 1: Identify the component(s).**

Read the Components table in `<repo-root>/README.md` to determine which directory corresponds to what the user said. Map natural language to the table — "Claude backend", "Claude agent", or just "Claude" all map to `backends/claude/`. "Orchestrator", "nyx", or "router" map to `agent/`. "UI", "interface", or "frontend" map to `ui/`.

If the user specifies "all" or does not specify a component, run this skill against every component in the Components table in sequence. Complete all steps for each component before moving to the next.

**Step 2: Understand the component's role in the overall architecture.**

Read the root `README.md` and the component's own `README.md` to understand what this component does, what calls it, what it calls, and how it fits into the overall system. Then read `AGENTS.md` for any additional architectural context. This understanding is required before analyzing code — a gap in isolation may not be a real gap at all; gaps that matter are the ones where the system's actual behavior diverges from its intended design, or where a missing piece leaves an integration point, workflow, or behavioral contract unfulfilled.

**Step 3: Read all source files in that directory.**

Read all source files in the identified directory. Do not skip any file.

**Step 4: Analyze for gaps.**

Always perform a full, independent analysis of the source — do not assume that previously filed gaps represent all known gaps. Every invocation of this skill is a fresh evaluation.

Focus exclusively on real gaps — not bugs, not risks, not style preferences. Look for missing capabilities, coverage holes, or unimplemented requirements that leave the system incomplete or inconsistent with its intended design. Categorize each finding as one of:

- **Functionality** — a behavior the system is expected to have but does not; a feature referenced in documentation, architecture, or adjacent code that has no implementation
- **Coverage** — missing tests, metrics, health checks, or validation that leaves a component's correctness or health unverifiable
- **Consistency** — a component that behaves differently from its peers in a way that violates a platform-wide convention or contract (e.g., one backend missing a field that all others provide)
- **Integration** — a missing or incomplete connection between components; a protocol, endpoint, or handoff that is partially implemented or not wired up
- **Observability** — missing logs, metrics, or tracing that would be needed to understand or debug the system's behavior in production

**Step 5: Validate uncertain findings.**

Before reporting, identify any findings where you are not confident the gap is real or where the implementation approach is non-obvious. For those, do a web search to check whether the missing capability is standard practice in the industry and whether the proposed implementation approach is well-understood. A finding is uncertain if any of the following apply:

- It involves an optional or aspirational capability rather than a clearly missing requirement
- It would require significant effort to implement but the necessity is unclear
- You are not sure whether the gap applies to this architecture specifically

If a search confirms the finding is real and well-understood, proceed to file it. If the search shows it is not standard practice or does not apply, drop the finding. Briefly note what you searched for and what you found in the gap write-up when this step influenced the decision.

**Step 6: Report findings.**

For each gap found, report:
- File and line number (or `n/a` if there is no existing file)
- Category (functionality, coverage, consistency, integration, observability)
- What is missing
- What the system should do or have (expected)
- What is currently absent or incomplete (actual)
- A suggested implementation approach

For each gap found, record it as a tracked issue. Every issue **must** be filed with at minimum the labels `gap` and `pending`. Include the component, file and line number, category, expected behavior, actual state, and suggested implementation. Do not implement the gap — only report and record it. An issue filed without labels is invalid — verify labels are present after filing.

If a sufficiently similar gap already exists, do not file a duplicate. Instead, add a comment to the existing gap noting that it has been re-identified. The comment must include:
- The agent of record that re-identified it
- The name and version of this skill (see the frontmatter of this file)

Report gaps in descending order of severity — functionality gaps first; integration gaps next; then consistency, coverage, and observability.

If no gaps are found, say so clearly. Do not pad the output with non-gaps.

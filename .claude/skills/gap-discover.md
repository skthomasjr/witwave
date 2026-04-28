---
name: gap-discover
description: Analyze one or all components of the witwave platform for gaps and record findings as tracked gaps. Trigger when the user says "find gaps", "discover gaps", "look for gaps", "scan for gaps", "search for gaps", or "run gap discover" — with or without a component name.
version: 1.2.0
---

# gap-discover

Analyze a specific component of the witwave platform for gaps.

## Instructions

**Step 1: Identify the component(s).**

Read the Components table in `<repo-root>/README.md` to determine which directory corresponds to what the user said. Map natural language to the table — "Claude backend", "Claude agent", or just "Claude" all map to `backends/claude/`. "Orchestrator", "witwave", or "router" map to `harness/`. "UI", "interface", "dashboard", or "frontend" map to `clients/dashboard/`.

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

**Step 5: Validate each candidate against intentional design before filing.**

The most common failure mode in this skill is filing a candidate that the codebase has either already provided elsewhere or has deliberately scoped out. Before filing each candidate, verify against the patterns below and DROP the candidate if any apply.

- **The capability already exists at a different path.** "Missing X" candidates are often false because X is implemented in a sibling file, a shared module, or a different layer. Before flagging "missing metric for Y" or "missing health check for Z", grep the component's `metrics.py` and `main.py` for the symbol. Before flagging "missing endpoint", grep `main.py` route declarations. Before flagging "missing CLI subcommand", check `clients/ww/cmd/`. The capability may be wired up under a slightly different name than the gap description assumes.
- **The capability is intentionally out of scope.** Several witwave components have explicit non-scope lists. The `echo` backend's README enumerates intentional exclusions (no MCP, no metrics, no persistence, no hooks, no session binding, no three-probe health) — flagging any of those as a gap on echo is a misread. Similar exclusions exist in shared modules (`shared/redact.py` doesn't try to be a generic PII engine; the harness has no LLM by design).
- **The capability is documented as future / aspirational.** AGENTS.md and component READMEs sometimes describe intended behavior that is reserved for a future version (e.g. cross-namespace Workspace bindings in v1alpha1 are documented as future-only). A "gap" against documented future scope isn't a gap — it's a roadmap entry. Check whether AGENTS.md or the relevant CRD types file labels the field as "reserved for v1.x" before flagging.
- **The "missing test" already exists in `tests/`.** Coverage-category gaps are common false positives because the smoke-test suite at `<repo-root>/tests/` is separate from per-component test directories. Check `tests/README.md` and the numbered test specs before flagging "no integration test for X".
- **The "missing observability" already has a metric or log.** Before flagging "no metric for failure mode X", grep the component's `metrics.py` for `_total{reason=...}` patterns and the source for `logger.warning` / `logger.error` lines that already cover the path. The metric may use a slightly different name than the gap description assumes.
- **Consistency-category gaps need the platform contract verified.** Before flagging "backend X is missing field Y that backends A/B have", check whether AGENTS.md's metrics-landscape section explicitly says "claude is the superset; peers track placeholders" — that's the documented contract, not a gap. Same for `/health/start` vs `/health` (echo intentionally ships only `/health` per its non-scope list).

A gap candidate is more likely to be **real** when: the missing capability is described in AGENTS.md or a component README as expected current behavior, no implementation exists under any reasonable name, and no inline comment marks it as future / out-of-scope. A gap candidate is more likely to be **false** when: the candidate would need to be implemented twice (because something equivalent already exists), or the candidate would re-introduce something a non-scope list deliberately excludes.

If any of the above resolve the concern, drop the candidate. Quality over quantity.

**Step 6: Validate uncertain findings.**

Before reporting, identify any findings where you are not confident the gap is real or where the implementation approach is non-obvious. For those, do a web search to check whether the missing capability is standard practice in the industry and whether the proposed implementation approach is well-understood. A finding is uncertain if any of the following apply:

- It involves an optional or aspirational capability rather than a clearly missing requirement
- It would require significant effort to implement but the necessity is unclear
- You are not sure whether the gap applies to this architecture specifically

If a search confirms the finding is real and well-understood, proceed to file it. If the search shows it is not standard practice or does not apply, drop the finding. Briefly note what you searched for and what you found in the gap write-up when this step influenced the decision.

**Step 7: Report findings.**

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

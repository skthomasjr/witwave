---
name: gap-refine
description:
  Analyze pending gaps holistically, identify dependencies and conflicts, update stale information, and produce an
  execution order for implementation. Optionally scoped to a specific component; if no component is specified, processes
  all components including cross-cutting gaps with no component assigned. Trigger when the user says "refine gaps", "run
  gap refine", "analyze gaps", "clean up gaps", "prioritize gaps", or "order gaps for implementation".
version: 1.2.0
---

# gap-refine

Analyze pending gaps as a set — not individually — to identify dependencies, conflicts, redundancies, and stale
information. The output is a clean, ordered, dependency-aware work queue ready for implementation.

## Instructions

**Step 1: Gather all pending gaps.**

The user may optionally specify a component (e.g. "refine gaps for codex") or say "all" to process everything. If no
component is specified, process all pending gaps across all components.

Retrieve all open pending gaps, filtered by component if specified. Read each one in full — component, file, line
number, category, expected behavior, actual state, suggested implementation, and any existing depends-on relationships.

Note: some gaps are cross-cutting and have no component assigned. When processing all components, always include
cross-cutting gaps. When filtering to a specific component, exclude cross-cutting gaps unless the user explicitly asks
to include them.

**Step 2: Verify each gap against the current code.**

Read the **full source file** (or the relevant area of the codebase if no file exists yet), not just the cited line
range, AND grep widely for the supposedly-missing capability under any reasonable name. False-positive gaps survive into
refine when both discover and refine assume the capability is missing because they didn't search broadly enough.

For each gap, confirm:

- The gap still exists — the missing capability has not been added since the gap was filed
- If the code has shifted (refactor, lines moved), update the file and line reference to reflect the current location
- If the surrounding code has changed, re-evaluate whether the expected behavior and implementation approach still apply
  correctly to the current code
- If a partial implementation was already applied, note what remains and update the description accordingly

Then run the same intentional-design checklist that gap-discover Step 5 runs, in case it was missed at filing time.
Specifically verify:

- **The capability really doesn't exist anywhere.** Grep the component's `metrics.py`, `main.py` route declarations,
  sibling files, shared modules, and `clients/ww/cmd/`. The capability may already be implemented under a slightly
  different name than the gap description assumes.
- **The capability isn't in the component's documented non-scope list.** Components like `echo` enumerate intentional
  exclusions in their READMEs; flagging an excluded capability as a gap is a misread.
- **The capability isn't documented as future / aspirational.** AGENTS.md and CRD types files mark some fields as
  "reserved for v1.x"; gaps against documented future scope are roadmap entries, not gaps.
- **No equivalent test exists in `<repo-root>/tests/`.** Coverage gaps are common false positives because the smoke-test
  suite is separate from per-component test directories.
- **No metric/log already covers the supposed observability gap.** Grep `metrics.py` for `_total{reason=...}` patterns
  and the source for `logger.warning`/`logger.error` lines that already cover the supposed silent path.
- **The "consistency" gap isn't a documented contract divergence.** AGENTS.md's metrics-landscape section explicitly
  says "claude is the superset; peers track placeholders" — peers missing a metric claude has is the documented design,
  not a gap.

If any of these resolve the concern, **close the gap as `wont-fix`** with a note explaining where the capability
actually lives (or why it's intentionally excluded). Do not let an invalid gap propagate to approve and implement.

If a gap is no longer present (the capability was actually added since filing), close it with a note explaining what
changed.

**Step 3: Validate uncertain findings.**

Before updating records or identifying relationships, flag any gaps where you are not confident the gap is real, the
severity is correctly assigned, or the implementation approach is sound. For those, do a web search to check whether the
missing capability is standard practice in the industry and whether the proposed implementation is well-understood. A
finding is uncertain if any of the following apply:

- The category or priority feels inconsistent with similar gaps in the set
- The implementation approach is vague, contested, or depends on an approach you are not sure is applicable here
- You are not sure whether the gap represents a real missing requirement given this architecture

If a search confirms the finding and severity, proceed. If the search suggests the priority or category is wrong, update
the record accordingly and note what you found. If the search shows the gap does not apply, close it with a note
explaining why.

**Step 4: Identify relationships across gaps.**

Read all affected files together and look for:

- **Dependencies** — gap A must be implemented before gap B because B's implementation assumes A's implementation is in
  place, or implementing B first would be invalidated or undone by A's implementation
- **Conflicts** — two gaps touch the same code in ways that would cause one implementation to break the other
- **Common root cause** — two or more gaps that stem from the same underlying missing piece and should be implemented
  together in a single change
- **Cascading effect** — implementing one gap that reveals or resolves another gap, or that creates a new gap elsewhere

**Step 5: Update gap records.**

For every gap processed, add a comment documenting:

- Whether the gap was verified, stale, or already partially implemented
- Any changes made to the file, line, expected behavior, or implementation description
- Any dependencies identified and why
- If nothing changed, explicitly state that the record was reviewed by this skill (name and version, see frontmatter)
  and is current as of this run
- The name and version of this skill (see the frontmatter of this file)

For gaps where relationships were found, also:

- Populate the `Depends on` field with any gap numbers that must be resolved first
- Apply the `blocked-by` label to any gap that has dependencies
- Update the `Implementation` field if the suggested approach needs revision based on current code or discovered
  relationships

**Step 6: Produce an execution order.**

Output a recommended implementation order based on dependencies, priority, and category:

- Gaps with no dependencies and high priority come first
- Functionality gaps take precedence over other categories at equal priority
- Blocked gaps come after their dependencies
- Where gaps share a common root cause, group them together
- Note any gaps that should be implemented in the same commit or PR to avoid intermediate incomplete states

The execution order is a recommendation, not a constraint — but it should reflect the most logical, dependency-safe path
through the pending work queue.

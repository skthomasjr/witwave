---
name: gap-approve
description:
  Evaluate pending gaps against the codebase and approve or defer them based on implementation confidence, scope, and
  priority. Optionally scoped to a specific component; if no component is specified, processes all components including
  cross-cutting gaps with no component assigned. Trigger when the user says "approve gaps", "run gap approve", "evaluate
  gaps", "review gaps for approval", or "approve pending gaps".
version: 1.2.0
---

# gap-approve

Evaluate each pending gap by reading the actual code, confirming the gap is real and well-scoped, and approving only
those where there is high confidence the gap exists and the implementation approach is actionable.

## Instructions

**Step 1: Find pending gaps.**

The user may optionally specify a component (e.g. `approve gaps for <component>`) or say "all" to process everything. If
no component is specified, process all pending gaps.

Retrieve all open pending gaps, filtered by component if specified. For each one, read the full gap record — component,
file, line number, category, expected behavior, actual state, and suggested implementation. Also read all comments on
the issue; prior refinement runs may have noted relationships, grouping recommendations, updated implementation
approaches, or caution areas that should inform the approval decision.

Note: some gaps are cross-cutting and have no component assigned. When processing all components, always include
cross-cutting gaps. When filtering to a specific component, exclude cross-cutting gaps unless the user explicitly asks
to include them.

**Step 2: Verify the gap against the current code.**

For each gap, read the specific file and surrounding context identified in the gap record (or the relevant area of the
codebase if no file exists yet). Do not rely solely on the gap description — confirm the expected behavior is genuinely
absent from the current code. Also check whether the gap may have already been implemented. If the gap no longer exists,
close it with a note explaining what changed and stop processing that gap.

**Step 3: Assess implementation scope.**

Evaluate the effort and scope of the suggested implementation:

- What would need to be added or changed? What components or files would be touched?
- Does the implementation involve shared state, external APIs, or cross-cutting concerns?
- Search the codebase for any existing tests or scaffolding that would need to be updated or extended.
- Is the suggested implementation approach correct and complete, or does it only partially close the gap? If the
  suggested approach is wrong or incomplete, identify a better approach and note it.

Rate the implementation scope: **narrow** (self-contained addition, low risk of unintended effects), **moderate**
(touches shared logic or multiple files, needs care), or **broad** (significant new surface area, complex interactions,
or unclear boundaries).

**Step 4: Make an approval decision.**

Apply this logic:

- **Functionality category + any priority** → approve; missing behavior gaps are approved regardless of scope; note any
  caution areas in a comment
- **High priority + any scope** → approve; note any caution areas in a comment
- **Medium priority + narrow or moderate scope** → approve
- **Medium priority + broad scope** → defer; leave a comment explaining the concern
- **Low priority + narrow scope** → approve
- **Low priority + moderate or broad scope** → defer; leave a comment explaining the concern

Only approve when you have high confidence that:

1. The expected behavior described is genuinely absent from the current code
2. The suggested implementation (or a clear alternative) would correctly close the gap
3. The implementation is unlikely to introduce regressions or unintended side effects

If confidence is low for any reason — ambiguous scope, unclear requirements, missing context — defer and explain.

**Step 5: Update each gap's status.**

For approved gaps: remove the `pending` label, add the `approved` label, update the status to `approved`, and add a
comment documenting:

- Confirmation that the gap was verified in the current code (file and line, or area of the codebase)
- The assessed implementation scope and why
- Any caution areas, edge cases, or related code paths the implementer should be aware of
- The name and version of this skill (see the frontmatter of this file)

For deferred gaps: leave the status as `pending` and add a comment explaining:

- Why it was deferred
- What the specific concern is
- What would need to be true to approve it in the future
- The name and version of this skill (see the frontmatter of this file)

Pending is not rejection — a deferred gap remains open and should be re-evaluated as the codebase evolves. An
implementation that is too broad today may become tractable as surrounding code matures, dependencies are resolved, or
related gaps are closed. Do not close deferred gaps.

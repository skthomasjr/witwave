---
name: feature-refinement
description: Analyze pending features holistically, identify dependencies and conflicts, update stale information, and produce an execution order for implementation. Trigger when the user says "refine features", "run feature refinement", "analyze features", "clean up features", "prioritize features", or "order features for implementation".
version: 1.1.1
---

# feature-refinement

Analyze pending features as a set — not individually — to identify dependencies, conflicts, redundancies, and stale information. The output is a clean, ordered, dependency-aware work queue ready for implementation.

## Instructions

**Step 1: Gather all pending features.**

Retrieve all open pending features. Read each one in full — description, value, component, design, request linkage, and any existing depends-on relationships.

---

**Step 2: Verify each feature against the current codebase.**

For each feature, read the relevant area of the codebase and confirm:

- The capability is still missing — it has not been fully or partially implemented since the feature was filed
- If a partial implementation exists, note what remains and update the description accordingly
- If the surrounding code has shifted (refactor, redesign, new components), re-evaluate whether the design approach still applies correctly to the current state of the system
- If the feature has been made redundant by other work, close it with a note explaining what changed

---

**Step 3: Validate uncertain features.**

Before updating records or identifying relationships, flag any features where the design approach is non-obvious, contested, or unclear given the current codebase. For those, do a web search to check whether the approach is standard practice and well-understood.

A feature is uncertain if any of the following apply:

- The design approach is vague or depends on a pattern you are not sure applies to this architecture
- The priority feels inconsistent with similar features in the set
- You are not sure whether the feature is still aligned with the originating request given how the codebase has evolved

If a search confirms the approach is sound, proceed. If it reveals a better approach, update the design accordingly and note what you found. If the search shows the feature no longer applies, close it with a note explaining why.

---

**Step 4: Identify relationships across features.**

Read all relevant source files together and look for:

- **Dependencies** — feature A must be implemented before feature B because B's implementation assumes A is in place, or implementing B first would be undone by A
- **Conflicts** — two features touch the same area of the codebase in ways that would cause one implementation to break the other
- **Common root cause** — two or more features that require the same foundational change and should be implemented together in a single change
- **Request grouping** — features derived from the same request that are closely related and would be best implemented together to avoid delivering a half-finished capability to the requester
- **Cascading effect** — implementing one feature that reveals or resolves another, or that creates a new gap or feature elsewhere

---

**Step 5: Update feature records.**

For every feature processed, add a comment documenting:

- Whether the feature was verified as still needed, already partially implemented, or made redundant
- Any changes made to the description, value, design, or component
- Any dependencies identified and why
- If nothing changed, explicitly state that the record was reviewed and is current as of this run
- The name and version of this skill (see the frontmatter of this file)

For features where relationships were found, also:

- Populate the `Depends on` field with any feature or issue numbers that must be resolved first
- Apply the `blocked-by` label to any feature that has dependencies
- Update the `Design` field if the suggested approach is stale or no longer correct given the current codebase — rewrite it fully rather than appending to it, so implementers have a single authoritative source
- If a conflict was found between two features, update both design fields to resolve it. If the conflict cannot be resolved without major redesign, defer one or both features, remove the `approved` label if present, and add a comment explaining the conflict and the recommended resolution path before implementation can proceed

---

**Step 6: Produce an execution order.**

Output a recommended implementation order based on dependencies, priority, and request grouping:

- Features with no dependencies and high priority come first
- Blocked features come after their dependencies
- Features from the same request that are closely related should be grouped together
- Note any features that should be implemented in the same commit or PR to avoid delivering an incomplete capability

The execution order is a recommendation, not a constraint — but it should reflect the most logical, dependency-safe path through the pending work queue.

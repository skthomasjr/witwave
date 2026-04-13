---
name: feature-approve
description: Evaluate pending features against the codebase and approve or defer them based on implementation confidence, scope, and priority. Trigger when the user says "approve features", "run feature approve", "evaluate features", "review features for approval", or "approve pending features".
version: 1.2.0
---

# feature-approve

Evaluate each pending feature by reading the actual codebase, confirming the capability is still missing and well-scoped, and approving only those where there is high confidence the feature is needed and the implementation approach is actionable.

## Instructions

**Step 1: Find pending features.**

Retrieve all open pending features. For each one, read the full feature record — description, value, component, design, request linkage, and any existing depends-on relationships. Also read all comments on the issue; prior refinement runs may have noted relationships, grouping recommendations, updated design approaches, or caution areas that should inform the approval decision.

---

**Step 2: Verify the feature against the current codebase.**

For each feature, read the relevant area of the codebase. Confirm the capability described is genuinely absent — it has not been fully or partially implemented since the feature was filed. Also check that the feature is still aligned with its originating request; if the request has evolved, re-read it before deciding.

If the feature is no longer needed — because it was implemented, superseded, or the request was withdrawn — close it with a note explaining what changed and stop processing that feature.

---

**Step 3: Assess architectural fit.**

Before evaluating scope, assess whether the feature belongs in this system and fits its design:

- Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` if not already done. Does the feature align with the platform's design philosophy and intended architecture?
- Could the capability be achieved through existing extensibility patterns, or does it require introducing new architectural concepts? If new concepts are needed, is that justified?
- Does the feature's component assignment make sense given how the system is structured? If it's cross-cutting, is that appropriate or does it suggest the design needs rethinking?
- Do the breaking changes and user communication fields in the feature record accurately reflect the real impact? If they are blank or underspecified, fill them in before proceeding.

If the feature is architecturally misaligned or would introduce a design inconsistency that outweighs its value, defer it and explain the concern clearly.

---

**Step 4: Assess implementation scope.**

Evaluate the effort and scope of the suggested design:

- What would need to be added or changed? Which components or files would be touched?
- Does the implementation involve shared state, external APIs, or cross-cutting concerns?
- Search the codebase for any existing scaffolding, tests, or related code that would need to be updated or extended.
- Is the suggested design approach correct and complete, or would it only partially deliver the feature? If the approach is wrong or incomplete, identify a better one and note it.
- If the feature requires new files or new components, identify where they should live and what conventions they should follow.

Rate the implementation scope: **narrow** (self-contained addition, low risk of unintended effects), **moderate** (touches shared logic or multiple files, needs care), or **broad** (significant new surface area, complex interactions, or unclear boundaries).

---

**Step 5: Make an approval decision.**

Apply this logic:

- **High priority + any scope** → approve; note any caution areas in a comment
- **Medium priority + narrow or moderate scope** → approve
- **Medium priority + broad scope** → defer; leave a comment explaining the concern
- **Low priority + narrow scope** → approve
- **Low priority + moderate or broad scope** → defer; leave a comment explaining the concern

Only approve when you have high confidence that:
1. The capability described is genuinely absent from the current codebase
2. The suggested design (or a clear alternative) would correctly deliver the feature
3. The implementation is unlikely to introduce regressions or unintended side effects

If confidence is low for any reason — ambiguous scope, unclear requirements, missing context — defer and explain.

---

**Step 6: Update each feature's status.**

For approved features: update the status to `approved` and add a comment documenting:
- Confirmation that the capability was verified as absent from the current codebase
- The assessed implementation scope and why
- Any caution areas, edge cases, or related code paths the implementer should be aware of
- The name and version of this skill (see the frontmatter of this file)

For deferred features: leave the status as `pending` and add a comment explaining:
- Why it was deferred
- What the specific concern is
- What would need to be true to approve it in the future
- The name and version of this skill (see the frontmatter of this file)

Pending is not rejection — a deferred feature remains open and should be re-evaluated as the codebase evolves. Do not close deferred features.

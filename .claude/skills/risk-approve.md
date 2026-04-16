---
name: risk-approve
description: Evaluate pending risks against the codebase and approve or defer them based on mitigation confidence, blast radius, and priority. Optionally scoped to a specific component; if no component is specified, processes all components including cross-cutting risks with no component assigned. Trigger when the user says "approve risks", "run risk approve", "evaluate risks", "review risks for approval", or "approve pending risks".
version: 1.2.0
---

# risk-approve

Evaluate each pending risk by reading the actual code, assessing mitigation complexity relative to priority and category, and approving only those where there is high confidence the risk is real and the mitigation is actionable.

## Instructions

**Step 1: Find pending risks.**

The user may optionally specify a component (e.g. `approve risks for <component>`) or say "all" to process everything. If no component is specified, process all pending risks.

Retrieve all open pending risks, filtered by component if specified. For each one, read the full risk record — component, file, line number, category, condition, impact, and suggested mitigation. Also read all comments on the issue; prior refinement runs may have noted relationships, grouping recommendations, updated mitigations, or caution areas that should inform the approval decision.

Note: some risks are cross-cutting and have no component assigned. When processing all components, always include cross-cutting risks. When filtering to a specific component, exclude cross-cutting risks unless the user explicitly asks to include them.

**Step 2: Read the affected code.**

For each risk, read the specific file and surrounding context identified in the risk record. Do not rely solely on the risk description — verify the condition can actually occur in the current code. Also check whether the mitigation may have already been applied. If the risk no longer exists, close it with a note explaining what changed and stop processing that risk.

**Step 3: Analyze mitigation risk.**

Trace the blast radius of the suggested mitigation:

- What does the affected code touch? What calls it, and what does it call?
- Does the mitigation involve shared state, concurrency, external APIs, or data that persists across requests?
- Search the codebase for tests covering the affected path. If tests exist, assess whether the mitigation would require updating them.
- Could the mitigation introduce a regression in a related code path?
- Is the suggested mitigation correct, or does it address the symptom but not the root cause? If the suggested mitigation is wrong or incomplete, identify a better approach and note it.

Rate the mitigation risk: **low** (isolated change, no side effects), **medium** (touches shared logic, needs care), or **high** (broad impact, complex interactions, or unclear correctness).

**Step 4: Make an approval decision.**

Apply this logic:

- **Security category + any priority** → approve; security risks are approved regardless of mitigation risk; note any caution areas in a comment
- **High priority + any mitigation risk** → approve; note any caution areas in a comment
- **Medium priority + low or medium mitigation risk** → approve
- **Medium priority + high mitigation risk** → defer; leave a comment explaining the concern
- **Low priority + low mitigation risk** → approve
- **Low priority + medium or high mitigation risk** → defer; leave a comment explaining the concern

Only approve when you have high confidence that:
1. The condition described can actually occur given the current code and deployment context
2. The suggested mitigation (or a clear alternative) addresses it correctly
3. The mitigation is unlikely to cause a regression

If confidence is low for any reason — ambiguous mitigation, unclear blast radius, missing context — defer and explain.

**Step 5: Update each risk's status.**

For approved risks: remove the `pending` label, add the `approved` label, update the status to `approved`, and add a comment documenting:
- Confirmation that the risk was verified in the current code (file and line)
- The assessed mitigation risk and why
- Any caution areas, edge cases, or related code paths the implementer should be aware of
- The name and version of this skill (see the frontmatter of this file)

For deferred risks: leave the status as `pending` and add a comment explaining:
- Why it was deferred
- What the specific concern is
- What would need to be true to approve it in the future
- The name and version of this skill (see the frontmatter of this file)

Pending is not rejection — a deferred risk remains open and should be re-evaluated as the codebase evolves. A mitigation that is too risky today may become safe as surrounding code matures, dependencies change, or related risks are resolved. Do not close deferred risks.

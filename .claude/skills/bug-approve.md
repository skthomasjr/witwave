---
name: bug-approve
description: Evaluate pending bugs against the codebase and approve or defer them based on fix confidence, risk, and priority. Optionally scoped to a specific component; if no component is specified, processes all components including cross-cutting bugs with no component assigned. Trigger when the user says "approve bugs", "run bug approve", "evaluate bugs", "review bugs for approval", or "approve pending bugs".
version: 1.1.0
---

# bug-approve

Evaluate each pending bug by reading the actual code, assessing fix risk relative to priority, and approving only those where there is high confidence the fix can be made safely.

## Instructions

**Step 1: Find pending bugs.**

The user may optionally specify a component (e.g. `approve bugs for <component>`) or say "all" to process everything. If no component is specified, process all pending bugs.

Retrieve all open pending bugs, filtered by component if specified. For each one, read the full bug record — component, file, line number, description, expected/actual behavior, and suggested fix. Also read all comments on the issue; prior refinement runs may have noted relationships, grouping recommendations, updated fixes, or caution areas that should inform the approval decision.

Note: some bugs are cross-cutting and have no component assigned. When processing all components, always include cross-cutting bugs. When filtering to a specific component, exclude cross-cutting bugs unless the user explicitly asks to include them.

**Step 2: Read the affected code.**

For each bug, read the specific file and surrounding context identified in the bug record. Do not rely solely on the bug description — verify the issue exists in the current code. Also check whether the fix may have already been applied (the bug description may still match the code superficially, but the actual defect may be gone). If the bug no longer exists, close the bug with a note explaining what changed and stop processing that bug.

**Step 3: Analyze fix risk.**

Trace the blast radius of the suggested fix:

- What does the affected code touch? What calls it, and what does it call?
- Does the fix involve shared state, concurrency, external APIs, or data that persists across requests?
- Search the codebase for tests covering the affected path. If tests exist, assess whether the fix would require updating them.
- Could the fix introduce a regression in a related code path?
- Is the suggested fix correct, or does it address the symptom but not the root cause? If the suggested fix is wrong or incomplete, identify a better approach and note it.

Rate the fix risk: **low** (isolated change, no side effects), **medium** (touches shared logic, needs care), or **high** (broad impact, complex interactions, or unclear correctness).

**Step 4: Make an approval decision.**

Apply this logic:

- **High priority + any risk** → approve; note any caution areas in a comment
- **Medium priority + low or medium risk** → approve
- **Medium priority + high risk** → defer; leave a comment explaining the concern
- **Low priority + low risk** → approve
- **Low priority + medium or high risk** → defer; leave a comment explaining the concern

Only approve when you have high confidence that:
1. The bug is real and present in the current code
2. The suggested fix (or a clear alternative) addresses it correctly
3. The fix is unlikely to cause a regression

If confidence is low for any reason — ambiguous fix, unclear blast radius, missing context — defer and explain.

**Step 5: Update each bug's status.**

For approved bugs: update the status to approved and add a comment documenting:
- Confirmation that the bug was verified in the current code (file and line)
- The assessed fix risk and why
- Any caution areas, edge cases, or related code paths the fixer should be aware of
- The name and version of this skill (see the frontmatter of this file)

For deferred bugs: leave the status as pending and add a comment explaining:
- Why it was deferred
- What the specific risk or concern is
- What would need to be true to approve it in the future
- The name and version of this skill (see the frontmatter of this file)

Pending is not rejection — a deferred bug remains open and should be re-evaluated as the codebase evolves. A fix that is too risky today may become safe as surrounding code matures, dependencies change, or related bugs are resolved. Do not close deferred bugs.

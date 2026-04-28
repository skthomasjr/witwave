---
name: bug-refine
description: Analyze pending bugs holistically, identify dependencies and conflicts, update stale information, and produce an execution order for fixing. Optionally scoped to a specific component; if no component is specified, processes all components including cross-cutting bugs with no component assigned. Trigger when the user says "refine bugs", "run bug refine", "analyze bugs", "clean up bugs", "prioritize bugs", or "order bugs for fixing".
version: 1.2.0
---

# bug-refine

Analyze pending bugs as a set — not individually — to identify dependencies, conflicts, redundancies, and stale information. The output is a clean, ordered, dependency-aware work queue ready for fixing.

## Instructions

**Step 1: Gather all pending bugs.**

The user may optionally specify a component (e.g. "refine bugs for codex") or say "all" to process everything. If no component is specified, process all pending bugs across all components.

Retrieve all open pending bugs, filtered by component if specified. Read each one in full — component, file, line number, description, suggested fix, and any existing depends-on relationships.

Note: some bugs are cross-cutting and have no component assigned. When processing all components, always include cross-cutting bugs. When filtering to a specific component, exclude cross-cutting bugs unless the user explicitly asks to include them.

**Step 2: Verify each bug against the current code.**

Read the **full source file**, not just the cited line range. Most false-positive bugs survive into refine because both discover and refine focused only on the cited lines and missed surrounding context that already addresses the concern.

For each bug, confirm:
- The bug still exists at the referenced location
- If the code has shifted (refactor, lines moved), update the file and line reference to reflect the current location
- If the surrounding code has changed, re-evaluate whether the suggested fix still applies correctly to the current code — not just whether the bug is still present
- If a partial fix was already applied, note what remains and update the description accordingly

Then run the same intentional-design checklist that bug-discover Step 5 runs, in case it was missed at filing time. Specifically verify:

- **No inline comment within ~10 lines** of the cited code documents the choice as intentional or references a prior issue (`#NNNN`) that already resolved it
- **No adjacent existing handler** (within 5-10 lines before/after) already covers the failure mode the bug describes
- **No lock / synchronization / single-threaded constraint** already eliminates the race the bug describes
- **No defensive check earlier on the call path** already validates the input the bug claims is unvalidated
- **The "silent failure" the bug describes isn't intentional** per a comment or surrounding context
- **The "double X" the bug describes isn't idempotent** in the underlying API

If any of these resolve the concern, **close the bug as `wont-fix`** with a note explaining what the discovery framing missed. Do not let an invalid bug propagate to approve and implement — that wastes effort across both downstream phases.

If a bug is no longer present (the code was actually fixed since filing), close it with a note explaining what changed.

**Step 3: Identify relationships across bugs.**

Read all affected files together and look for:

- **Dependencies** — bug A must be fixed before bug B because B's fix assumes A's fix is in place, or fixing B first would be invalidated by A's fix
- **Conflicts** — two bugs touch the same code in ways that would cause one fix to break the other
- **Common root cause** — two or more bugs that stem from the same underlying issue and should be fixed together in a single change
- **Cascading risk** — a fix for one bug that increases or decreases the risk profile of another

**Step 4: Update bug records.**

For every bug processed, add a comment documenting:
- Whether the bug was verified, stale, or already partially fixed
- Any changes made to the file, line, or fix description
- Any dependencies identified and why
- If nothing changed, explicitly state that the record was reviewed by this skill (name and version, see frontmatter) and is current as of this run
- The name and version of this skill (see the frontmatter of this file)

For bugs where relationships were found, also:
- Populate the `Depends on` field with any bug numbers that must be resolved first
- Apply the `blocked-by` label to any bug that has dependencies
- Update the `Fix` field if the suggested fix needs revision based on current code or discovered relationships

**Step 5: Produce an execution order.**

Output a recommended fix order based on dependencies, priority, and risk:
- Bugs with no dependencies and high priority come first
- Blocked bugs come after their dependencies
- Where bugs share a common root cause, group them together
- Note any bugs that should be fixed in the same commit or PR to avoid intermediate broken states

The execution order is a recommendation, not a constraint — but it should reflect the safest, most efficient path through the approved work queue.

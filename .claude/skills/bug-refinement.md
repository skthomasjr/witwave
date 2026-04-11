---
name: bug-refinement
description: Analyze all pending bugs holistically, identify dependencies and conflicts, update stale information, and produce an execution order for fixing.
version: 1.0.0
---

# bug-refinement

Analyze approved bugs as a set — not individually — to identify dependencies, conflicts, redundancies, and stale information. The output is a clean, ordered, dependency-aware work queue ready for fixing.

## Instructions

**Step 1: Gather all pending bugs.**

Retrieve all open bugs in the pending state. Read each one in full — component, file, line number, description, suggested fix, and any existing depends-on relationships.

**Step 2: Verify each bug against the current code.**

For each bug, read the affected file and confirm:
- The bug still exists at the referenced location
- If the code has shifted (refactor, lines moved), update the file and line reference to reflect the current location
- If the surrounding code has changed, re-evaluate whether the suggested fix still applies correctly to the current code — not just whether the bug is still present
- If a partial fix was already applied, note what remains and update the description accordingly

If a bug is no longer present, close it with a note explaining what changed.

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
- Populate the `Depends on` field with any issue numbers that must be resolved first
- Apply the `blocked-by` label to any bug that has dependencies
- Update the `Fix` field if the suggested fix needs revision based on current code or discovered relationships

**Step 5: Produce an execution order.**

Output a recommended fix order based on dependencies, priority, and risk:
- Bugs with no dependencies and high priority come first
- Blocked bugs come after their dependencies
- Where bugs share a common root cause, group them together
- Note any bugs that should be fixed in the same commit or PR to avoid intermediate broken states

The execution order is a recommendation, not a constraint — but it should reflect the safest, most efficient path through the approved work queue.

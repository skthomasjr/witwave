---
name: gap-implement
description: Implement all approved gaps one at a time, committing and pushing each implementation. Trigger when the user says "implement gaps", "fix gaps", "implement approved gaps", "fix approved gaps", "run gap implement", or "start gap implementation".
version: 1.1.0
---

# gap-implement

Implement all approved gaps, one at a time. Each gap is fully implemented, verified, committed, and pushed before moving to the next.

## Instructions

Repeat the following steps until all approved gaps have been processed.

**Step 1: Select the next gap to implement.**

First, check for any open gaps labeled `gap` and `approved` whose status is `in-progress`. If one exists, resume it — it was already selected and started in a previous run. Do not start a new gap until the in-progress one is resolved.

If no in-progress gap exists, retrieve all open gaps labeled `gap` and `approved`. If none remain, stop — all approved gaps have been implemented.

From the approved gaps, select the one to implement next using this priority order:
- Gaps with no `blocked-by` dependencies come before blocked gaps
- Among unblocked gaps: functionality category before other categories; then high priority before medium before low
- Among equal priority: prefer the gap with the most self-contained implementation (narrowest scope)
- Among equal scope: prefer the gap whose suggested implementation is most clearly correct

Once selected, remove the `approved` label, add the `in-progress` label, and update the gap status to `in-progress`.

**Step 2: Understand the affected code.**

Read the full source file(s) identified in the gap record — not just the affected lines. Understand what calls the affected code and what it calls. If the gap touches shared utilities or cross-cutting concerns, read those files too. If no existing file is identified, understand the area of the codebase where the implementation will live.

**Step 3: Research if needed.**

If the implementation approach involves an unfamiliar pattern, API, or library — for example, the gap requires wiring up an integration, implementing a protocol, or using a SDK feature you have not used before — do a targeted web search before writing code. Search for the specific pattern or API in question. If a search confirms the right approach, proceed. If the search reveals a better or more complete implementation, note what you found and adjust the approach accordingly.

**Step 4: Apply the implementation.**

Implement the missing capability. Use the suggested implementation from the gap record unless your code review reveals a better or more correct approach. If you deviate, note why.

Do not implement more than one gap per commit. Do not make unrelated changes.

**Step 5: Verify the implementation.**

Re-read the changed code. Confirm:
- The expected behavior described in the gap record is now present
- The implementation does not introduce a regression in adjacent code paths
- The implementation is complete — no half-measures or TODOs left behind

If the implementation is wrong or incomplete, revise it before continuing.

**Step 6: Commit and push.**

Stage only the files changed for this gap. Write a commit message that describes what was implemented and why, referencing the gap number. Push the commit.

**Step 7: Close the gap.**

Leave a comment on the gap documenting:
- What was added and where (file and line)
- Whether the implementation followed the suggested approach or deviated, and why
- Confirmation that the implementation was verified
- The name and version of this skill (see the frontmatter of this file)

Then close the gap.

Return to Step 1.

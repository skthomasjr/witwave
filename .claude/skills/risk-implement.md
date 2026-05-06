---
name: risk-implement
description:
  Mitigate all approved risks one at a time, committing and pushing each mitigation. Trigger when the user says
  "implement risks", "fix risks", "mitigate risks", "implement approved risks", "fix approved risks", "mitigate approved
  risks", "run risk implement", or "start risk mitigation".
version: 1.2.0
---

# risk-implement

Mitigate all approved risks, one at a time. Each risk is fully mitigated, verified, committed, and pushed before moving
to the next.

## Instructions

Repeat the following steps until all approved risks have been processed.

**Step 1: Select the next risk to mitigate.**

First, check for any open risks labeled `risk` and `approved` whose status is `in-progress`. If one exists, resume it —
it was already selected and started in a previous run. Do not start a new risk until the in-progress one is resolved.

If no in-progress risk exists, retrieve all open risks labeled `risk` and `approved`. If none remain, stop — all
approved risks have been mitigated.

From the approved risks, select the one to mitigate next using this priority order:

- Risks with no `blocked-by` dependencies come before blocked risks
- Among unblocked risks: security category before other categories; then high priority before medium before low
- Among equal priority: prefer the risk with the most isolated mitigation (lowest blast radius)
- Among equal blast radius: prefer the risk whose suggested mitigation is most clearly correct

Once selected, remove the `approved` label, add the `in-progress` label, and update the risk status to `in-progress`.

**Step 2: Understand the affected code.**

Read the full source file(s) identified in the risk record — not just the affected lines. Understand what calls the
affected code and what it calls. If the risk touches shared utilities or cross-cutting logic, read those files too.

**Step 3: Research if needed.**

If the mitigation approach is unfamiliar or its correctness is uncertain — for example, the mitigation involves a
security pattern, concurrency primitive, or library API you are not confident about — do a targeted web search before
writing code. Search for the specific pattern or technique in question. If a search confirms the approach is sound and
standard, proceed. If the search reveals a better or more correct mitigation, note what you found and adjust
accordingly.

**Step 4: Apply the mitigation.**

Apply the mitigation. Use the suggested mitigation from the risk record unless your code review reveals a better or more
correct approach. If you deviate, note why.

Do not mitigate more than one risk per commit. Do not make unrelated changes.

**Step 5: Verify the mitigation.**

Re-read the changed code. Confirm:

- The condition described in the risk record can no longer occur — or its impact has been reduced to an acceptable level
- The mitigation does not introduce a regression in adjacent code paths
- The mitigation is complete — no half-measures or TODOs left behind

If the mitigation is wrong or incomplete, revise it before continuing.

**Step 6: Commit and push.**

Stage only the files changed for this risk. Write a commit message that describes what was mitigated and why,
referencing the risk number. Push the commit.

**Step 7: Close the risk.**

Leave a comment on the risk documenting:

- What was changed and where (file and line)
- Whether the mitigation followed the suggested approach or deviated, and why
- Confirmation that the mitigation was verified
- The name and version of this skill (see the frontmatter of this file)

Then remove the `in-progress` label and close the risk.

Return to Step 1.

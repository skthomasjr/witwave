---
name: feature-implement
description: Implement all approved features one at a time, committing and pushing each implementation. Trigger when the user says "implement features", "fix features", "implement approved features", "fix approved features", "run feature implement", or "start feature implementation".
version: 1.2.0
---

# feature-implement

Implement all approved features, one at a time. Each feature is fully implemented, verified, committed, and pushed before moving to the next.

## Instructions

Repeat the following steps until all approved features have been processed.

**Step 1: Select the next feature to implement.**

First, check for any open features labeled `feature` and `approved` whose status is `in-progress`. If one exists, resume it — it was already selected and started in a previous run. Do not start a new feature until the in-progress one is resolved.

If no in-progress feature exists, retrieve all open features labeled `feature` and `approved`. If none remain, stop — all approved features have been implemented.

From the approved features, select the one to implement next using this priority order:
- Features with no `blocked-by` dependencies come before blocked features
- Among unblocked features: high priority before medium before low
- Among equal priority: prefer features from the same request that are grouped together in the refinement execution order
- Among equal grouping: prefer the feature with the most self-contained implementation (narrowest scope)

Before proceeding, verify that all issues listed in the feature's `Depends on` field are closed and implemented. If any dependency is still open, the feature is genuinely blocked — do not implement it. Select the next eligible feature instead.

Once selected, remove the `approved` label, add the `in-progress` label, update the feature status to `in-progress`, and set `**Claimed by:**` to the current agent name.

---

**Step 2: Understand the affected code.**

Read the full source file(s) relevant to this feature — not just the area where code will be added. Understand what calls the surrounding code and what it calls. If the feature touches shared utilities or cross-cutting concerns, read those files too. If the feature requires new files or components, identify where they should live and what naming conventions the surrounding codebase follows. Re-read the originating request and its comment thread to make sure the implementation will satisfy what was actually asked for. If the feature description no longer aligns with the request, update the feature record before writing any code.

---

**Step 3: Research if needed.**

If the implementation involves an unfamiliar pattern, API, library, or external service — do a targeted web search before writing code. Search for the specific pattern or integration in question. If a search confirms the right approach, proceed. If the search reveals a better or more complete implementation, note what you found and adjust the approach accordingly.

---

**Step 4: Apply the implementation.**

Implement the new capability. Use the suggested design from the feature record unless your code review reveals a better or more correct approach. If you deviate, note why.

Do not implement more than one feature per commit. Do not make unrelated changes.

---

**Step 5: Verify the implementation.**

Re-read the changed code. Confirm:
- The capability described in the feature record is now present and working as described
- The implementation does not introduce a regression in adjacent code paths
- The implementation is complete — no half-measures or TODOs left behind
- The result is consistent with what the originating request asked for
- All new code paths are covered by tests; if the feature introduces a new component or file, write tests for it from scratch rather than relying on existing coverage
- The feature integrates correctly with the rest of the system — if it adds a new command, endpoint, or agent behavior, verify it end-to-end in a realistic scenario, not just in isolation
- All relevant documentation has been updated — read `<repo-root>/README.md`, `<repo-root>/AGENTS.md`, and any component-level READMEs to identify anywhere the new capability should be mentioned, and update them. If the feature adds a new component, endpoint, configuration option, or behavioral change visible to users or other agents, document it

If the implementation is wrong or incomplete, revise it before continuing.

---

**Step 6: Commit and push.**

Stage only the files changed for this feature. Write a commit message that describes what was added and why, referencing the feature number. Push the commit.

---

**Step 7: Close the feature.**

Leave a comment on the feature documenting:
- What was added and where
- Whether the implementation followed the suggested design or deviated, and why
- Confirmation that the implementation was verified
- The name and version of this skill (see the frontmatter of this file)

Then close the feature.

Look up all features linked to the same originating request. If every one of them is now closed, post a comment on the originating request summarizing what was delivered and asking whether the request is complete or whether further work is needed. Do not close the request — leave that decision to the requester.

Return to Step 1.

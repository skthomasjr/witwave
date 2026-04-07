---
name: work-features
description: >-
  Review open feature proposals one at a time — triage, approve, enrich, or
  defer each one so the feature backlog stays actionable and evaluate-features
  has a clean queue to work from
---

Work through all open feature proposals systematically — get oriented, understand
the codebase they relate to, then act on each issue in priority order.

Feature issues are proposals, not tasks. The goal is not to implement them here
but to ensure each one is well-specified, accurately prioritized, and in the
right status so `evaluate-features` can plan and create implementation tasks.

Steps:

1. Load all open feature proposals from GitHub:

   ```bash
   gh issue list --state open --label "feature" --limit 100 \
     --json number,title,labels \
     --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(", "))]"'
   ```

   Read each issue body in full via `/github-issue view <number>`. For each
   proposal, note:

   - Issue number and title
   - Priority (`priority/p0` through `priority/p3`)
   - Status (`status/pending`, `status/approved`, `status/needs-more-info`)
   - Confidence (`high`, `medium`, `low`)
   - Whether the Questions field has unresolved blockers
   - Whether the Acceptance criteria are specific and verifiable

   Then load the full task history for each feature so you know its
   implementation progress before evaluating anything. Substitute the actual
   feature issue number for `<number>`:

   ```bash
   gh issue list --state all --label "type/feature" --limit 100 \
     --json number,title,state,body \
     --jq '.[] | select(.body | contains("**Feature:** #<number>")) |
       "#\(.number) [\(.state)] theme:\(.body | capture("\\*\\*Feature Theme:\\*\\* (?<t>[\\w-]+)").t // "?") slice:\(.body | capture("\\*\\*Feature Slice:\\*\\* (?<s>[0-9]+)").s // "?") \(.title)"'
   ```

   For each feature note: which themes are fully closed, which theme is
   currently in-flight (has open tasks), and how many slices exist total.
   This is the ground truth for implementation progress — do not rely on
   source code alone to judge whether a feature is "done".

2. Build a complete picture of the codebase before evaluating anything:

   Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the
   system's purpose and architecture. Read all source files under `<repo-root>/agent/`
   and `<repo-root>/agent/backends/`, plus `Dockerfile` and `docker-compose.yml`.
   Read all files under `<repo-root>/docs/`. Do not start evaluating until you
   have read all of them — you need to know what is already built to judge whether
   a proposal is still relevant and whether its implementation details are accurate.

3. Triage and sequence the proposals — highest impact first:

   Rank the proposals in the order they should be acted on. Prefer this ordering:

   - `status/approved` proposals with `confidence/high` and clear acceptance criteria
     — verify they are still relevant and correctly prioritized
   - `status/pending` proposals that are ready to approve — well-specified,
     evidence-backed, unblocked Questions field
   - `status/pending` proposals that need enrichment before approval — thin
     Value or Implementation, stale file references, unanswered Questions
   - `status/needs-more-info` proposals — assess whether the blocker has been
     resolved; re-approve or close
   - Low-confidence or speculative proposals — reassess; flag for human review if no clear path forward

   Within a tier, act on higher-priority proposals first (`p0` before `p1`, etc.).

   Present the planned order with one sentence per proposal explaining the
   action planned, then proceed immediately to step 4.

4. For each proposal, in order:

   **a. Check the issue comment thread.**

   Run `/github-issue view <number>` and read the full body and comment thread.
   If there is an open question posted by a previous agent run that has not been
   answered by the user:

   - If the issue is already claimed by this agent, unclaim it: update the body
     to set `Claimed by: none` and `Status: status/approved`, then run
     `gh issue edit <number> --body "<updated body>" --add-label "status/approved" --remove-label "status/in-progress"`
   - Skip this proposal and move on to the next one.

   **b. Check implementation progress.**

   Use the task history loaded in step 1 to determine whether this proposal is
   already being driven by `evaluate-features`. Do not close feature issues —
   closing is `evaluate-features`' responsibility, not this skill's.

   - If open tasks exist for this feature: skip — `evaluate-features` is
     already driving it. Move on to the next proposal.
   - If all tasks are closed and no open tasks remain: the last theme has
     landed. Leave the feature open — `evaluate-features` will either plan
     the next theme or close it once all Acceptance criteria are met.

   **c. Classify the proposal.**

   Determine which action is appropriate:

   - **Approve** — the proposal has a clear Value with cited evidence, a specific
     Implementation section with accurate file references, verifiable Acceptance
     criteria, and no unresolved blockers in Questions. Set `status/approved`.
   - **Enrich then approve** — the proposal has a sound idea but thin or stale
     details. Update the issue body directly with corrected file paths, API names,
     or implementation steps. If you can resolve Questions using your codebase
     knowledge, update the Questions field and note the source. After enriching,
     approve.
   - **Request more info** — the proposal has unresolved questions that require
     human judgment (architectural decisions, product direction, external
     constraints). Post a comment describing what is needed and set
     `status/needs-more-info`. Do not approve under uncertainty.
   - **Leave pending** — the proposal is speculative or low-confidence but not
     clearly wrong. Leave it in its current status and note it in the summary
     for human review. Do not close it.

   **d. Act on the proposal.**

   For **Approve** or **Enrich then approve**:

   - If enriching: edit the issue body directly to update stale Implementation
     details (file paths, function names, API names, env var names). Do not pad
     — only change what is actually wrong or missing.
   - Update the Confidence field if the codebase research made it clearer or murkier.
   - Fetch the current issue body: `gh issue view <number> --json body --jq '.body'`
   - Update the body's `**Status:**` line to `status/approved`.
   - Apply the updated body and relabel in one call:
     `gh issue edit <number> --body "<updated body>" --add-label "status/approved" --remove-label "status/pending,status/needs-more-info"`
   - Post a comment: `/github-issue comment <number> "Approved — <one sentence
     summarizing why it is ready and what, if anything, was updated>"`

   For **Request more info**:

   - Fetch the current issue body: `gh issue view <number> --json body --jq '.body'`
   - Update the body's `**Status:**` line to `status/needs-more-info`.
   - Apply the updated body and relabel in one call:
     `gh issue edit <number> --body "<updated body>" --add-label "status/needs-more-info" --remove-label "status/pending,status/approved"`
   - Post a comment: `/github-issue comment <number> "Needs more info — <describe
     exactly what decision or information is required and why it cannot be resolved
     from the codebase alone>"`

   For **Leave pending**: no label or body changes. Note the issue number and
   reason in the step 5 summary for human review.

5. After all proposals are acted on, report a summary:

   - Proposals approved (issue number, title)
   - Proposals approved after enrichment (issue number, what was updated)
   - Proposals set to needs-more-info (issue number, what was asked)
   - Proposals skipped — in-flight (issue number, open task numbers)
   - Proposals left pending for human review (issue number, reason)
   - Total open `status/approved` `feature` count — this is the queue
     available to `evaluate-features`

6. Reflect on the triage process. If you encountered any ambiguity, missing
   context, or steps that slowed you down — note them and create a GitHub Issue
   using `/github-issue create task status/approved` with type
   `type/code-quality` describing the specific improvement and which step in
   this skill it affects.

---
name: evaluate-features
description: >-
  Translate approved feature proposals into actionable implementation issues —
  theme-scoped task slices per feature, grounded in the current codebase,
  leaving the feature open until fully complete
---

For each approved feature, assess implementation status, pick the next theme,
and create one focused GitHub Issue per discrete unit of work within that theme.
Tasks in a theme may run in parallel with explicit dependencies between them.
Do not plan the next theme until all tasks from the current one are closed.

**Pipeline:** `evaluate-features` → tasks → `develop` → code merged → repeat.

Steps:

1. **Load context.**

   a. List approved features:

      ```bash
      gh issue list --state open \
        --label "feature" --label "status/approved" --limit 100 \
        --json number,title \
        --jq '.[] | "#\(.number) \(.title)"'
      ```

      If none, stop.

   b. For each feature, load its full task history. Substitute the actual
      feature issue number for `<number>`:

      ```bash
      gh issue list --state all --label "type/feature" --limit 500 \
        --json number,title,state,body \
        --jq '.[] | select(.body | test("\\*\\*Feature:\\*\\* #<number>([^0-9]|$)")) |
          "#\(.number) [\(.state)] theme:\(.body | capture("\\*\\*Feature Theme:\\*\\* (?<t>[\\w-]+)").t // "?") slice:\(.body | capture("\\*\\*Feature Slice:\\*\\* (?<s>[0-9]+)").s // "?") \(.title)"'
      ```

      Note: current theme (theme of any open tasks), completed themes, and
      next slice number (closed task count + 1).

   c. Read `README.md`, `AGENTS.md`, all files under `docs/`, every source
      file under `agent/` and `agent/backends/`, `Dockerfile`, and
      `docker-compose.active.yml`. Do not skim — a complete picture of the codebase
      is required before assessing any feature.

2. **For each approved feature, in priority order (`p0` first):**

   **a. Read the feature issue** via `/github-issue view <number>`. Note the
   **Acceptance criteria**, **Implementation**, **Confidence**, and
   **Questions** fields.

   **b. Assess implementation status.** From the task history loaded in
   step 1b, identify the current theme — the theme of any open `type/feature`
   issues for this feature. Then check whether all issues for that specific
   theme and slice are closed.

   **Hard rule: do not plan a new theme or create any new issues if any
   `type/feature` issues for the current theme and slice are still open.**
   Every issue in the current theme must be fully closed before moving to
   the next theme.

   Then take the appropriate path:

   - **Open type/feature issues exist for the current theme/slice** — the
     current theme has not fully landed. Run `/github-issue view <number>`
     to fetch the feature issue's current comment thread. If no comment
     containing "Waiting on open type/feature issues" exists, run
     `/github-issue comment <number> "Waiting on open type/feature issues for theme '<theme>' slice <N> before planning next theme: #N, #N"`.
     Move on. Do not proceed to 2c.

   - **Fully implemented** — all type/feature issues across all themes and
     slices are closed AND all acceptance criteria are met in the source. Run
     `/github-issue close <number> "Fully implemented — all acceptance
     criteria met"` and move on.

   - **Hand-off** — all open type/feature issues are closed, core behavior
     is working, and only refinements remain (error handling, a log line, a
     metric, a config default) that `evaluate-gaps` or `evaluate-bugs` would
     catch naturally. To hand off:
     1. Fetch the issue body (`gh issue view <number> --json body --jq '.body'`),
        update its `**Status:**` line to `status/needs-more-info`, then apply
        the updated body and labels in one call:
        `gh issue edit <number> --body "<updated body>" --add-label "status/needs-more-info" --remove-label "status/approved,status/in-progress,status/pending"`
     2. Run `/github-issue comment <number> "Core complete — handed off to evaluate-gaps / evaluate-bugs. Relabel to status/approved to re-enter this queue."`
     Move on.

   - **Not started or partially implemented** — all type/feature issues for
     the current theme and slice are closed but acceptance criteria are not
     yet fully met in the source. Continue to 2c to plan the next theme.

   **c. Determine the next theme.** Based on completed themes and current
   codebase state, choose the next logical phase. Name it with a short
   lowercase hyphenated label (e.g. `data-model`, `api-layer`, `config`,
   `observability`, `error-handling`). Start with the foundation everything
   else depends on. Skip if the **Questions** field has unresolved blockers
   or **Confidence** is `low` with no clear path — post a comment explaining
   why and move on.

   **d. Plan the theme's tasks.** Identify every discrete unit of work to
   complete this theme. Each unit must be completable in isolation (one file
   or one concern), specific enough to start immediately, and small enough to
   finish in a single focused change. Map dependencies — units with no
   ordering constraint are parallel.

   **e. Create one GitHub Issue per unit** via
   `/github-issue create task status/approved`. Create independent tasks
   first so their issue numbers exist when setting `Depends on:` for
   dependent tasks. This is the primary output — every run must produce at
   least one new issue per feature, or explain why it did not.

   For each issue provide:

   - **Title:** short and concrete — `"Add X to Y"` or `"Wire Z into W"`
   - **Type:** `type/feature`
   - **Priority:** inherited from the feature issue
   - **Created by:** `<agent-name>` (value of `$AGENT_NAME` if set, otherwise `local-agent`)
   - **Feature:** `#<feature-number>`
   - **Feature Theme:** `<theme-label>`
   - **Feature Slice:** closed-count + 1 for the first new task this run,
     incrementing by 1 for each additional task created in the same run
   - **Depends on:** `none`, or `#<task-number>` of a task that must land
     first (must already exist before referencing it)
   - **File:** primary file and line number where the change begins
   - **Description:**
     - **What:** one sentence — the specific change, file, and function
     - **Why:** one sentence — how this advances the feature
     - **How:** 3–5 concrete bullets — exact steps to implement it
   - **Acceptance criteria:** 1–3 verifiable conditions that define done

   Example:

   > **Title:** Add `webhook_url` field to `AgentConfig`
   >
   > **What:** Add `webhook_url: str | None` to `AgentConfig` in
   > `agent/config.py:AgentConfig`.
   >
   > **Why:** Required foundation for feature #42 (webhook triggers) —
   > nothing else in the `api-layer` theme can proceed without it.
   >
   > **How:**
   > - Add `webhook_url: str | None = None` to the `AgentConfig` dataclass
   > - Add `AGENT_WEBHOOK_URL` to the env var block in `_from_env()`
   > - Raise `ValueError` on load if the value is set but not a valid URL
   >
   > **Acceptance criteria:**
   > - [ ] `AgentConfig.webhook_url` defaults to `None`
   > - [ ] `AGENT_WEBHOOK_URL` env var populates the field
   > - [ ] An invalid URL raises `ValueError` on config load

   **f.** Run `/github-issue comment <number> "Theme '<label>' — tasks created: #N, #N, #N"`

3. **Report:**
   - Tasks created per feature (numbers, titles, theme, dependencies)
   - Features holding — open tasks blocking next theme (task numbers)
   - Features handed off, fully closed, or deferred (with reason)

4. **Reflect.** If any step was unclear or produced poor results, create a
   `type/code-quality` issue via `/github-issue create task status/approved`
   describing the improvement and which step it affects.

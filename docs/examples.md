# Examples

This document provides working examples of agent configuration files.

## Heartbeat

A heartbeat fires on a cron schedule and dispatches a prompt to the agent's default backend. It is defined in a single
`HEARTBEAT.md` file in the agent's `.nyx/` directory.

**Minimal test heartbeat** (fires every hour, verifies scheduler + backend are alive):

```markdown
---
description: Test heartbeat — verifies the scheduler fires correctly.
schedule: "0 * * * *"
enabled: true
---

Respond with HEARTBEAT_OK.
```

**Real heartbeat** (fires every 30 minutes, drives autonomous work):

```markdown
---
description: Proactive work heartbeat — reviews open issues and advances the highest-priority item.
schedule: "*/30 * * * *"
enabled: true
---

You have woken up. Check for the highest-priority approved GitHub Issue and advance it by one meaningful step.
Do not start new work while a prior run is still in progress. Report what you did or why you skipped.
```

Frontmatter fields:

| Field         | Required | Description                              |
| ------------- | -------- | ---------------------------------------- |
| `description` | No       | Human-readable summary                   |
| `schedule`    | Yes      | Cron expression (UTC)                    |
| `enabled`     | Yes      | `true` to activate, `false` to disable   |

---

## Jobs

Jobs are defined as `*.md` files in the agent's `.nyx/jobs/` directory. Each file is an independent scheduled task.
The job scheduler watches the directory for changes and registers or unregisters jobs at runtime — no restart needed.

**Minimal test job** (fires every 5 minutes, verifies job scheduler + backend are alive):

```markdown
---
name: Ping
description: Test job — verifies the job scheduler fires and the backend responds correctly.
schedule: "*/5 * * * *"
enabled: true
---

Respond with JOB_OK.
```

**Code review job** (fires hourly, creates GitHub Issues for findings):

```markdown
---
name: Code Review
description: Reviews source code and files GitHub Issues for bugs, reliability issues, and code quality findings.
schedule: "0 * * * *"
enabled: true
---

Review the source code in the repo root and create GitHub Issues for findings.

Steps:

1. Read README.md and CLAUDE.md to understand the purpose and architecture.
2. Read all source files under the repo root.
3. Evaluate for bugs, reliability issues, code quality, and missing observability.
4. For each finding, check whether an open issue already covers it before creating a new one.
5. Create issues using the appropriate type label (type/bug, type/reliability, type/code-quality, type/enhancement).
```

**Question answering job** (fires every 5 minutes, answers open GitHub questions):

```markdown
---
name: Answer Questions
description: Polls for open GitHub Issues with type/question, claims one, researches and answers it.
schedule: "*/5 * * * *"
enabled: true
---

Poll for open unanswered questions in GitHub Issues and answer them.

Steps:

1. List open issues labeled type/question using `gh issue list --state open --label "type/question"`.
2. Take the first unclaimed issue (where the body contains "Claimed by: none").
3. Claim it by updating the body to set "Claimed by: <agent-name>".
4. Research the question thoroughly using repo files, git history, and external sources as needed.
5. Post a complete answer as a comment using `gh issue comment <number> --body "<answer>"`.
6. Close the issue with `gh issue close <number>`.
```

**Development job** (fires every 30 minutes, works through approved issues):

```markdown
---
name: Development
description: Works through approved GitHub Issues in priority order — one item per run.
schedule: "*/30 * * * *"
enabled: true
---

Resolve the highest-priority approved GitHub Issue.

Priority order: type/bug → type/reliability → type/code-quality → type/enhancement.
Within each type, work priority/p0 before priority/p1, and so on.

Steps:

1. List all open approved issues using `gh issue list --label "status/approved"`.
2. Select the highest-priority issue with the smallest scope.
3. Read the issue body to understand the problem, file reference, and acceptance criteria.
4. Read the relevant source files.
5. Implement the minimal fix necessary.
6. Close the issue when done.
```

Frontmatter fields:

| Field         | Required | Description                                                    |
| ------------- | -------- | -------------------------------------------------------------- |
| `name`        | No       | Display name used in logs and metrics; defaults to filename    |
| `description` | No       | Human-readable summary                                         |
| `schedule`    | Yes      | Cron expression (UTC)                                          |
| `enabled`     | No       | `true` (default) to activate, `false` to disable              |
| `session`     | No       | Session ID override; defaults to a deterministic UUID          |
| `model`       | No       | Model override passed to the backend; defaults to backend default |

---

## Tasks

Tasks are calendar-driven scheduled prompts. Unlike jobs (which repeat on a simple cron interval indefinitely),
tasks are bounded by a daily time window, optional days-of-week selection, and optional start/end date bounds —
modeled after recurring calendar events. Tasks can optionally loop within their window, pausing between iterations
and stopping early when the agent signals completion.

**Minimal test task** (fires once daily at midnight UTC, no window close, no loop):

```markdown
---
name: Task Ping
description: Test task — verifies the task scheduler fires and the backend responds correctly. Fires once daily at midnight UTC.
window-start: "00:00"
enabled: true
---

Respond with TASK_OK.
```

**Realistic looping task** (weekdays, Chicago time, 2-hour window, loops every 30 minutes, stops on signal):

```markdown
---
name: Morning Standup Review
description: Reviews open GitHub Issues each weekday morning and summarizes progress.
days: "1-5"
timezone: America/Chicago
window-start: "08:00"
window-duration: 2h
loop: true
loop-gap: 30m
done-when: STANDUP_DONE
---

Review all open GitHub Issues updated in the last 24 hours. Summarize progress, flag blockers, and identify
the highest-priority item for today. When done, respond with STANDUP_DONE.
```

Frontmatter fields:

| Field             | Required | Description |
| ----------------- | -------- | ----------- |
| `name`            | No       | Display name used in logs and metrics. Defaults to filename stem. |
| `description`     | No       | Human-readable summary. |
| `start`           | No       | Earliest date the task is eligible to run (inclusive, `YYYY-MM-DD`). Omit for no lower bound. |
| `end`             | No       | Last date the task is eligible to run (inclusive, `YYYY-MM-DD`). Omit for no expiry. |
| `days`            | No       | Cron weekday expression — numeric (`1-5`, `1,3,5`) or abbreviation (`Mon-Fri`, `Mon,Wed,Fri`). Default: `*` (every day). |
| `timezone`        | No       | IANA time zone (e.g. `America/New_York`). Applied to `window-start` and `window-end`. Default: `UTC`. |
| `window-start`    | Yes      | Start of the daily run window (`HH:MM` in the task's time zone). |
| `window-end`      | No       | End of the daily run window (`HH:MM`). Mutually exclusive with `window-duration`. Required to enable looping. |
| `window-duration` | No       | Duration of the run window from `window-start`. Format: `30m`, `4h`, `1h30m`. Mutually exclusive with `window-end`. |
| `loop`            | No       | If `true`, re-run within the window after each completion. Requires `window-end` or `window-duration`. Default: `false`. |
| `loop-gap`        | No       | Pause after a run completes before the next iteration. Format: `30s`, `15m`, `1h`, `1h30m`. Default: no pause. |
| `done-when`       | No       | Stop looping for the day if the backend response contains this string. |
| `model`           | No       | Model override passed to the backend. |
| `enabled`         | No       | `false` disables without deleting. Default: `true`. |

---

## backends.yaml

`backends.yaml` lives in `.nyx/` and controls which backend handles each concern. The `routing` block maps concern
names to backend IDs defined in the `backends` list.

**Minimal single-backend config:**

```yaml
backends:
  - id: claude
    type: a2a
    url: http://iris-a2-claude:8080

routing:
  default: claude
```

**Multi-backend config with per-concern routing:**

```yaml
backends:
  - id: claude
    type: a2a
    url: http://iris-a2-claude:8080

  - id: codex
    type: a2a
    url: http://iris-a2-codex:8080

  - id: gemini
    type: a2a
    url: http://iris-a2-gemini:8080

routing:
  default: claude      # fallback for any unmatched concern
  a2a: claude          # handles incoming A2A requests
  heartbeat: claude    # handles heartbeat-triggered work
  job: claude          # handles job execution
  task: claude         # handles task execution
```

The `url` for any backend can be overridden at deploy time via an environment variable named
`A2A_URL_<ID_UPPERCASED_WITH_UNDERSCORES>` — for example, `A2A_URL_IRIS_A2_CLAUDE`. This lets the same
`backends.yaml` work across Docker Compose, Kubernetes, and local sidecar deployments without modification.

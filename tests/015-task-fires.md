---
description: Smoke test — verifies the task scheduler fires a task and the backend responds correctly.
enabled: true
---

Bob has a task at `.agents/test/bob/.nyx/tasks/smoke.md` with `window-start: "00:00"` and `window-duration: 24h`, so it fires once on any day as soon as the scheduler starts.

Poll the conversation log at:

```
.agents/test/bob/a2-claude/logs/conversation.log
```

Poll every 5 seconds for up to 60 seconds until `TASK_SMOKE_OK` appears.

The test passes if `TASK_SMOKE_OK` is found in the conversation log within 60 seconds.
The test fails if `TASK_SMOKE_OK` does not appear within 60 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

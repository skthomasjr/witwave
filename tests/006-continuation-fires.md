---
description: Verifies that a continuation fires after an upstream job completes and the backend responds correctly.
enabled: true
---

Bob has a continuation registered at `.agents/test/bob/.nyx/continuations/ping.md` that continues after `job:Ping` and responds with `CONTINUATION_OK`.

The job ping fires every 5 minutes. Poll the conversation log until `CONTINUATION_OK` appears, confirming the continuation ran.

Poll the conversation log at:

```
.agents/test/bob/a2-claude/logs/conversation.log
```

Poll every 5 seconds for up to 650 seconds — long enough to guarantee the job ping fires at least once regardless of when the test starts relative to the 5-minute job schedule.

The test passes if `CONTINUATION_OK` is found in the conversation log within 650 seconds.
The test fails if `CONTINUATION_OK` does not appear within 650 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

---
description: Verifies that the precommitted task continuation fires after task-ping completes.
enabled: true
---

Bob has a precommitted task fixture at `.agents/test/bob/.witwave/tasks/ping.md` that responds with `TASK_OK`. Bob also has `.agents/test/bob/.witwave/continuations/ping-delayed.md`, which continues after `task:task-ping`, waits 10 seconds, and responds with `CONTINUATION_DELAYED_OK` when the upstream response contains `TASK_OK`.

## Verification

Poll Bob's conversation evidence:

```bash
ww conversation list --namespace witwave-test --agent bob --expand
```

Poll every 2 seconds for up to 90 seconds until both `TASK_OK` and `CONTINUATION_DELAYED_OK` appear in the same task session.

## Pass/Fail Criteria

The test passes if:

1. `TASK_OK` appears in Bob's conversation log.
2. `CONTINUATION_DELAYED_OK` appears after `TASK_OK` in the same session.

The test fails if either string is absent within 90 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it; mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

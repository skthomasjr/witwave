---
description: Smoke test - verifies the precommitted task scheduler fixture fires and the backend responds correctly.
enabled: true
---

Bob has a precommitted task fixture at `.agents/test/bob/.witwave/tasks/smoke.md`. It has a 24-hour window and responds
with `TASK_SMOKE_OK`, so it should fire after the test deployment becomes ready.

## Verification

Poll Bob's conversation evidence:

```bash
ww conversation list --namespace witwave-test --agent bob --expand
```

Poll every 2 seconds for up to 60 seconds until `TASK_SMOKE_OK` appears.

## Pass/Fail Criteria

The test passes if `TASK_SMOKE_OK` appears in Bob's conversation log within 60 seconds. The test fails if it does not
appear within the timeout.

**If the failure is caused by a code bug in the system under test, do not fix it; mark the test as failed and report the
issue. Only fix tooling or execution problems that prevent the test itself from running.**

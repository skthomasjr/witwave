---
description: Verifies that the precommitted job continuation fires after the ping job completes.
enabled: true
---

Bob has a precommitted recurring job at `.agents/test/bob/.witwave/jobs/ping.md` that responds with `JOB_OK`. Bob also has `.agents/test/bob/.witwave/continuations/ping.md`, which continues after `job:ping` and responds with `CONTINUATION_OK`.

## Verification

Poll Bob's conversation evidence:

```bash
ww conversation list --namespace witwave-test --agent bob --expand
```

Poll every 15 seconds for up to 16 minutes until both `JOB_OK` and `CONTINUATION_OK` appear in the same job session. The long timeout is intentional because the `ping` job runs every 15 minutes.

## Pass/Fail Criteria

The test passes if:

1. `JOB_OK` appears in Bob's conversation log.
2. `CONTINUATION_OK` appears after `JOB_OK` in the same session.

The test fails if either string is absent within the timeout.

**If the failure is caused by a code bug in the system under test, do not fix it; mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

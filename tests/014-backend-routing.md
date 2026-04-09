---
description: Verifies that per-concern routing in backends.yaml sends each concern to the configured backend.
enabled: true
---

Bob's `backends.yaml` routes all concerns (`a2a`, `heartbeat`, `job`, `task`, `trigger`, `continuation`) to the `claude` backend (`bob-a2-claude` at port 8090). This test verifies that traffic actually reaches the configured backend by checking the backend's own conversation log rather than the nyx-agent log.

The a2-claude backend writes all prompts and responses to its own conversation log at `.agents/test/bob/a2-claude/logs/conversation.log`. If routing is working correctly, all of the following should appear in that log (not the nyx-agent log or any other backend's log):

1. An A2A request
2. A job execution (JOB_OK)
3. A trigger execution (TRIGGER_OK)

The test does not need to fire new requests — tests 001-007 have already exercised all these paths. Simply verify that all three signal strings are present in the a2-claude conversation log.

## Verification

Check that the following tokens are all present in `.agents/test/bob/a2-claude/logs/conversation.log`:

- `JOB_OK` (from job:Ping running against a2-claude)
- `TRIGGER_OK` (from trigger:ping running against a2-claude)
- `HEARTBEAT_OK` (from heartbeat running against a2-claude) — may not be present if heartbeat hasn't fired yet; skip this check if absent

Also verify that the codex backend conversation log at `.agents/test/bob/a2-codex/logs/conversation.log` does NOT contain `JOB_OK` or `TRIGGER_OK` (confirming traffic was not misrouted).

## Pass/Fail Criteria

The test passes if:
1. `JOB_OK` is present in `.agents/test/bob/a2-claude/logs/conversation.log`.
2. `TRIGGER_OK` is present in `.agents/test/bob/a2-claude/logs/conversation.log`.
3. `JOB_OK` is NOT present in `.agents/test/bob/a2-codex/logs/conversation.log`.
4. `TRIGGER_OK` is NOT present in `.agents/test/bob/a2-codex/logs/conversation.log`.

The test fails if any of the above conditions are not met.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

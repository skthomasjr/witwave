---
description: Verifies that a continuation with a delay fires after an upstream trigger completes, with the correct delay behavior.
enabled: true
---

Bob has a continuation registered at `.agents/test/bob/.nyx/continuations/trigger-ping.md` that continues after `trigger:ping` with a 5-second delay and responds with `CONTINUATION_TRIGGER_OK`.

Step 1 — fire the upstream trigger:

```
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/ping \
  -H "Content-Type: application/json" \
  -d '{}'
```

The trigger returns 202 immediately. After the trigger backend completes, the continuation waits 5 seconds then fires.

Step 2 — poll the conversation log at:

```
.agents/test/bob/a2-claude/logs/conversation.log
```

Poll every 2 seconds for up to 60 seconds until `CONTINUATION_TRIGGER_OK` appears.

The test passes if `CONTINUATION_TRIGGER_OK` is found in the conversation log within 60 seconds.
The test fails if `CONTINUATION_TRIGGER_OK` does not appear within 60 seconds, or if the trigger endpoint is unreachable.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

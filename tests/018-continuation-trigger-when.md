---
description:
  Verifies that trigger-when filtering allows a continuation to fire only when the upstream response contains the
  expected string.
enabled: true
---

Bob has a continuation at `.agents/test/bob/.witwave/continuations/ping-delayed.md` with
`continues-after: task:Task Ping` and `trigger-when: TASK_OK`. This continuation only fires if the upstream response
contains `TASK_OK`.

This test uses a different approach — create two continuations on the same upstream (trigger:ping): one with a
`trigger-when` string that will appear in the response, and one with a string that will not. Only the first should fire.

## Setup

Create a continuation that should fire (trigger-when matches the response):

```
cat > .agents/test/bob/.witwave/continuations/trigger-when-match.md << 'EOF'
---
name: Trigger When Match
description: Should fire — trigger-when matches TRIGGER_OK which appears in the upstream response.
continues-after: trigger:ping
trigger-when: TRIGGER_OK
---
Respond with TRIGGER_WHEN_MATCH_FIRED.
EOF
```

Create a continuation that should NOT fire (trigger-when does not match):

```
cat > .agents/test/bob/.witwave/continuations/trigger-when-nomatch.md << 'EOF'
---
name: Trigger When No Match
description: Should not fire — trigger-when string will not appear in the upstream response.
continues-after: trigger:ping
trigger-when: THIS_STRING_WILL_NOT_APPEAR_IN_RESPONSE
---
Respond with TRIGGER_WHEN_NOMATCH_FIRED.
EOF
```

Wait 5 seconds for watchers to register the files.

## Fire the upstream trigger

```
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:-smoke-test-token}" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Verify the response is 202.

## Poll

Poll the conversation log at `.agents/test/bob/logs/conversation.jsonl` every 2 seconds for up to 60 seconds.

## Cleanup

```
rm .agents/test/bob/.witwave/continuations/trigger-when-match.md
rm .agents/test/bob/.witwave/continuations/trigger-when-nomatch.md
```

## Pass/Fail Criteria

The test passes if:

1. `TRIGGER_WHEN_MATCH_FIRED` appears in the log within 60 seconds.
2. `TRIGGER_WHEN_NOMATCH_FIRED` does NOT appear in the log.

The test fails if `TRIGGER_WHEN_MATCH_FIRED` is absent, or if `TRIGGER_WHEN_NOMATCH_FIRED` appears.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

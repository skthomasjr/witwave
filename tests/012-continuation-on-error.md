---
description: Verifies that a continuation with on-error:true fires when the upstream fails, and that a success-only continuation does not fire on failure.
enabled: true
---

This test verifies the `on-error` and `on-success` flags on continuations by using a trigger that the backend cannot process successfully.

## Setup

Create a trigger whose backend prompt is guaranteed to produce an error by targeting a non-existent backend endpoint (we simulate the error by creating a trigger that calls for a backend that is unreachable). 

A simpler approach: create a trigger whose content instructs the agent to respond normally, then create two continuations — one `on-error: true, on-success: false` and one `on-success: true, on-error: false` (the default). Verify only the success continuation fires after a normal trigger execution.

Create a trigger for this test:

```
cat > .agents/test/bob/.nyx/triggers/error-test-trigger.md << 'EOF'
---
name: Error Test Trigger
description: Trigger for testing continuation on-error vs on-success behavior.
endpoint: error-test-trigger
---
Respond with ERROR_TRIGGER_UPSTREAM_OK.
EOF
```

Create the success-only continuation (default behavior):

```
cat > .agents/test/bob/.nyx/continuations/on-success-test.md << 'EOF'
---
name: On Success Test
description: Should fire only on success.
continues-after: trigger:error-test-trigger
on-success: true
on-error: false
---
Respond with CONTINUATION_SUCCESS_FIRED.
EOF
```

Create the error-only continuation:

```
cat > .agents/test/bob/.nyx/continuations/on-error-test.md << 'EOF'
---
name: On Error Test
description: Should fire only on error — not expected to fire in this test.
continues-after: trigger:error-test-trigger
on-success: false
on-error: true
---
Respond with CONTINUATION_ERROR_FIRED.
EOF
```

Wait 5 seconds for watchers to register the new files.

## Fire the trigger

```
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/error-test-trigger \
  -H "Content-Type: application/json" \
  -d '{}'
```

Verify the response is 202.

## Poll

Poll the conversation log at `.agents/test/bob/a2-claude/logs/conversation.log` every 2 seconds for up to 60 seconds.

## Cleanup

```
rm .agents/test/bob/.nyx/triggers/error-test-trigger.md
rm .agents/test/bob/.nyx/continuations/on-success-test.md
rm .agents/test/bob/.nyx/continuations/on-error-test.md
```

## Pass/Fail Criteria

The test passes if:
1. `CONTINUATION_SUCCESS_FIRED` appears in the log within 60 seconds (success continuation fired correctly).
2. `CONTINUATION_ERROR_FIRED` does NOT appear in the log (error continuation correctly suppressed on success).

The test fails if `CONTINUATION_SUCCESS_FIRED` is absent, or if `CONTINUATION_ERROR_FIRED` appears.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

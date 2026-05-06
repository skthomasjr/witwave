---
description:
  Verifies that custom headers (including {{env.VAR}} values) are sent with the webhook POST and delivery succeeds.
enabled: true
---

This test verifies that a webhook with a `headers:` map — including a header whose value contains
`{{env.WEBHOOK_TEST_TOKEN}}` — delivers successfully. Correct header transmission is confirmed indirectly: the trigger
endpoint returns 202 (meaning harness accepted the POST), and the backend produces `FEATURE_SINK_OK`, confirming the
full delivery path completed.

The webhook fixture is `.agents/test/bob/.witwave/webhooks/test-headers.md`. It fires when a response contains
`WEBHOOK_HEADERS_FIRE` and POSTs to the URL held in `WEBHOOK_TEST_URL_FEATURE_SINK` (resolves to
`http://witwave-bob:8099/triggers/feature-sink`) with:

- `X-Test-Token: test-token-abc123` (resolved from `{{env.WEBHOOK_TEST_TOKEN}}`)
- `X-Static-Header: static-value`

## Step 1 — Send a prompt that produces WEBHOOK_HEADERS_FIRE

```
curl -s -X POST http://localhost:8099/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"023","method":"message/send","params":{"message":{"role":"user","messageId":"msg-023","contextId":"webhook-headers-test","parts":[{"kind":"text","text":"Respond with exactly: WEBHOOK_HEADERS_FIRE"}]}}}'
```

Wait for the A2A response to contain `WEBHOOK_HEADERS_FIRE`.

## Step 2 — Wait for the webhook chain to complete

After `WEBHOOK_HEADERS_FIRE` appears, the webhook runner fires asynchronously. Poll the shared conversation log until
`FEATURE_SINK_OK` appears, or until 30 seconds have elapsed:

```
.agents/test/bob/logs/conversation.jsonl
```

## Pass/Fail Criteria

The test passes if ALL of the following are true:

1. The A2A request returns HTTP 200.
2. The A2A response contains `WEBHOOK_HEADERS_FIRE`.
3. The conversation log contains `FEATURE_SINK_OK` within 30 seconds of the A2A response.

The test fails if any condition is not met within the timeout.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

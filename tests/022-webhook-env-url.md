---
description: Verifies that env-derived webhook URLs resolve correctly via url-env-var and delivery succeeds.
enabled: true
---

This test verifies that a webhook whose URL is resolved via the `url-env-var` field reads the full URL from the named
environment variable at parse time and successfully POSTs to the resolved URL. Inline `{{env.VAR}}` interpolation in the
`url:` field is not supported (see #524) — env-derived URLs must go through `url-env-var`.

The webhook fixture is `.agents/test/bob/.witwave/webhooks/test-env-url.md`. It fires when a response contains
`WEBHOOK_ENV_URL_FIRE` and POSTs to `WEBHOOK_TEST_URL_FEATURE_SINK`, which resolves to
`http://witwave-bob:8099/triggers/feature-sink`. That trigger dispatches a prompt to the backend which responds with
`FEATURE_SINK_OK`.

## Step 1 — Send a prompt that produces WEBHOOK_ENV_URL_FIRE

```
curl -s -X POST http://localhost:8099/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"022","method":"message/send","params":{"message":{"role":"user","messageId":"msg-022","contextId":"webhook-env-url-test","parts":[{"kind":"text","text":"Respond with exactly: WEBHOOK_ENV_URL_FIRE"}]}}}'
```

Wait for the A2A response to contain `WEBHOOK_ENV_URL_FIRE`.

## Step 2 — Wait for the webhook chain to complete

After `WEBHOOK_ENV_URL_FIRE` appears, the webhook runner fires asynchronously. Poll the shared conversation log until
`FEATURE_SINK_OK` appears, or until 30 seconds have elapsed:

```
.agents/test/bob/logs/conversation.jsonl
```

## Pass/Fail Criteria

The test passes if ALL of the following are true:

1. The A2A request returns HTTP 200.
2. The A2A response contains `WEBHOOK_ENV_URL_FIRE`.
3. The conversation log contains `FEATURE_SINK_OK` within 30 seconds of the A2A response.

The test fails if any condition is not met within the timeout.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

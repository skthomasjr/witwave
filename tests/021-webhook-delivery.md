---
description:
  Verifies end-to-end outbound webhook delivery — a completed A2A prompt fires a webhook that POSTs to a trigger
  endpoint, which runs a second prompt on the backend.
enabled: true
---

This test exercises the full webhook chain:

1. An A2A prompt produces a response containing `WEBHOOK_FIRE`
2. The `chain-test` webhook subscription matches on that response and POSTs to `POST /triggers/webhook-sink`
3. The `webhook-sink` trigger dispatches a second prompt to the backend
4. The backend responds with `WEBHOOK_CHAIN_OK`, which appears in the conversation log

## Step 1 — Send a prompt that produces WEBHOOK_FIRE

```text
curl -s -X POST http://localhost:8099/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"021","method":"message/send","params":{"message":{"role":"user","messageId":"msg-021","contextId":"webhook-chain-test","parts":[{"kind":"text","text":"Respond with exactly: WEBHOOK_FIRE"}]}}}'
```

Wait for the A2A response to contain `WEBHOOK_FIRE` (poll the response or wait up to 30 seconds).

## Step 2 — Wait for the webhook chain to complete

After `WEBHOOK_FIRE` appears in the response, the webhook runner will fire asynchronously. Poll the Bob conversation log
until `WEBHOOK_CHAIN_OK` appears, or until 30 seconds have elapsed:

```text
ww conversation list --namespace witwave-test --agent bob --expand
```

## Pass/Fail Criteria

The test passes if ALL of the following are true:

1. The A2A request returns HTTP 200.
2. The A2A response contains `WEBHOOK_FIRE`.
3. The conversation log contains `WEBHOOK_CHAIN_OK` within 30 seconds of the A2A response.

The test fails if any condition is not met within the timeout.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

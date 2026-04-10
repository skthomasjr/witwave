---
description: Verifies that LLM extraction runs before body rendering and the extracted variable appears in the POST body.
enabled: true
---

This test verifies the LLM extraction pipeline: the webhook runner sends the context (rendered markdown body with
`{{response_preview}}` substituted in) plus the extraction prompt to the backend LLM, uses the response as a
variable, and substitutes it into the `body:` template before POSTing.

The webhook fixture is `.agents/test/bob/.nyx/webhooks/test-extract.md`. It fires when a response contains
`WEBHOOK_EXTRACT_FIRE` and:

1. Substitutes `{{response_preview}}` into the markdown body (which becomes the extraction context).
2. Sends the extraction prompt — "return only the word between EXTRACT_START and EXTRACT_END" — to the LLM with that context.
3. Places the extracted word into `{{extracted_word}}` in the body template:
   `{"event": "extract-test", "extracted": "{{extracted_word}}", "agent": "{{agent}}"}`
4. POSTs that JSON to `http://bob:8000/triggers/feature-sink`.

The trigger dispatches a prompt to the backend which responds with `FEATURE_SINK_OK`.

## Step 1 — Send a prompt that produces WEBHOOK_EXTRACT_FIRE with an embedded token

The prompt must include a word between `EXTRACT_START` and `EXTRACT_END` so the extraction has something to find.

```
curl -s -X POST http://localhost:8099/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"024","method":"message/send","params":{"message":{"role":"user","messageId":"msg-024","contextId":"webhook-extract-test","parts":[{"kind":"text","text":"Respond with exactly this text: WEBHOOK_EXTRACT_FIRE EXTRACT_START canary EXTRACT_END"}]}}}'
```

Wait for the A2A response to contain `WEBHOOK_EXTRACT_FIRE`.

## Step 2 — Wait for the webhook chain to complete

After `WEBHOOK_EXTRACT_FIRE` appears, the webhook runner fires asynchronously (including an LLM extraction call
before delivery). Poll the shared conversation log until `FEATURE_SINK_OK` appears, or until 60 seconds have
elapsed (allow extra time for the extraction LLM call):

```
.agents/test/bob/logs/conversation.jsonl
```

## Pass/Fail Criteria

The test passes if ALL of the following are true:

1. The A2A request returns HTTP 200.
2. The A2A response contains `WEBHOOK_EXTRACT_FIRE`.
3. The conversation log contains `FEATURE_SINK_OK` within 60 seconds of the A2A response.

The test fails if any condition is not met within the timeout.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

---
description: Verifies that the nyx-agent /metrics endpoint returns both its own metrics and relabelled backend metrics in a single scrape.
enabled: true
---

Bob's nyx-agent aggregates Prometheus metrics from itself and all reachable backends into a single `/metrics` endpoint. This test verifies the aggregation is working correctly.

## Verification

Send a request first to ensure at least one task has been processed (so counters are non-zero):

```
curl -s -X POST http://localhost:8099/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"020","method":"message/send","params":{"message":{"role":"user","messageId":"msg-020","contextId":"metrics-test","parts":[{"kind":"text","text":"Respond with METRICS_TEST_OK."}]}}}'
```

Wait for the response to contain `METRICS_TEST_OK`.

Then fetch the aggregated metrics:

```
curl -s http://localhost:8099/metrics
```

## Pass/Fail Criteria

The test passes if ALL of the following are true:

1. `GET http://localhost:8099/metrics` returns HTTP 200.
2. The response body contains at least one `agent_` prefixed metric (e.g. `agent_a2a_requests_total`).
3. The response body contains at least one `a2_` prefixed metric with a `backend=` label (e.g. a line matching `a2_.*backend="`).
4. The response body does NOT contain a 500 error or HTML error page.

The test fails if any of the above conditions are not met.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

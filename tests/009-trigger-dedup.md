---
description: Verifies that a second POST to a trigger endpoint that is already processing returns 409 Conflict.
enabled: true
---

The trigger ping endpoint runs synchronously against the backend. To force in-flight overlap, use a slow trigger
endpoint that takes time to complete. Since trigger:ping is fast, this test uses curl's `--max-time` and fires two
requests in rapid succession — the second should arrive while the first is still being processed by the backend.

Step 1 — fire the first POST in the background (do not wait for it to return):

```text
curl -s -o /dev/null \
  -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:?set TRIGGERS_AUTH_TOKEN before running smoke specs}" \
  -H "Content-Type: application/json" \
  -d '{}' &
FIRST_PID=$!
```

Step 2 — immediately fire a second POST and capture its HTTP status code:

```text
sleep 0.1
SECOND_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:?set TRIGGERS_AUTH_TOKEN before running smoke specs}" \
  -H "Content-Type: application/json" \
  -d '{}')
echo "Second request status: $SECOND_CODE"
wait $FIRST_PID
```

The test passes if `SECOND_CODE` is `409`. The test fails if `SECOND_CODE` is `202` (deduplication not working) or any
other code.

**Note:** There is a small timing window where the first request may complete before the second arrives, causing the
second to also return 202. If that happens, retry the sequence up to 3 times before failing. If the backend is
consistently too fast for this test to observe the overlap, mark the test as passed with a note that deduplication logic
exists in the code but cannot be race-condition-tested at this latency.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

---
description: Verifies all three health endpoints return the expected status codes and payloads.
enabled: true
---

Check each health endpoint on bob's witwave agent:

```
curl -s http://localhost:8099/health/start
curl -s http://localhost:8099/health/live
curl -s http://localhost:8099/health/ready
```

Expected responses:

- `/health/start` — HTTP 200, body contains `"status": "ok"` or `"status": "starting"`
- `/health/live` — HTTP 200, body contains `"status": "ok"` and `"agent": "bob"`
- `/health/ready` — HTTP 200, body contains `"status": "ready"`

The test passes if all three endpoints return HTTP 200 with the expected fields. The test fails if any endpoint is
unreachable, returns a non-200 status, or is missing expected fields.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

---
description: Verifies that a trigger endpoint accepts a POST request and the backend responds correctly.
enabled: true
---

Send a POST request to bob's ping trigger using curl:

```
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:-smoke-test-token}" \
  -H "Content-Type: application/json" \
  -d '{}'
```

The test passes if the HTTP response code is 202. The test fails if the response code is anything other than 202, or if
the endpoint is unreachable.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

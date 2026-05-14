---
description: Verifies the trigger discovery endpoint returns the registered trigger list.
enabled: true
---

Fetch the trigger discovery endpoint:

```text
curl -s http://localhost:8099/.well-known/agent-triggers.json
```

The response must be a JSON array. Verify that it contains at least one entry with `"endpoint": "ping"` and that each
entry has the fields `endpoint`, `name`, `methods`, and `session_id`.

The test passes if the response is a valid JSON array containing an entry with `"endpoint": "ping"`. The test fails if
the endpoint is unreachable, returns non-JSON, returns an empty array, or the ping entry is missing.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

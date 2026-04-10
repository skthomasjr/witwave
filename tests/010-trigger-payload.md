---
description: Verifies that a trigger receives and correctly processes a JSON payload from the request body.
enabled: true
---

Send a POST request to bob's echo trigger with a JSON payload containing a unique token:

```
curl -s -X POST http://localhost:8099/triggers/echo \
  -H "Content-Type: application/json" \
  -d '{"token": "PAYLOAD_TEST_7x9q"}'
```

The trigger is asynchronous — it returns 202 immediately and the backend processes the payload in the background.

Wait up to 30 seconds for the backend to complete by polling the conversation log at:

```
.agents/test/bob/a2-codex/logs/conversation.log
```

Poll every 2 seconds until the string `PAYLOAD_TEST_7x9q` appears in the log, or until 30 seconds have elapsed.

The test passes if `PAYLOAD_TEST_7x9q` is found in the conversation log within 30 seconds.
The test fails if the string does not appear within 30 seconds, or if the trigger endpoint is unreachable.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

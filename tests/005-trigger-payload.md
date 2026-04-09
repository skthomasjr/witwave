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
Wait up to 30 seconds for the backend to complete by polling the session log.

To check whether the backend processed the payload, send a second A2A JSON-RPC request to bob's nyx agent at http://localhost:8099/ using the same deterministic session ID the trigger uses.

The trigger's session ID is the UUID5 of the string "bob.echo" in the URL namespace. Compute it:

```
python3 -c "import uuid; print(uuid.uuid5(uuid.NAMESPACE_URL, 'bob.echo'))"
```

Send an A2A JSON-RPC request to http://localhost:8099/ using that session ID with the prompt:
"What was the last token value you were asked to echo? Reply with just the token."

The test passes if the response contains PAYLOAD_TEST_7x9q.
The test fails if the response does not contain PAYLOAD_TEST_7x9q, is empty, or the agent is unreachable.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

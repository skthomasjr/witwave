---
description: Verifies that a job can be dispatched and the backend responds correctly.
enabled: true
---

Send an A2A JSON-RPC request to bob's nyx agent at http://localhost:8099/ with the prompt "Respond with JOB_OK."

The test passes if the response contains JOB_OK.
The test fails if the response is empty, contains an error, or the agent is unreachable.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

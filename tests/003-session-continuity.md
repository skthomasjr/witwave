---
description: Verifies that a backend maintains session state across multiple turns in the same session.
enabled: true
---

Send two sequential A2A JSON-RPC requests to bob's nyx agent at http://localhost:8099/, both using the same session ID.

First request: "Remember the word PINEAPPLE. Acknowledge with REMEMBERED."
Second request: "What word were you asked to remember? Respond with just the word."

The test passes if the second response contains PINEAPPLE.
The test fails if the second response does not contain PINEAPPLE, is empty, or the agent is unreachable.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

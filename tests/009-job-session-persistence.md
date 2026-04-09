---
description: Verifies that repeated job executions use the same deterministic session ID.
enabled: true
---

The job scheduler generates a deterministic session ID for each job derived from the agent name and job filename. For bob's ping job the session ID is always `fa977813-f68c-5013-9215-555337423f4d`.

This test verifies that the session ID appearing in the conversation log matches that deterministic value across multiple job runs.

Poll the conversation log at:

```
.agents/test/bob/a2-claude/logs/conversation.log
```

Poll every 5 seconds for up to 650 seconds until at least two entries containing `fa977813-f68c-5013-9215-555337423f4d` appear (one per job run). Two occurrences confirm the same session ID is reused across runs.

The test passes if the session ID `fa977813-f68c-5013-9215-555337423f4d` appears at least twice in the conversation log within 650 seconds.
The test fails if fewer than two occurrences appear within 650 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

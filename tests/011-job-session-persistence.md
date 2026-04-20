---
description: Verifies that repeated job executions use the same deterministic session ID.
enabled: true
---

The job scheduler generates a deterministic session ID for each job derived from the agent name and job filename. For a job file named `session-probe.md`, bob's session ID is always `uuid5(NAMESPACE_URL, "bob.session-probe")` = `b94e5f7c-6a0d-5bdd-a642-8469dbff89fe`.

This test creates a run-once job, waits for it to fire, recreates it to fire a second time, then verifies both entries in the conversation log share the same deterministic session ID.

## Setup

Create the run-once session probe job:

```
cat > .agents/test/bob/.witwave/jobs/session-probe.md << 'EOF'
---
name: Session Probe
description: Run-once job used to verify deterministic session ID persistence.
---
Respond with SESSION_PROBE_OK.
EOF
```

Wait 10 seconds for the job to fire and the response to land in the log.

## Fire again

Delete and recreate the file to trigger a second run:

```
rm .agents/test/bob/.witwave/jobs/session-probe.md

cat > .agents/test/bob/.witwave/jobs/session-probe.md << 'EOF'
---
name: Session Probe
description: Run-once job used to verify deterministic session ID persistence.
---
Respond with SESSION_PROBE_OK.
EOF
```

Wait 10 seconds for the second run.

## Cleanup

```
rm .agents/test/bob/.witwave/jobs/session-probe.md
```

## Verification

Check the conversation log at `.agents/test/bob/logs/conversation.jsonl`.

Count occurrences of `b94e5f7c-6a0d-5bdd-a642-8469dbff89fe` in the log. There must be at least 2.

## Pass/Fail Criteria

The test passes if the session ID `b94e5f7c-6a0d-5bdd-a642-8469dbff89fe` appears at least twice in the conversation log.
The test fails if fewer than two occurrences appear.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

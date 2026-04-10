---
description: Verifies that a continuation fires after an upstream job completes and the backend responds correctly.
enabled: true
---

This test creates a run-once job and a matching continuation, fires the job on startup, and verifies the continuation fires.

## Setup

Create a run-once job (no schedule — fires immediately when registered):

```
cat > .agents/test/bob/.nyx/jobs/continuation-probe.md << 'EOF'
---
name: Continuation Probe
description: Run-once job used to verify continuation wiring.
---
Respond with CONTINUATION_PROBE_JOB_OK.
EOF
```

Create a continuation that fires after the job:

```
cat > .agents/test/bob/.nyx/continuations/continuation-probe.md << 'EOF'
---
name: Continuation Probe
description: Fires after the run-once continuation-probe job.
continues-after: job:Continuation Probe
---
Respond with CONTINUATION_PROBE_OK.
EOF
```

Wait 5 seconds for the file watchers to register both files.

## Verification

Poll the conversation log at `.agents/test/bob/logs/conversation.jsonl` every 2 seconds for up to 60 seconds until `CONTINUATION_PROBE_OK` appears.

## Cleanup

```
rm .agents/test/bob/.nyx/jobs/continuation-probe.md
rm .agents/test/bob/.nyx/continuations/continuation-probe.md
```

## Pass/Fail Criteria

The test passes if `CONTINUATION_PROBE_OK` appears in the conversation log within 60 seconds.
The test fails if `CONTINUATION_PROBE_OK` does not appear within 60 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

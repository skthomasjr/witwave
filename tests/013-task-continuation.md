---
description: Verifies that a continuation fires after a run-once task completes, with trigger-when filtering.
enabled: true
---

This test creates a run-once task and a continuation that fires after it with a `trigger-when` filter. The continuation should only fire when the task response contains the expected string.

## Setup

Create the run-once task:

```
cat > .agents/test/bob/.witwave/tasks/task-continuation-probe.md << 'EOF'
---
name: Task Continuation Probe
description: Run-once task used to verify task-to-continuation wiring.
---
Respond with TASK_CONT_PROBE_OK.
EOF
```

Create a continuation that fires after the task (trigger-when matches the response):

```
cat > .agents/test/bob/.witwave/continuations/task-continuation-probe.md << 'EOF'
---
name: Task Continuation Probe
description: Fires after the run-once task-continuation-probe task.
continues-after: "task:Task Continuation Probe"
trigger-when: TASK_CONT_PROBE_OK
---
Respond with TASK_CONT_FIRED.
EOF
```

Create a continuation that should NOT fire (trigger-when does not match):

```
cat > .agents/test/bob/.witwave/continuations/task-continuation-probe-nomatch.md << 'EOF'
---
name: Task Continuation Probe No Match
description: Should not fire — trigger-when will not appear in the task response.
continues-after: "task:Task Continuation Probe"
trigger-when: THIS_STRING_WILL_NOT_APPEAR
---
Respond with TASK_CONT_NOMATCH_FIRED.
EOF
```

Wait 5 seconds for the file watchers to register all files.

## Verification

Poll the conversation log at `.agents/test/bob/logs/conversation.jsonl` every 2 seconds for up to 60 seconds.

## Cleanup

```
rm .agents/test/bob/.witwave/tasks/task-continuation-probe.md
rm .agents/test/bob/.witwave/continuations/task-continuation-probe.md
rm .agents/test/bob/.witwave/continuations/task-continuation-probe-nomatch.md
```

## Pass/Fail Criteria

The test passes if:
1. `TASK_CONT_FIRED` appears in the log within 60 seconds.
2. `TASK_CONT_NOMATCH_FIRED` does NOT appear in the log.

The test fails if `TASK_CONT_FIRED` is absent, or if `TASK_CONT_NOMATCH_FIRED` appears.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

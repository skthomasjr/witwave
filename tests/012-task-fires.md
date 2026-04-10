---
description: Smoke test — verifies the task scheduler fires a run-once task and the backend responds correctly.
enabled: true
---

This test creates a run-once task (no `window-start`) which fires immediately when registered.

## Setup

```
cat > .agents/test/bob/.nyx/tasks/task-smoke.md << 'EOF'
---
name: Task Smoke
description: Run-once task used to verify the task scheduler fires and the backend responds.
---
Respond with TASK_SMOKE_OK.
EOF
```

Wait 5 seconds for the file watcher to register the task.

## Verification

Poll the conversation log at `.agents/test/bob/a2-codex/logs/conversation.log` every 2 seconds for up to 60 seconds until `TASK_SMOKE_OK` appears.

## Cleanup

```
rm .agents/test/bob/.nyx/tasks/task-smoke.md
```

## Pass/Fail Criteria

The test passes if `TASK_SMOKE_OK` is found in the conversation log within 60 seconds.
The test fails if `TASK_SMOKE_OK` does not appear within 60 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

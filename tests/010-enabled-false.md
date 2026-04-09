---
description: Verifies that items with enabled:false are suppressed and never fire.
enabled: true
---

This test creates disabled fixtures for each feature type, waits to confirm they don't fire, then removes them.

## Setup

Create a disabled job file:

```
cat > .agents/test/bob/.nyx/jobs/disabled-test.md << 'EOF'
---
name: Disabled Job Test
description: Should never fire — enabled is false.
schedule: "* * * * *"
enabled: false
---
Respond with DISABLED_JOB_FIRED.
EOF
```

Create a disabled trigger file:

```
cat > .agents/test/bob/.nyx/triggers/disabled-test.md << 'EOF'
---
name: Disabled Trigger Test
description: Should never be reachable — enabled is false.
endpoint: disabled-test
enabled: false
---
Respond with DISABLED_TRIGGER_FIRED.
EOF
```

Create a disabled continuation file:

```
cat > .agents/test/bob/.nyx/continuations/disabled-test.md << 'EOF'
---
name: Disabled Continuation Test
description: Should never fire — enabled is false.
continues-after: "*"
enabled: false
---
Respond with DISABLED_CONTINUATION_FIRED.
EOF
```

## Verification

Wait 10 seconds for the file watchers to pick up the new files.

Then verify the disabled trigger endpoint is not reachable (returns 404):

```
CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/disabled-test \
  -H "Content-Type: application/json" \
  -d '{}')
echo "Disabled trigger status: $CODE"
```

The disabled trigger must return 404.

Wait a further 70 seconds (long enough for the disabled job's `* * * * *` schedule to fire if it were enabled).

Check the conversation log at `.agents/test/bob/a2-claude/logs/conversation.log` for any of these strings:
- `DISABLED_JOB_FIRED`
- `DISABLED_TRIGGER_FIRED`
- `DISABLED_CONTINUATION_FIRED`

## Cleanup

Remove the test fixtures:

```
rm .agents/test/bob/.nyx/jobs/disabled-test.md
rm .agents/test/bob/.nyx/triggers/disabled-test.md
rm .agents/test/bob/.nyx/continuations/disabled-test.md
```

## Pass/Fail Criteria

The test passes if ALL of the following are true:
1. The disabled trigger endpoint returned 404.
2. None of `DISABLED_JOB_FIRED`, `DISABLED_TRIGGER_FIRED`, or `DISABLED_CONTINUATION_FIRED` appear in the conversation log.

The test fails if the disabled trigger returned anything other than 404, or if any disabled sentinel string appears in the log.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

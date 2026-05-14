---
description: Verifies that continuations can chain — a continuation fires after another continuation completes.
enabled: false
deferred-note:
  "Deferred under CLI gitSync: this spec creates transient continuation files locally; convert to precommitted fixtures
  before re-enabling."
---

This test creates a two-level continuation chain: the trigger ping fires, which triggers a first continuation, which in
turn triggers a second continuation.

## Setup

Create the first continuation (fires after trigger:ping):

```text
cat > .agents/test/bob/.witwave/continuations/chain-1.md << 'EOF'
---
name: Chain Step 1
description: First link in the chain — fires after trigger:ping.
continues-after: trigger:ping
---
Respond with CHAIN_STEP_1_OK.
EOF
```

Create the second continuation (fires after continuation:Chain Step 1):

```text
cat > .agents/test/bob/.witwave/continuations/chain-2.md << 'EOF'
---
name: Chain Step 2
description: Second link in the chain — fires after continuation:Chain Step 1.
continues-after: "continuation:Chain Step 1"
---
Respond with CHAIN_STEP_2_OK.
EOF
```

Wait 5 seconds for the file watcher to register both continuations.

## Fire the upstream trigger

```text
curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:?set TRIGGERS_AUTH_TOKEN before running smoke specs}" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Verify the response is 202 before proceeding.

## Poll for both chain steps

Poll the conversation log at `ww conversation list --namespace witwave-test --agent bob --expand` every 2 seconds for up
to 60 seconds until both `CHAIN_STEP_1_OK` and `CHAIN_STEP_2_OK` appear.

## Cleanup

```text
rm .agents/test/bob/.witwave/continuations/chain-1.md
rm .agents/test/bob/.witwave/continuations/chain-2.md
```

## Pass/Fail Criteria

The test passes if both `CHAIN_STEP_1_OK` and `CHAIN_STEP_2_OK` appear in the conversation log within 60 seconds of
firing the trigger. The test fails if either token is missing after 60 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

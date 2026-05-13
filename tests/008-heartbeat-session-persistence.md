---
description: Verifies that the heartbeat reuses the same deterministic session ID across multiple firings.
enabled: false
deferred-note: "Deferred under CLI gitSync: this spec mutates HEARTBEAT.md locally; convert to a precommitted fixture or push through the gitSync source before re-enabling."
---

The heartbeat uses a deterministic session ID derived from the agent name: `uuid5(NAMESPACE_URL, "bob.heartbeat")` =
`9f058a6c-e2cf-5618-8e86-dd403353bbcf`.

This test verifies that the session ID appearing in the conversation log for heartbeat entries matches that
deterministic value across multiple heartbeat runs.

Bob's heartbeat is scheduled at `0 * * * *` (every hour). To avoid waiting an hour, this test temporarily replaces the
heartbeat schedule with a faster one, waits for two firings, then restores the original.

## Setup — replace heartbeat with fast schedule

```
cp .agents/test/bob/.witwave/HEARTBEAT.md .agents/test/bob/.witwave/HEARTBEAT.md.bak

cat > .agents/test/bob/.witwave/HEARTBEAT.md << 'EOF'
---
description: Fast heartbeat for session persistence test.
schedule: "* * * * *"
enabled: true
---
Respond with HEARTBEAT_SESSION_TEST_OK.
EOF
```

Wait 5 seconds for the file watcher to reload the heartbeat.

## Poll for two firings with the same session ID

Poll the conversation log at `ww conversation list --namespace witwave-test --agent bob --expand` every 5 seconds for up to 150 seconds until the
session ID `9f058a6c-e2cf-5618-8e86-dd403353bbcf` appears at least twice.

## Restore original heartbeat

```
mv .agents/test/bob/.witwave/HEARTBEAT.md.bak .agents/test/bob/.witwave/HEARTBEAT.md
```

## Pass/Fail Criteria

The test passes if the session ID `9f058a6c-e2cf-5618-8e86-dd403353bbcf` appears at least twice in the conversation log
within 150 seconds. The test fails if fewer than two occurrences appear within 150 seconds.

**If the failure is caused by a code bug in the system under test, do not fix it — mark the test as failed and report
the issue. Only fix tooling or execution problems that prevent the test itself from running.**

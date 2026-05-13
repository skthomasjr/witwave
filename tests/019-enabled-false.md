---
description: Verifies that precommitted fixtures with enabled:false stay suppressed in the default smoke deployment.
enabled: true
---

Bob carries several Codex fixtures under `.agents/test/bob/.witwave/jobs/` and `.agents/test/bob/.witwave/continuations/` with `enabled: false`. They are deliberately parked while the default test deployment is Claude-only.

## Verification

Inspect Bob's conversation evidence:

```bash
ww conversation list --namespace witwave-test --agent bob --expand
```

Check that none of these disabled-fixture markers appear:

- `animal-memory-codex`
- `backend-check-codex`
- `model-check-codex-default`
- `model-check-codex-gpt-5-3-codex`
- `model-check-codex-gpt-5-5`
- `ping-codex`
- `bob-codex`

## Pass/Fail Criteria

The test passes if none of the disabled Codex fixtures appear in Bob's conversation log. It fails if any Codex fixture fires or any entry is attributed to `bob-codex` in the default deployment.

**If the failure is caused by a code bug in the system under test, do not fix it; mark the test as failed and report the issue. Only fix tooling or execution problems that prevent the test itself from running.**

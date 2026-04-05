---
status: idle
locked_by: null
locked_at: null
---

# TODO

## Bugs

✨ _All clear_

## Reliability

✨ _All clear_

## Code Quality

✨ _All clear_

## Enhancements

- [ ] [#15] Add `agent_sdk_subprocess_spawn_duration_seconds` Histogram metric — time the `ClaudeSDKClient.__aenter__()`
      call in `run_query()` (`executor.py` line 261) to isolate SDK subprocess spawn latency from query processing.
      Declare in `metrics.py`, observe as `time.monotonic() - _spawn_start` at the top of the `async with` block body.
      Surfaces a key latency component currently hidden inside `agent_sdk_session_duration_seconds`.

---
status: fixing
locked_by: kira
locked_at: 2026-04-05T15:30:18Z
---

# TODO

## Bugs

- [ ] [#14] `agent_session_idle_seconds` reports session age instead of idle time (`executor.py` line 352).
      `_track_session()` (line 142) calls `move_to_end()` on reuse but never updates the stored `time.monotonic()`
      timestamp. The metric computes `time.monotonic() - sessions[session_id]` which yields total session age, not time
      since last use. Fix: update `sessions[session_id] = time.monotonic()` after `move_to_end()` in `_track_session()`,
      and rename the eviction metric variable from `created_at` to `last_used_at` to reflect the new semantics.

## Reliability

✨ _All clear_

## Code Quality

✨ _All clear_

## Enhancements

- [ ] [#15] Add `agent_sdk_subprocess_spawn_duration_seconds` Histogram metric — time the `ClaudeSDKClient.__aenter__()`
      call in `run_query()` (`executor.py` line 261) to isolate SDK subprocess spawn latency from query processing.
      Declare in `metrics.py`, observe as `time.monotonic() - _spawn_start` at the top of the `async with` block body.
      Surfaces a key latency component currently hidden inside `agent_sdk_session_duration_seconds`.

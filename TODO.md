---
status: fixing
locked_by: kira
locked_at: 2026-04-05T05:04:06Z
---

# TODO

## Bugs

✨ _All clear_

## Reliability

✨ _All clear_

## Code Quality

✨ _All clear_

## Enhancements

- [ ] Add `agent_task_last_error_timestamp_seconds` Gauge (no labels) to track the Unix epoch of the most recent failed
      task execution. Define in `metrics.py`, import in `executor.py`, and observe at the four error paths inside
      `_run_inner()` (lines 358, 373, 379, 386) alongside the existing `agent_task_error_duration_seconds` calls using
      `time.time()`. Document in `README.md`. Completes the last-error-timestamp pattern across all three execution
      contexts (heartbeat, agenda, tasks).

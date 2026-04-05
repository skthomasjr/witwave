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

- [ ] Add `agent_heartbeat_last_error_timestamp_seconds` Gauge (no labels) to track the Unix epoch of the most recent
      failed heartbeat. Mirrors the existing `agent_heartbeat_last_success_timestamp_seconds`. Define in `metrics.py`,
      import in `heartbeat.py`, and observe at line 127 on the error path inside `_run_loop()` using `time.time()`.
      Document in `README.md`.

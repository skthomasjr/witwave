---
status: reviewing
locked_by: iris
locked_at: "2026-04-05T06:22:56Z"
---

# TODO

## Bugs

✨ _All clear_

## Reliability

✨ _All clear_

## Code Quality

✨ _All clear_

## Enhancements

- [ ] Add `agent_bus_error_processing_duration_seconds` histogram (labeled by `kind`) to record the duration of bus
      message processing when it ends in an error. This completes the error-duration pattern already established for
      tasks, heartbeats, and agenda items — the bus worker in `main.py` `bus_worker()` is the only execution path
      without a dedicated error-duration metric. Observe `time.monotonic() - t0` inside the `except Exception` block.

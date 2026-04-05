---
status: reviewing
locked_by: iris
locked_at: "2026-04-05T02:33:35Z"
---

# TODO

## Bugs

✨ _All clear_

## Reliability

✨ _All clear_

## Code Quality

✨ _All clear_

## Enhancements

- [ ] **Metric: `agent_lru_cache_utilization_percent`** — Gauge that reports
  `len(sessions) / MAX_SESSIONS * 100` after each `_track_session()` call in
  `executor.py`. Shows how close the LRU cache is to the eviction threshold,
  making it immediately actionable in dashboards without needing to know the
  configured `MAX_SESSIONS` value. Define in `metrics.py` as a Gauge with no
  labels. Set in `_track_session()` right after the existing
  `agent_active_sessions.set()` call (executor.py line 138).

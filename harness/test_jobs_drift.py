"""Unit tests for job cron drift behaviour (#860).

Mirrors test_heartbeat_drift.py (#659) but for harness/jobs.py, which
previously constructed a single persistent ``croniter`` cursor outside
the loop and advanced it exactly one tick per iteration. Under job
overruns, reload errors, system sleep, or NTP step adjustments the
cursor could fall behind wall-clock, producing delayed or back-to-back
fires.

These tests exercise the tick-anchoring logic in isolation so the
regression is guarded by a deterministic check rather than a live
scheduler run.
"""

from datetime import datetime, timedelta, timezone

from croniter import croniter


def _compute_next(last_scheduled: datetime | None, now: datetime, schedule: str) -> datetime:
    """Mirror the anchoring logic in run_job (harness/jobs.py).

    Each iteration anchors the croniter at ``max(now, last_scheduled)``
    and returns the next tick. Keeping this helper in the test file
    keeps the regression guard self-contained and doesn't require
    importing the scheduler module, which pulls in metric/bus
    dependencies.
    """
    anchor = now if last_scheduled is None else max(now, last_scheduled)
    return croniter(schedule, anchor).get_next(datetime)


def test_no_drift_when_job_run_overruns_a_tick():
    """A long-running job that overruns one tick should still target the
    NEXT tick relative to wall-clock, not the one we already missed."""
    schedule = "*/1 * * * *"  # every minute
    t0 = datetime(2026, 4, 18, 12, 0, 0, tzinfo=timezone.utc)

    # First iteration at 12:00 → next_run = 12:01.
    next1 = _compute_next(None, t0, schedule)
    assert next1 == datetime(2026, 4, 18, 12, 1, 0, tzinfo=timezone.utc)

    # Simulate overrun: job body took 90s so the next iteration begins
    # at 12:02:30 (thirty seconds past the 12:02 tick).
    now_late = datetime(2026, 4, 18, 12, 2, 30, tzinfo=timezone.utc)
    next2 = _compute_next(next1, now_late, schedule)
    # Anchor is max(12:02:30, 12:01) = 12:02:30 → next match is 12:03.
    assert next2 == datetime(2026, 4, 18, 12, 3, 0, tzinfo=timezone.utc)


def test_small_backward_clock_skew_does_not_double_fire():
    """If wall-clock moves backwards slightly between iterations (NTP
    jitter), the last_scheduled anchor must prevent firing the same
    tick twice."""
    schedule = "*/5 * * * *"

    last_scheduled = datetime(2026, 4, 18, 12, 5, 0, tzinfo=timezone.utc)
    now_skewed_back = datetime(2026, 4, 18, 12, 4, 59, 900_000, tzinfo=timezone.utc)
    nxt = _compute_next(last_scheduled, now_skewed_back, schedule)
    # Anchor = max(12:04:59.9, 12:05) = 12:05 → next tick is 12:10.
    assert nxt == datetime(2026, 4, 18, 12, 10, 0, tzinfo=timezone.utc)


def test_long_suspend_catches_up_to_next_future_tick():
    """After a system suspend that crosses many ticks, the next fire
    should align with the next FUTURE tick — no backlog replay."""
    schedule = "*/5 * * * *"

    last_scheduled = datetime(2026, 4, 18, 12, 0, 0, tzinfo=timezone.utc)
    now_resumed = datetime(2026, 4, 18, 13, 2, 0, tzinfo=timezone.utc)
    nxt = _compute_next(last_scheduled, now_resumed, schedule)
    # Anchor = max(13:02, 12:00) = 13:02 → next match is 13:05.
    assert nxt == datetime(2026, 4, 18, 13, 5, 0, tzinfo=timezone.utc)


def test_steady_state_fires_each_tick_with_no_drift():
    """Iterations that complete well within one tick should produce the
    expected cadence with no drift."""
    schedule = "*/5 * * * *"

    last = None
    now = datetime(2026, 4, 18, 12, 0, 0, tzinfo=timezone.utc)
    fires = []
    for _ in range(4):
        nxt = _compute_next(last, now, schedule)
        fires.append(nxt)
        last = nxt
        now = nxt + timedelta(milliseconds=50)

    assert fires == [
        datetime(2026, 4, 18, 12, 5, 0, tzinfo=timezone.utc),
        datetime(2026, 4, 18, 12, 10, 0, tzinfo=timezone.utc),
        datetime(2026, 4, 18, 12, 15, 0, tzinfo=timezone.utc),
        datetime(2026, 4, 18, 12, 20, 0, tzinfo=timezone.utc),
    ]


if __name__ == "__main__":  # pragma: no cover
    test_no_drift_when_job_run_overruns_a_tick()
    test_small_backward_clock_skew_does_not_double_fire()
    test_long_suspend_catches_up_to_next_future_tick()
    test_steady_state_fires_each_tick_with_no_drift()
    print("all jobs drift tests passed")

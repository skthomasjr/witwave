"""Unit tests for shared/hook_events.py (#779)."""

import asyncio
import importlib
import sys
from pathlib import Path

import pytest

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


class _CountCounter:
    """Minimal stand-in for a Prometheus Counter — only needs .inc()."""

    def __init__(self) -> None:
        self.count = 0

    def inc(self, amount: int = 1) -> None:
        self.count += amount


def _reload(monkeypatch, url: str = "", token: str = "", cap: str = "32"):
    """Reload shared.hook_events with specific env so module-level
    constants are re-evaluated."""
    monkeypatch.setenv("HARNESS_EVENTS_URL", url)
    monkeypatch.setenv("HARNESS_EVENTS_AUTH_TOKEN", token)
    monkeypatch.setenv("TRIGGERS_AUTH_TOKEN", "")
    monkeypatch.setenv("HOOK_POST_MAX_INFLIGHT", cap)
    import hook_events  # type: ignore

    importlib.reload(hook_events)
    return hook_events


def test_no_url_returns_false_silently(monkeypatch):
    he = _reload(monkeypatch, url="")
    ok = he.schedule_post({"agent": "a", "tool": "shell", "decision": "deny"})
    assert ok is False
    assert len(he._INFLIGHT) == 0


def test_post_is_scheduled_when_url_and_token_set(monkeypatch):
    async def run():
        he = _reload(monkeypatch, url="http://harness", token="s")
        ok = he.schedule_post({"agent": "a", "tool": "shell", "decision": "deny"})
        assert ok is True
        # Wait for the task to finish (it will fail to POST since the
        # host doesn't resolve, but the failure path is what we want
        # covered — no raise, task cleans up).
        pending = list(he._INFLIGHT)
        await asyncio.gather(*pending, return_exceptions=True)
        # Done-callback discards from the set.
        assert len(he._INFLIGHT) == 0

    asyncio.run(run())


def test_shed_bumps_counter(monkeypatch):
    async def run():
        he = _reload(monkeypatch, url="http://harness", token="s", cap="2")
        # Pre-fill the inflight set so the cap is tripped immediately.
        # Use never-completing futures so the set stays full.
        loop = asyncio.get_running_loop()
        fake1 = loop.create_future()
        fake2 = loop.create_future()
        he._INFLIGHT.add(fake1)
        he._INFLIGHT.add(fake2)

        counter = _CountCounter()
        ok = he.schedule_post(
            {"agent": "a", "tool": "shell", "decision": "deny"},
            shed_counter=counter,
        )
        assert ok is False, "cap=2 with 2 in flight should shed"
        assert counter.count == 1, "shed_counter should be incremented once"

        # Clean up the fakes so pytest doesn't complain.
        fake1.set_result(None)
        fake2.set_result(None)
        he._INFLIGHT.discard(fake1)
        he._INFLIGHT.discard(fake2)

    asyncio.run(run())


def test_missing_token_logs_once(monkeypatch, caplog):
    import logging

    async def run():
        he = _reload(monkeypatch, url="http://harness", token="")
        with caplog.at_level(logging.WARNING, logger="hook_events"):
            he.schedule_post({"agent": "a"})
            he.schedule_post({"agent": "b"})
            he.schedule_post({"agent": "c"})
            # Let the scheduled tasks run _post_once which triggers the
            # missing-token warning path.
            pending = list(he._INFLIGHT)
            await asyncio.gather(*pending, return_exceptions=True)
        warnings = [r for r in caplog.records if r.levelno == logging.WARNING]
        assert len(warnings) == 1, (
            "missing-token warning must fire exactly once per process "
            "to avoid log flood on sustained misconfig"
        )

    asyncio.run(run())


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

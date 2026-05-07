"""Unit tests for background-task shed WARN log emission (#1644).

When ``AgentExecutor.track_background`` sheds a task because the in-flight
pool is at capacity, only ``harness_background_tasks_shed_total`` was being
incremented. There was no log line and no event, so operators had no
real-time visibility into drops — drops only showed up on a Prometheus
dashboard, which is too coarse for "is this happening right now while I'm
debugging?" Issue #1644 adds a per-shed WARN with session_id + caller
identity, rate-limited per-source so a sustained drop doesn't flood logs.

This test asserts the WARN fires on the first shed in a window and carries
the session_id + caller_identity passed to ``track_background``.
"""

from __future__ import annotations

import logging
import sys
import types
from pathlib import Path

import pytest

# Make harness/ importable like sibling tests.
_HARNESS = Path(__file__).resolve().parent
if str(_HARNESS) not in sys.path:
    sys.path.insert(0, str(_HARNESS))
# Make shared/ importable so executor.py can resolve `from log_utils import …`
# and friends when imported under a bare pytest run (no Docker image).
_SHARED = _HARNESS.parent / "shared"
if str(_SHARED) not in sys.path:
    sys.path.insert(0, str(_SHARED))


def _stub_module(name: str, **attrs: object) -> types.ModuleType:
    """Register a placeholder module under ``name`` so ``import name`` works."""
    parts = name.split(".")
    for i in range(len(parts)):
        sub = ".".join(parts[: i + 1])
        if sub not in sys.modules:
            sys.modules[sub] = types.ModuleType(sub)
    mod = sys.modules[name]
    for k, v in attrs.items():
        setattr(mod, k, v)
    return mod


# The ``a2a`` SDK and a few cluster-only deps aren't installed in the bare
# local env; this test only exercises the shed-decision branch, which doesn't
# touch any of them. Stub the import surface so executor.py loads.
class _A2AAgentExecutorStub:  # noqa: D401 — minimal placeholder
    """Placeholder for a2a.server.agent_execution.AgentExecutor."""


_stub_module("a2a")
_stub_module("a2a.server")
_stub_module("a2a.server.agent_execution", AgentExecutor=_A2AAgentExecutorStub, RequestContext=object)
_stub_module("a2a.server.events", EventQueue=object)
_stub_module("a2a.utils", new_agent_text_message=lambda *a, **kw: None)


def _make_executor_stub():
    """Build an AgentExecutor without running __init__ (which loads backend.yaml).

    track_background only touches self._background_tasks for the shed-path
    decision, so the rest of the executor state is irrelevant here.
    """
    import executor as ex_mod

    obj = ex_mod.AgentExecutor.__new__(ex_mod.AgentExecutor)
    obj._background_tasks = set()
    return obj, ex_mod


class _FakeTask:
    """Stand-in for an asyncio.Task returning a stable name from get_name."""

    def __init__(self, name: str = "bg-fake") -> None:
        self._name = name

    def get_name(self) -> str:
        return self._name


def test_shed_emits_warn_with_session_and_caller(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    obj, ex_mod = _make_executor_stub()

    # Force shed: cap of 1, occupy with one fake task.
    monkeypatch.setattr(ex_mod, "BACKGROUND_TASKS_MAX", 1)
    # Reset rate-limit state so prior tests don't suppress the WARN.
    ex_mod._background_shed_log_state.clear()
    obj._background_tasks.add(_FakeTask("bg-existing"))

    async def _noop() -> None:
        return None

    coro = _noop()
    caplog.set_level(logging.WARNING)

    result = obj.track_background(
        coro,
        source="a2a",
        session_id="sess-abc",
        caller_identity="user-xyz",
    )

    assert result is None, "track_background must return None on shed"

    # The new #1644 WARN line carries source / session / caller. Match on the
    # message format produced by logger.warning(...).
    matching = [
        rec
        for rec in caplog.records
        if "background task shed:" in rec.getMessage()
        and "source=a2a" in rec.getMessage()
        and "session=sess-abc" in rec.getMessage()
        and "caller=user-xyz" in rec.getMessage()
    ]
    assert matching, (
        "expected a WARN log with 'background task shed: source=... session=... "
        f"caller=...'; got messages={[r.getMessage() for r in caplog.records]!r}"
    )
    assert matching[0].levelno == logging.WARNING


def test_shed_warn_rate_limited_per_source(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Subsequent sheds in the same window must not re-emit the per-shed WARN."""
    obj, ex_mod = _make_executor_stub()
    monkeypatch.setattr(ex_mod, "BACKGROUND_TASKS_MAX", 1)
    monkeypatch.setattr(ex_mod, "_BACKGROUND_SHED_LOG_WINDOW_SEC", 60.0)
    ex_mod._background_shed_log_state.clear()
    obj._background_tasks.add(_FakeTask("bg-existing"))

    async def _noop() -> None:
        return None

    caplog.set_level(logging.WARNING)

    for i in range(3):
        obj.track_background(
            _noop(),
            source="a2a",
            session_id=f"sess-{i}",
            caller_identity="caller-x",
        )

    per_shed_msgs = [
        rec.getMessage() for rec in caplog.records if "background task shed: source=a2a session=" in rec.getMessage()
    ]
    # Exactly one per-shed line in the window — others suppressed and counted.
    assert len(per_shed_msgs) == 1, f"expected 1 per-shed WARN within the window; got {per_shed_msgs!r}"


def test_shed_coro_close_failure_logs_warn(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """#1670: when coro.close() raises in the shed path, surface a WARN
    instead of silently swallowing the exception so operators can see
    close-failure events.
    """
    from unittest.mock import MagicMock

    obj, ex_mod = _make_executor_stub()
    monkeypatch.setattr(ex_mod, "BACKGROUND_TASKS_MAX", 1)
    ex_mod._background_shed_log_state.clear()
    obj._background_tasks.add(_FakeTask("bg-existing"))

    # MagicMock coro that explodes on close(). The shed path calls coro.close()
    # exactly once after deciding to drop the task.
    fake_coro = MagicMock(name="fake_coro")
    fake_coro.close.side_effect = RuntimeError("boom-on-close")

    caplog.set_level(logging.WARNING)

    result = obj.track_background(
        fake_coro,
        source="a2a",
        session_id="sess-close-fail",
        caller_identity="caller-y",
    )

    assert result is None, "track_background must return None on shed"
    fake_coro.close.assert_called_once()

    matching = [
        rec
        for rec in caplog.records
        if "background-task close after shed failed" in rec.getMessage()
        and "source=a2a" in rec.getMessage()
        and "boom-on-close" in rec.getMessage()
    ]
    assert matching, (
        "expected a WARN log naming the source and the close exception; "
        f"got messages={[r.getMessage() for r in caplog.records]!r}"
    )
    assert matching[0].levelno == logging.WARNING

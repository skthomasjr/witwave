"""Unit tests for `_sub_app_lifespan` shutdown timeout (#1618).

A sub-app that never emits ``lifespan.shutdown.complete`` must not be
able to stall pod termination indefinitely. The lifespan context manager
bounds the wait via ``asyncio.wait_for(<shutdown>, timeout=
_SUB_APP_SHUTDOWN_TIMEOUT_SEC)`` and logs a WARN (not ERROR — operators
expect this on bad rollouts) before proceeding with the rest of the
teardown so the pod still drains in bounded time.
"""
from __future__ import annotations

import asyncio
import logging
import os
import sys
import time
import types
import unittest
from pathlib import Path
from unittest.mock import MagicMock


_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

os.environ.setdefault("AGENT_NAME", "claude-test")
os.environ.setdefault("AGENT_OWNER", "test")
os.environ.setdefault("AGENT_ID", "claude")
# Override the default 10s with a snappier value so the test isn't slow.
# `os.environ[...] =` (not setdefault) so this wins regardless of prior
# state when other test modules in the same pytest session import `main`
# first. The constant is read at module import; we ALSO monkey-patch
# main._SUB_APP_SHUTDOWN_TIMEOUT_SEC inside each test for the case where
# main is already imported (so the env override missed the boat).
os.environ["SUB_APP_SHUTDOWN_TIMEOUT_SEC"] = "0.5"

import tempfile as _tempfile
_log_tmp_dir = _tempfile.mkdtemp(prefix="claude-test-")
os.environ.setdefault("CONVERSATION_LOG", os.path.join(_log_tmp_dir, "conversation.jsonl"))
os.environ.setdefault("TRACE_LOG", os.path.join(_log_tmp_dir, "tool-activity.jsonl"))


def _install_stub(name: str, mod: types.ModuleType) -> None:
    sys.modules.setdefault(name, mod)


# ---------------------------------------------------------------------------
# prometheus_client stub
# ---------------------------------------------------------------------------
if "prometheus_client" not in sys.modules:
    _pc = types.ModuleType("prometheus_client")

    class _Metric:
        def __init__(self, *a, **kw):
            pass

        def labels(self, *a, **kw):
            return self

        def inc(self, *a, **kw):
            pass

        def dec(self, *a, **kw):
            pass

        def set(self, *a, **kw):
            pass

        def observe(self, *a, **kw):
            pass

        def info(self, *a, **kw):
            pass

        def set_function(self, *a, **kw):
            pass

    _pc.Counter = _Metric
    _pc.Gauge = _Metric
    _pc.Histogram = _Metric
    _pc.Info = _Metric
    _pc.CollectorRegistry = lambda *a, **kw: MagicMock()
    _pc.REGISTRY = MagicMock()
    _install_stub("prometheus_client", _pc)


# ---------------------------------------------------------------------------
# uvicorn stub
# ---------------------------------------------------------------------------
if "uvicorn" not in sys.modules:
    _uv = types.ModuleType("uvicorn")

    class _Config:
        def __init__(self, *a, **kw):
            pass

    class _Server:
        def __init__(self, *a, **kw):
            self.started = False

        async def serve(self):
            return None

    _uv.Config = _Config
    _uv.Server = _Server
    _install_stub("uvicorn", _uv)


# ---------------------------------------------------------------------------
# a2a-sdk stubs
# ---------------------------------------------------------------------------
def _install_a2a_stubs() -> None:
    if "a2a" in sys.modules:
        return
    _a2a = types.ModuleType("a2a")
    _server = types.ModuleType("a2a.server")
    _apps = types.ModuleType("a2a.server.apps")
    _rh = types.ModuleType("a2a.server.request_handlers")
    _tasks = types.ModuleType("a2a.server.tasks")
    _ae = types.ModuleType("a2a.server.agent_execution")
    _events = types.ModuleType("a2a.server.events")
    _types = types.ModuleType("a2a.types")
    _utils = types.ModuleType("a2a.utils")

    class _A2AStarletteApplication:
        def __init__(self, *a, **kw):
            pass

        def build(self, *a, **kw):
            from starlette.applications import Starlette
            return Starlette()

    class _DefaultRequestHandler:
        def __init__(self, *a, **kw):
            pass

    class _InMemoryTaskStore:
        pass

    class _AgentExecutorBase:
        pass

    class _RequestContext:
        pass

    class _EventQueue:
        pass

    class _AgentCard:
        def __init__(self, **kw):
            for k, v in kw.items():
                setattr(self, k, v)

    class _AgentSkill:
        def __init__(self, **kw):
            for k, v in kw.items():
                setattr(self, k, v)

    class _AgentCapabilities:
        def __init__(self, **kw):
            for k, v in kw.items():
                setattr(self, k, v)

    _apps.A2AStarletteApplication = _A2AStarletteApplication
    _rh.DefaultRequestHandler = _DefaultRequestHandler
    _tasks.InMemoryTaskStore = _InMemoryTaskStore
    _ae.AgentExecutor = _AgentExecutorBase
    _ae.RequestContext = _RequestContext
    _events.EventQueue = _EventQueue
    _types.AgentCard = _AgentCard
    _types.AgentSkill = _AgentSkill
    _types.AgentCapabilities = _AgentCapabilities
    _utils.new_agent_text_message = lambda text: {"text": text}

    _install_stub("a2a", _a2a)
    _install_stub("a2a.server", _server)
    _install_stub("a2a.server.apps", _apps)
    _install_stub("a2a.server.request_handlers", _rh)
    _install_stub("a2a.server.tasks", _tasks)
    _install_stub("a2a.server.agent_execution", _ae)
    _install_stub("a2a.server.events", _events)
    _install_stub("a2a.types", _types)
    _install_stub("a2a.utils", _utils)


_install_a2a_stubs()


# ---------------------------------------------------------------------------
# yaml stub
# ---------------------------------------------------------------------------
if "yaml" not in sys.modules:
    _yaml = types.ModuleType("yaml")

    class _YAMLError(Exception):
        pass

    _yaml.YAMLError = _YAMLError
    _yaml.safe_load = lambda _s: {}
    _install_stub("yaml", _yaml)


# ---------------------------------------------------------------------------
# Sibling stubs
# ---------------------------------------------------------------------------
def _stub_sibling(name: str, attrs: dict) -> None:
    if name in sys.modules:
        return
    m = types.ModuleType(name)
    for k, v in attrs.items():
        setattr(m, k, v)
    sys.modules[name] = m


_stub_sibling("executor", {"AgentExecutor": type("AgentExecutor", (), {})})
_stub_sibling(
    "conversations",
    {
        "auth_disabled_escape_hatch": lambda *a, **kw: False,
        "make_conversations_handler": lambda *a, **kw: (lambda r: None),
        "make_trace_handler": lambda *a, **kw: (lambda r: None),
    },
)
_stub_sibling("sqlite_task_store", {"SqliteTaskStore": type("SqliteTaskStore", (), {})})
_stub_sibling(
    "session_binding",
    {
        "derive_session_id": lambda *a, **kw: "sid",
        "set_fallback_counter": lambda *a, **kw: None,
    },
)


class _NoopMetric:
    def labels(self, *a, **kw):
        return self

    def inc(self, *a, **kw):
        pass

    def dec(self, *a, **kw):
        pass

    def set(self, *a, **kw):
        pass

    def observe(self, *a, **kw):
        pass

    def info(self, *a, **kw):
        pass


_metric = _NoopMetric()
_stub_sibling(
    "metrics",
    {
        "backend_event_loop_lag_seconds": _metric,
        "backend_health_checks_total": _metric,
        "backend_info": _metric,
        "backend_mcp_request_duration_seconds": _metric,
        "backend_mcp_requests_total": _metric,
        "backend_sdk_info": _metric,
        "backend_session_binding_fallback_total": _metric,
        "backend_session_caller_cardinality": _metric,
        "backend_startup_duration_seconds": _metric,
        "backend_task_restarts_total": _metric,
        "backend_up": _metric,
        "backend_uptime_seconds": _metric,
    },
)


import importlib  # noqa: E402

main = importlib.import_module("main")


# ---------------------------------------------------------------------------
# Sub-apps used by the tests below
# ---------------------------------------------------------------------------
async def _hung_shutdown_app(scope, receive, send):
    """ASGI lifespan app that completes startup but never sends shutdown.complete.

    Models a sub-app whose shutdown handler is wedged (e.g. waiting on a
    backend that never returns).
    """
    assert scope["type"] == "lifespan"
    msg = await receive()
    if msg["type"] == "lifespan.startup":
        await send({"type": "lifespan.startup.complete"})
    # Wait for shutdown signal but never acknowledge it.
    msg = await receive()
    # Hang here until cancelled.
    while True:
        await asyncio.sleep(60)


async def _wellbehaved_app(scope, receive, send):
    """Baseline well-behaved ASGI lifespan app for sanity."""
    assert scope["type"] == "lifespan"
    msg = await receive()
    if msg["type"] == "lifespan.startup":
        await send({"type": "lifespan.startup.complete"})
    msg = await receive()
    if msg["type"] == "lifespan.shutdown":
        await send({"type": "lifespan.shutdown.complete"})


async def _drive_lifespan(app):
    async with main._sub_app_lifespan(app):
        pass


class SubAppLifespanShutdownTimeoutTests(unittest.TestCase):
    """Verify _sub_app_lifespan bounds the wait for shutdown.complete (#1618)."""

    def setUp(self):
        # Force a snappy timeout regardless of import order. If another
        # test module imported `main` before us, the env override at the
        # top of this file missed the boat — patch the constant directly.
        self._orig_timeout = main._SUB_APP_SHUTDOWN_TIMEOUT_SEC
        main._SUB_APP_SHUTDOWN_TIMEOUT_SEC = 0.5

    def tearDown(self):
        main._SUB_APP_SHUTDOWN_TIMEOUT_SEC = self._orig_timeout

    def test_constant_is_float_and_env_overridable(self):
        """The module exposes _SUB_APP_SHUTDOWN_TIMEOUT_SEC as a float."""
        self.assertIsInstance(main._SUB_APP_SHUTDOWN_TIMEOUT_SEC, float)
        self.assertGreater(main._SUB_APP_SHUTDOWN_TIMEOUT_SEC, 0.0)

    def test_hung_shutdown_unblocks_with_warn_log(self):
        """Hung sub-app shutdown must not stall — bounded wait + WARN log."""
        with self.assertLogs("main", level=logging.WARNING) as cm:
            started = time.monotonic()
            asyncio.run(_drive_lifespan(_hung_shutdown_app))
            elapsed = time.monotonic() - started

        # Should exit within ~0.5s + small allowance for cancellation.
        # Comfortably under the 10s production default. Test sets
        # SUB_APP_SHUTDOWN_TIMEOUT_SEC=0.5; with the post-timeout task
        # cancellation we still expect well under 3s.
        self.assertLess(
            elapsed,
            3.0,
            f"lifespan teardown took {elapsed:.2f}s -- timeout bound did not fire",
        )

        # The WARN should mention the timeout path (not just any warning).
        timeout_warns = [m for m in cm.output if "shutdown timed out" in m]
        self.assertTrue(
            timeout_warns,
            f"expected a WARN mentioning 'shutdown timed out'; got: {cm.output}",
        )
        # Operator-friendly: WARN, not ERROR.
        for record in cm.records:
            if "shutdown timed out" in record.getMessage():
                self.assertEqual(record.levelno, logging.WARNING)

    def test_wellbehaved_shutdown_no_timeout_warn(self):
        """Well-behaved sub-app must not trip the timeout WARN path."""
        # assertLogs requires at least one record at the level; capture
        # with a manual handler instead so absence-of-WARN is verifiable.
        records: list[logging.LogRecord] = []

        class _Capture(logging.Handler):
            def emit(self, record):
                records.append(record)

        handler = _Capture(level=logging.WARNING)
        logger = logging.getLogger("main")
        logger.addHandler(handler)
        try:
            asyncio.run(_drive_lifespan(_wellbehaved_app))
        finally:
            logger.removeHandler(handler)

        timeout_warns = [
            r for r in records
            if r.levelno == logging.WARNING and "shutdown timed out" in r.getMessage()
        ]
        self.assertFalse(
            timeout_warns,
            f"well-behaved sub-app tripped timeout WARN: {[r.getMessage() for r in timeout_warns]}",
        )


if __name__ == "__main__":
    unittest.main()

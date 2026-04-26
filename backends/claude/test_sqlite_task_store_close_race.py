"""Concurrency tests for ``SqliteTaskStore.close`` (#1649).

Covers the close-vs-operation race: while ``close()`` is in flight (after it
has nulled ``_conn`` but before the worker thread finishes
``sqlite3.Connection.close()``), a concurrent ``save()`` / ``get()`` /
``delete()`` must NOT call ``_open_db`` and stamp out a fresh connection on
the way down. The fix uses a ``self._closing`` sentinel guarded by the same
``asyncio.Lock`` as every other store op; a racing op observes the flag
under the lock and raises ``RuntimeError("task store is closing")`` instead
of resurrecting the DB handle.
"""
from __future__ import annotations

import asyncio
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch


_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

os.environ.setdefault("AGENT_NAME", "claude-test")
os.environ.setdefault("AGENT_OWNER", "test")
os.environ.setdefault("AGENT_ID", "claude")


# ---------------------------------------------------------------------------
# prometheus_client stub (the real client is heavyweight; metrics module
# only needs the surface area to import).
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

    for _name in (
        "Counter",
        "Gauge",
        "Histogram",
        "Summary",
        "Info",
        "CollectorRegistry",
    ):
        setattr(_pc, _name, _Metric)

    def _generate_latest(*a, **kw):  # pragma: no cover - never called here
        return b""

    _pc.generate_latest = _generate_latest
    _pc.CONTENT_TYPE_LATEST = "text/plain"
    sys.modules["prometheus_client"] = _pc


# ---------------------------------------------------------------------------
# a2a stub — sqlite_task_store only references ``Task`` for type hints and
# ``ServerCallContext`` / ``TaskStore`` as base symbols. We feed it a stub
# package tree with model_dump_json / model_validate_json that round-trip
# JSON so the real persistence path is exercised.
# ---------------------------------------------------------------------------
import json as _json


class _StubServerCallContext:  # noqa: D401 - stub
    pass


class _StubTaskStore:  # noqa: D401 - stub abstract base
    pass


class _StubTask:  # noqa: D401 - stub matching the bits the store touches
    def __init__(self, id: str, payload: str = "") -> None:
        self.id = id
        self.payload = payload

    def model_dump_json(self) -> str:
        return _json.dumps({"id": self.id, "payload": self.payload})

    @classmethod
    def model_validate_json(cls, raw: str) -> "_StubTask":
        obj = _json.loads(raw)
        return cls(id=obj["id"], payload=obj.get("payload", ""))


# Always ensure the specific names we need exist on these submodules,
# regardless of whether a sibling test (e.g. test_health_readiness.py)
# already installed its own partial a2a stub tree before us. We look up
# / create each layer with sys.modules.setdefault so we cooperate with
# any existing stubs.
def _ensure_module(name: str) -> types.ModuleType:
    mod = sys.modules.get(name)
    if mod is None:
        mod = types.ModuleType(name)
        sys.modules[name] = mod
    return mod


_a2a = _ensure_module("a2a")
_a2a_server = _ensure_module("a2a.server")
_a2a_server_context = _ensure_module("a2a.server.context")
_ensure_module("a2a.server.tasks")
_a2a_server_tasks_ts = _ensure_module("a2a.server.tasks.task_store")
_a2a_types = _ensure_module("a2a.types")

if not hasattr(_a2a_server_context, "ServerCallContext"):
    _a2a_server_context.ServerCallContext = _StubServerCallContext
if not hasattr(_a2a_server_tasks_ts, "TaskStore"):
    _a2a_server_tasks_ts.TaskStore = _StubTaskStore
if not hasattr(_a2a_types, "Task"):
    _a2a_types.Task = _StubTask


# ---------------------------------------------------------------------------
# metrics stub — sqlite_task_store imports ``metrics`` for the lock-wait
# histogram only. A no-op object with a ``None`` histogram skips observation.
# A sibling test (test_health_readiness.py) may have already installed a
# minimal ``metrics`` stub without the histogram attribute we need; force
# the attribute on whatever's there.
# ---------------------------------------------------------------------------
_metrics_mod = sys.modules.get("metrics")
if _metrics_mod is None:
    _metrics_mod = types.ModuleType("metrics")
    sys.modules["metrics"] = _metrics_mod
if not hasattr(_metrics_mod, "backend_sqlite_task_store_lock_wait_seconds"):
    _metrics_mod.backend_sqlite_task_store_lock_wait_seconds = None


# Imported AFTER stubs are installed. A sibling test module
# (test_health_readiness.py) installs a hollow stub at
# ``sys.modules["sqlite_task_store"]`` to keep its main.py import path
# light; evict it so we exercise the real module.
sys.modules.pop("sqlite_task_store", None)
import sqlite_task_store as sts  # noqa: E402
from a2a.types import Task  # noqa: E402


class CloseRaceTest(unittest.TestCase):
    """Verify close() racing get/save/delete never spawns a second connection."""

    def setUp(self) -> None:
        self._tmpdir = tempfile.TemporaryDirectory(prefix="sqlite-task-store-")
        self._path = os.path.join(self._tmpdir.name, "tasks.db")

    def tearDown(self) -> None:
        self._tmpdir.cleanup()

    def _run(self, coro):
        return asyncio.run(coro)

    def test_close_blocks_subsequent_reopen(self) -> None:
        """After close() returns, _get_conn must not have re-opened.

        Establishes a baseline: a single open during the seed save, then
        close() drops it. We DO NOT touch the store post-close in this case
        — the subsequent-reopen contract is covered by the race test below.
        """
        store = sts.SqliteTaskStore(self._path)
        open_calls = 0
        real_open = sts._open_db

        def _counting_open(path: str):
            nonlocal open_calls
            open_calls += 1
            return real_open(path)

        async def go() -> None:
            with patch.object(sts, "_open_db", side_effect=_counting_open):
                await store.save(Task(id="seed", payload="x"))
                self.assertEqual(open_calls, 1)
                await store.close()
                # close() does not itself reopen.
                self.assertEqual(open_calls, 1)
                self.assertIsNone(store._conn)
                self.assertFalse(store._closing)

        self._run(go())

    def test_close_racing_save_does_not_spawn_second_connection(self) -> None:
        """The headline #1649 invariant.

        We patch ``_open_db`` so the worker thread that runs the real
        ``sqlite3.close()`` blocks until we've launched a racing ``save()``.
        The fix means the racing op observes ``_closing=True`` under the
        lock and raises rather than calling ``_open_db`` a second time.
        """
        store = sts.SqliteTaskStore(self._path)
        open_calls = 0
        real_open = sts._open_db

        def _counting_open(path: str):
            nonlocal open_calls
            open_calls += 1
            return real_open(path)

        # Hold close() inside the worker thread until we've kicked off the
        # racing save. We pause INSIDE _do_close by wrapping conn.close().
        close_started = asyncio.Event()
        release_close = asyncio.Event()

        async def go() -> None:
            with patch.object(sts, "_open_db", side_effect=_counting_open):
                await store.save(Task(id="seed", payload="x"))
                self.assertEqual(open_calls, 1)

                # Wrap the connection in a proxy so the worker thread blocks
                # mid-close. ``sqlite3.Connection`` attributes are read-only,
                # so we substitute a proxy object that delegates everything
                # except close(). Schedule the unblock via the loop because
                # we're crossing thread boundaries.
                loop = asyncio.get_running_loop()
                real_conn = store._conn

                class _SlowCloseProxy:
                    def __init__(self, inner):
                        self._inner = inner

                    def __getattr__(self, name):
                        return getattr(self._inner, name)

                    def execute(self, *a, **kw):
                        return self._inner.execute(*a, **kw)

                    def close(self):
                        loop.call_soon_threadsafe(close_started.set)
                        fut = asyncio.run_coroutine_threadsafe(
                            release_close.wait(), loop
                        )
                        fut.result()
                        return self._inner.close()

                store._conn = _SlowCloseProxy(real_conn)  # type: ignore[assignment]

                close_task = asyncio.create_task(store.close())
                # Wait until close() is parked inside the worker thread.
                await close_started.wait()

                # _conn was nulled and _closing flipped under the lock
                # before we got here.
                self.assertIsNone(store._conn)
                self.assertTrue(store._closing)

                # Race: try to save while close is parked. Must raise
                # rather than calling _open_db.
                with self.assertRaises(RuntimeError) as ctx:
                    await store.save(Task(id="racer", payload="y"))
                self.assertIn("closing", str(ctx.exception))

                # Same for get and delete — same code path through
                # _get_conn, but assert explicitly so a future refactor
                # that splits the path can't regress only one of them.
                with self.assertRaises(RuntimeError):
                    await store.get("racer")
                with self.assertRaises(RuntimeError):
                    await store.delete("racer")

                # CRITICAL: no second open occurred.
                self.assertEqual(open_calls, 1)

                # Let close finish.
                release_close.set()
                await close_task

                # Post-close: flag cleared, no extra opens were stamped.
                self.assertFalse(store._closing)
                self.assertEqual(open_calls, 1)

        self._run(go())

    def test_close_is_idempotent(self) -> None:
        """Repeat close() calls must be safe and never reopen."""
        store = sts.SqliteTaskStore(self._path)
        open_calls = 0
        real_open = sts._open_db

        def _counting_open(path: str):
            nonlocal open_calls
            open_calls += 1
            return real_open(path)

        async def go() -> None:
            with patch.object(sts, "_open_db", side_effect=_counting_open):
                await store.save(Task(id="seed"))
                await store.close()
                await store.close()
                await store.close()
                self.assertEqual(open_calls, 1)

        self._run(go())


if __name__ == "__main__":
    unittest.main()

"""SQLite-backed TaskStore implementation using Python's built-in sqlite3.

Provides persistence across process restarts without requiring additional
dependencies (SQLAlchemy, aiosqlite, Redis).  All blocking I/O runs through
``asyncio.to_thread`` so the event loop is never blocked.

Concurrency (#726)
------------------
The original implementation serialised every op through a single
``sqlite3.Connection`` guarded by a single ``asyncio.Lock``, which meant a
slow read could block every writer and vice versa. This version keeps WAL
journaling + a 5s ``busy_timeout`` but drops the asyncio.Lock in favour of
per-thread connections. Each worker thread (created on demand by
``asyncio.to_thread``) gets its own ``sqlite3.Connection`` stored in a
``threading.local`` carrier; SQLite's native file locks (readers-don't-
block-readers under WAL, writers wait up to ``busy_timeout``) provide the
serialisation without forcing user-space round-trips.

Usage
-----
Configure via the ``TASK_STORE_PATH`` environment variable::

  TASK_STORE_PATH=/home/agent/logs/tasks.db

When the variable is unset, fall back to ``InMemoryTaskStore``.
"""

from __future__ import annotations

import logging
import os
import sqlite3
import threading

from a2a.server.context import ServerCallContext
from a2a.server.tasks.task_store import TaskStore
from a2a.types import Task

import asyncio

logger = logging.getLogger(__name__)

# Default SQLite busy_timeout. Tunable per-deployment via env so a slow NFS
# volume or a pathological batch of writes can be given more headroom
# without editing code.
_BUSY_TIMEOUT_MS = int(os.environ.get("SQLITE_TASK_STORE_BUSY_TIMEOUT_MS", "5000"))


def _configure_connection(conn: sqlite3.Connection) -> None:
    """Apply PRAGMAs shared by every per-thread connection (#726).

    WAL must be set at least once against the file (it's persistent), but
    applying it on every open is cheap and idempotent. ``busy_timeout`` and
    ``synchronous`` *are* per-connection and must be re-applied.
    """
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute(f"PRAGMA busy_timeout={_BUSY_TIMEOUT_MS}")
    conn.execute("PRAGMA synchronous=NORMAL")


def _ensure_schema(conn: sqlite3.Connection) -> None:
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS tasks (
            id TEXT PRIMARY KEY,
            data TEXT NOT NULL
        )
        """
    )
    conn.commit()


class SqliteTaskStore(TaskStore):
    """Persistent task store backed by a local SQLite database.

    Task state survives process restarts.  On startup, any tasks that were
    in-flight when the process was killed remain in the store with their
    last known status; clients polling for completion will eventually time
    out and receive a proper error rather than waiting indefinitely.

    Under the hood each worker thread (``asyncio.to_thread`` spins up a
    dedicated ``concurrent.futures.ThreadPoolExecutor`` worker) is given
    its own ``sqlite3.Connection`` via a ``threading.local`` carrier so
    readers and writers no longer queue behind a single user-space lock
    (#726). SQLite's own file locks (WAL + ``busy_timeout``) provide
    correctness.
    """

    def __init__(self, path: str) -> None:
        self._path = path
        self._tls = threading.local()
        # One-shot init guard. The first connection opened is used to
        # create the schema + log startup pragmas; subsequent threads
        # skip the logging (schema creation remains IF NOT EXISTS).
        self._init_lock = threading.Lock()
        self._initialised = False

    def _get_conn(self) -> sqlite3.Connection:
        conn = getattr(self._tls, "conn", None)
        if conn is not None:
            return conn
        # First-time open for this thread. Ensure the directory exists
        # before sqlite3.connect touches the filesystem.
        os.makedirs(
            os.path.dirname(self._path) if os.path.dirname(self._path) else ".",
            exist_ok=True,
        )
        conn = sqlite3.connect(self._path, check_same_thread=False)
        _configure_connection(conn)
        with self._init_lock:
            if not self._initialised:
                _ensure_schema(conn)
                self._initialised = True
                logger.info(
                    "SqliteTaskStore opened at %s (WAL, busy_timeout=%sms, "
                    "per-thread connection pool)",
                    self._path, _BUSY_TIMEOUT_MS,
                )
            else:
                # Still verify schema from this thread's connection.
                # Cheap — IF NOT EXISTS is a no-op when the table is there.
                _ensure_schema(conn)
        self._tls.conn = conn
        return conn

    def _save_sync(self, task_id: str, data: str) -> None:
        conn = self._get_conn()
        conn.execute(
            "INSERT INTO tasks (id, data) VALUES (?, ?) "
            "ON CONFLICT(id) DO UPDATE SET data = excluded.data",
            (task_id, data),
        )
        conn.commit()

    def _get_sync(self, task_id: str) -> str | None:
        conn = self._get_conn()
        row = conn.execute(
            "SELECT data FROM tasks WHERE id = ?", (task_id,)
        ).fetchone()
        return row[0] if row else None

    def _delete_sync(self, task_id: str) -> None:
        conn = self._get_conn()
        conn.execute("DELETE FROM tasks WHERE id = ?", (task_id,))
        conn.commit()

    async def save(
        self, task: Task, context: ServerCallContext | None = None
    ) -> None:
        data = task.model_dump_json()
        await asyncio.to_thread(self._save_sync, task.id, data)
        logger.debug("Task %s saved to SQLite store.", task.id)

    async def get(
        self, task_id: str, context: ServerCallContext | None = None
    ) -> Task | None:
        raw = await asyncio.to_thread(self._get_sync, task_id)
        if raw is None:
            logger.debug("Task %s not found in SQLite store.", task_id)
            return None
        task = Task.model_validate_json(raw)
        logger.debug("Task %s retrieved from SQLite store.", task_id)
        return task

    async def delete(
        self, task_id: str, context: ServerCallContext | None = None
    ) -> None:
        await asyncio.to_thread(self._delete_sync, task_id)
        logger.debug("Task %s deleted from SQLite store.", task_id)

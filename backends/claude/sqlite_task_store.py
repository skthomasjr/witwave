"""SQLite-backed TaskStore implementation using Python's built-in sqlite3.

Provides persistence across process restarts without requiring additional
dependencies (SQLAlchemy, aiosqlite, Redis).  All blocking I/O runs through
asyncio.to_thread so the event loop is never blocked.

Usage
-----
Configure via the TASK_STORE_PATH environment variable:

  TASK_STORE_PATH=/home/agent/logs/tasks.db

When the variable is unset, fall back to InMemoryTaskStore.
"""

from __future__ import annotations

import asyncio
import logging
import os
import sqlite3

from a2a.server.context import ServerCallContext
from a2a.server.tasks.task_store import TaskStore
from a2a.types import Task

logger = logging.getLogger(__name__)


def _open_db(path: str) -> sqlite3.Connection:
    """Open (or create) the SQLite database and ensure the tasks table exists.

    Enables WAL journaling and a 5s busy_timeout so concurrent readers/writers
    wait for the lock instead of raising ``SQLITE_BUSY`` immediately, and so
    readers do not block writers (and vice versa). ``check_same_thread=False``
    is retained because ``SqliteTaskStore`` shares a single connection across
    threads via ``asyncio.to_thread`` under an ``asyncio.Lock``.
    """
    os.makedirs(os.path.dirname(path) if os.path.dirname(path) else ".", exist_ok=True)
    conn = sqlite3.connect(path, check_same_thread=False)
    journal_mode = conn.execute("PRAGMA journal_mode=WAL").fetchone()
    conn.execute("PRAGMA busy_timeout=5000")
    conn.execute("PRAGMA synchronous=NORMAL")
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS tasks (
            id TEXT PRIMARY KEY,
            data TEXT NOT NULL
        )
        """
    )
    conn.commit()
    logger.info(
        "SqliteTaskStore pragmas applied: journal_mode=%s busy_timeout=5000 synchronous=NORMAL",
        journal_mode[0] if journal_mode else "unknown",
    )
    return conn


def _db_save(conn: sqlite3.Connection, task_id: str, data: str) -> None:
    conn.execute(
        "INSERT INTO tasks (id, data) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data",
        (task_id, data),
    )
    conn.commit()


def _db_get(conn: sqlite3.Connection, task_id: str) -> str | None:
    row = conn.execute("SELECT data FROM tasks WHERE id = ?", (task_id,)).fetchone()
    return row[0] if row else None


def _db_delete(conn: sqlite3.Connection, task_id: str) -> None:
    conn.execute("DELETE FROM tasks WHERE id = ?", (task_id,))
    conn.commit()


class SqliteTaskStore(TaskStore):
    """Persistent task store backed by a local SQLite database.

    Task state survives process restarts.  On startup, any tasks that were
    in-flight when the process was killed remain in the store with their last
    known status; clients polling for completion will eventually time out and
    receive a proper error rather than waiting indefinitely.
    """

    def __init__(self, path: str) -> None:
        self._path = path
        self._conn: sqlite3.Connection | None = None
        self._lock = asyncio.Lock()

    def _get_conn(self) -> sqlite3.Connection:
        if self._conn is None:
            self._conn = _open_db(self._path)
            logger.info("SqliteTaskStore opened at %s", self._path)
        return self._conn

    async def save(
        self, task: Task, context: ServerCallContext | None = None
    ) -> None:
        data = task.model_dump_json()
        async with self._lock:
            await asyncio.to_thread(_db_save, self._get_conn(), task.id, data)
        logger.debug("Task %s saved to SQLite store.", task.id)

    async def get(
        self, task_id: str, context: ServerCallContext | None = None
    ) -> Task | None:
        async with self._lock:
            raw = await asyncio.to_thread(_db_get, self._get_conn(), task_id)
        if raw is None:
            logger.debug("Task %s not found in SQLite store.", task_id)
            return None
        task = Task.model_validate_json(raw)
        logger.debug("Task %s retrieved from SQLite store.", task_id)
        return task

    async def delete(
        self, task_id: str, context: ServerCallContext | None = None
    ) -> None:
        async with self._lock:
            await asyncio.to_thread(_db_delete, self._get_conn(), task_id)
        logger.debug("Task %s deleted from SQLite store.", task_id)

    async def close(self) -> None:
        """Close the underlying SQLite connection, committing WAL.

        Invoked from the backend shutdown path so a graceful SIGTERM
        flushes the WAL and releases the file handle (#713). Subsequent
        operations will re-open on demand via _get_conn. Idempotent.
        """
        async with self._lock:
            conn = self._conn
            self._conn = None
        if conn is not None:
            def _do_close() -> None:
                try:
                    # PRAGMA wal_checkpoint(TRUNCATE) rolls the WAL
                    # into the main DB so a subsequent cold open is
                    # short — avoids "elongated startup" on restart
                    # after a long-running process (#713).
                    try:
                        conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
                    except sqlite3.OperationalError:
                        pass
                    conn.close()
                except Exception as exc:  # pragma: no cover - defensive
                    logger.warning("SqliteTaskStore close error: %r", exc)
            await asyncio.to_thread(_do_close)
            logger.info("SqliteTaskStore closed at %s", self._path)

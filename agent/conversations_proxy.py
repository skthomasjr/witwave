"""Fetch and merge conversation and trace logs from backend agents."""

import logging

import httpx

from backends.config import BackendConfig

logger = logging.getLogger(__name__)


async def fetch_backend_conversations(
    backends: list[BackendConfig],
    since: str | None = None,
    limit: int | None = None,
) -> list[dict]:
    """Fetch /conversations from each backend and return merged entries sorted by ts.

    Backends that are unreachable or return non-200 are silently skipped.
    """
    params: dict = {}
    if since:
        params["since"] = since

    seen: set[tuple] = set()
    all_entries: list[dict] = []
    async with httpx.AsyncClient(timeout=5.0) as client:
        for backend in backends:
            if not backend.url:
                continue
            url = backend.url.rstrip("/") + "/conversations"
            try:
                resp = await client.get(url, params=params)
                if resp.status_code == 200:
                    entries = resp.json()
                    if isinstance(entries, list):
                        for entry in entries:
                            key = (entry.get("ts"), entry.get("session_id"), entry.get("role"), entry.get("agent"), (entry.get("text") or "")[:64])
                            if key not in seen:
                                seen.add(key)
                                all_entries.append(entry)
                else:
                    logger.debug(f"Backend {backend.id!r} /conversations returned {resp.status_code} — skipping")
            except Exception as exc:
                logger.debug(f"Backend {backend.id!r} /conversations unreachable: {exc!r} — skipping")

    all_entries.sort(key=lambda e: e.get("ts", ""))
    if limit is not None:
        all_entries = all_entries[-limit:]
    return all_entries


async def fetch_backend_trace(
    backends: list[BackendConfig],
    since: str | None = None,
    limit: int | None = None,
) -> list[dict]:
    """Fetch /trace from each backend and return merged entries sorted by ts.

    Backends that are unreachable or return non-200 are silently skipped.
    """
    params: dict = {}
    if since:
        params["since"] = since

    seen: set[tuple] = set()
    all_entries: list[dict] = []
    async with httpx.AsyncClient(timeout=5.0) as client:
        for backend in backends:
            if not backend.url:
                continue
            url = backend.url.rstrip("/") + "/trace"
            try:
                resp = await client.get(url, params=params)
                if resp.status_code == 200:
                    entries = resp.json()
                    if isinstance(entries, list):
                        for entry in entries:
                            key = (entry.get("ts"), entry.get("session_id"), entry.get("event_type"), entry.get("id") or entry.get("tool_use_id"))
                            if key not in seen:
                                seen.add(key)
                                all_entries.append(entry)
                else:
                    logger.debug(f"Backend {backend.id!r} /trace returned {resp.status_code} — skipping")
            except Exception as exc:
                logger.debug(f"Backend {backend.id!r} /trace unreachable: {exc!r} — skipping")

    all_entries.sort(key=lambda e: e.get("ts", ""))
    if limit is not None:
        all_entries = all_entries[-limit:]
    return all_entries

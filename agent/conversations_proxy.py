"""Fetch and merge conversation and trace logs from backend agents."""

import asyncio
import logging

import httpx

from backends.config import BackendConfig

logger = logging.getLogger(__name__)


async def fetch_backend_conversations(
    backends: list[BackendConfig],
    since: str | None = None,
    limit: int | None = None,
    auth_token: str | None = None,
) -> list[dict]:
    """Fetch /conversations from each backend concurrently and return merged entries sorted by ts.

    Backends that are unreachable or return non-200 are silently skipped.
    When auth_token is provided, it is forwarded as a Bearer Authorization header.
    """
    # Pass limit to each backend so the per-backend response is bounded.
    # This caps the merged deduplication set to O(n_backends × limit) entries
    # rather than allowing unlimited accumulation (#365).
    params: dict = {}
    if since:
        params["since"] = since
    if limit is not None:
        params["limit"] = limit
    headers: dict = {}
    if auth_token:
        headers["Authorization"] = f"Bearer {auth_token}"

    async def _fetch_one_conversations(client: httpx.AsyncClient, backend: BackendConfig) -> list[dict]:
        if not backend.url:
            return []
        url = backend.url.rstrip("/") + "/conversations"
        try:
            resp = await client.get(url, params=params, headers=headers)
            if resp.status_code == 200:
                entries = resp.json()
                if isinstance(entries, list):
                    return entries
            else:
                logger.debug(f"Backend {backend.id!r} /conversations returned {resp.status_code} — skipping")
        except Exception as exc:
            logger.debug(f"Backend {backend.id!r} /conversations unreachable: {exc!r} — skipping")
        return []

    seen: set[tuple] = set()
    all_entries: list[dict] = []
    async with httpx.AsyncClient(timeout=5.0) as client:
        results = await asyncio.gather(
            *[_fetch_one_conversations(client, b) for b in backends],
            return_exceptions=True,
        )
    for result in results:
        if isinstance(result, BaseException):
            logger.debug(f"Backend /conversations gather error: {result!r} — skipping")
            continue
        for entry in result:
            key = (entry.get("ts"), entry.get("session_id"), entry.get("role"), entry.get("agent"), (entry.get("text") or "")[:64])
            if key not in seen:
                seen.add(key)
                all_entries.append(entry)

    all_entries.sort(key=lambda e: e.get("ts", ""))
    if limit is not None:
        all_entries = all_entries[-limit:]
    return all_entries


async def fetch_backend_trace(
    backends: list[BackendConfig],
    since: str | None = None,
    limit: int | None = None,
    auth_token: str | None = None,
) -> list[dict]:
    """Fetch /trace from each backend concurrently and return merged entries sorted by ts.

    Backends that are unreachable or return non-200 are silently skipped.
    When auth_token is provided, it is forwarded as a Bearer Authorization header.
    """
    # Pass limit to each backend so the per-backend response is bounded.
    # This caps the merged deduplication set to O(n_backends × limit) entries
    # rather than allowing unlimited accumulation (#365).
    params: dict = {}
    if since:
        params["since"] = since
    if limit is not None:
        params["limit"] = limit
    headers: dict = {}
    if auth_token:
        headers["Authorization"] = f"Bearer {auth_token}"

    async def _fetch_one_trace(client: httpx.AsyncClient, backend: BackendConfig) -> list[dict]:
        if not backend.url:
            return []
        url = backend.url.rstrip("/") + "/trace"
        try:
            resp = await client.get(url, params=params, headers=headers)
            if resp.status_code == 200:
                entries = resp.json()
                if isinstance(entries, list):
                    return entries
            else:
                logger.debug(f"Backend {backend.id!r} /trace returned {resp.status_code} — skipping")
        except Exception as exc:
            logger.debug(f"Backend {backend.id!r} /trace unreachable: {exc!r} — skipping")
        return []

    seen: set[tuple] = set()
    all_entries: list[dict] = []
    async with httpx.AsyncClient(timeout=5.0) as client:
        results = await asyncio.gather(
            *[_fetch_one_trace(client, b) for b in backends],
            return_exceptions=True,
        )
    for result in results:
        if isinstance(result, BaseException):
            logger.debug(f"Backend /trace gather error: {result!r} — skipping")
            continue
        for entry in result:
            key = (entry.get("ts"), entry.get("session_id"), entry.get("event_type"), entry.get("id") or entry.get("tool_use_id"))
            if key not in seen:
                seen.add(key)
                all_entries.append(entry)

    all_entries.sort(key=lambda e: e.get("ts", ""))
    if limit is not None:
        all_entries = all_entries[-limit:]
    return all_entries

"""Shared conversations and trace handler factories used by all backend main modules.

Provides a single implementation of _read_jsonl, make_conversations_handler,
and make_trace_handler so that changes to authentication, pagination, or
response shape are applied once rather than across three separate copies.
"""
import asyncio
import hmac as hmac_mod
import json
import logging
from collections import deque
from datetime import datetime

from starlette.requests import Request
from starlette.responses import JSONResponse

logger = logging.getLogger(__name__)


def _warn_if_empty_token(auth_token: str | None, handler_name: str) -> None:
    """Emit a clear warning when auth_token is an empty string.

    Distinguishes "unset" (None) from "set-but-empty" ("") so operators get a
    loud signal that a misconfigured CONVERSATIONS_AUTH_TOKEN has silently
    disabled authentication. Preserves the existing permissive behavior for
    backward compatibility — the handler still skips auth — but the warning
    surfaces the exposure at startup instead of leaving it silent.
    """
    if auth_token == "":
        logger.warning(
            "%s: CONVERSATIONS_AUTH_TOKEN is set but empty; authentication is DISABLED "
            "and logs are readable by any caller. Set a non-empty token or unset the "
            "variable entirely to acknowledge disabled auth.",
            handler_name,
        )


def _read_jsonl(path: str, since_dt: datetime | None, limit_n: int | None) -> list:
    """Read a JSONL log file, optionally filtering by timestamp and limiting results.

    Designed to be called via asyncio.to_thread to avoid blocking the event loop.
    Uses deque(maxlen=limit_n) so only the last limit_n entries are kept in memory.
    """
    entries: deque = deque(maxlen=limit_n)
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    entry = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if since_dt:
                    try:
                        ts = datetime.fromisoformat(entry.get("ts", "").replace("Z", "+00:00"))
                        if ts < since_dt:
                            continue
                    except ValueError:
                        continue
                entries.append(entry)
    except FileNotFoundError:
        pass
    return list(entries)


def make_conversations_handler(
    auth_token: str,
    conversation_log: str,
):
    """Return an ASGI handler for GET /conversations."""
    _warn_if_empty_token(auth_token, "make_conversations_handler")

    async def conversations_handler(request: Request) -> JSONResponse:
        if auth_token:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        since_dt: datetime | None = None
        if since:
            try:
                since_dt = datetime.fromisoformat(since.replace("Z", "+00:00"))
            except ValueError:
                return JSONResponse({"error": "invalid since"}, status_code=400)
        entries = await asyncio.to_thread(_read_jsonl, conversation_log, since_dt, limit_n)
        return JSONResponse(entries)

    return conversations_handler


def make_trace_handler(
    auth_token: str,
    trace_log: str,
):
    """Return an ASGI handler for GET /trace."""
    _warn_if_empty_token(auth_token, "make_trace_handler")

    async def trace_handler(request: Request) -> JSONResponse:
        if auth_token:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        since_dt: datetime | None = None
        if since:
            try:
                since_dt = datetime.fromisoformat(since.replace("Z", "+00:00"))
            except ValueError:
                return JSONResponse({"error": "invalid since"}, status_code=400)
        entries = await asyncio.to_thread(_read_jsonl, trace_log, since_dt, limit_n)
        return JSONResponse(entries)

    return trace_handler


def make_proxy_conversations_handler(
    auth_token: str,
    fetch_fn,
):
    """Return an ASGI handler for GET /conversations backed by a fan-out fetch function.

    fetch_fn is an async callable with the signature:
        async def fetch_fn(since: str | None, limit: int | None) -> list[dict]

    This variant is used by nyx-harness, which fans out to multiple backend agents
    rather than reading a local JSONL file.
    """
    _warn_if_empty_token(auth_token, "make_proxy_conversations_handler")

    async def conversations_handler(request: Request) -> JSONResponse:
        if auth_token:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        if since:
            try:
                datetime.fromisoformat(since.replace("Z", "+00:00"))
            except ValueError:
                return JSONResponse({"error": "invalid since"}, status_code=400)
        entries = await fetch_fn(since, limit_n)
        return JSONResponse(entries)

    return conversations_handler


def make_proxy_trace_handler(
    auth_token: str,
    fetch_fn,
):
    """Return an ASGI handler for GET /trace backed by a fan-out fetch function.

    fetch_fn is an async callable with the signature:
        async def fetch_fn(since: str | None, limit: int | None) -> list[dict]
    """
    _warn_if_empty_token(auth_token, "make_proxy_trace_handler")

    async def trace_handler(request: Request) -> JSONResponse:
        if auth_token:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        if since:
            try:
                datetime.fromisoformat(since.replace("Z", "+00:00"))
            except ValueError:
                return JSONResponse({"error": "invalid since"}, status_code=400)
        entries = await fetch_fn(since, limit_n)
        return JSONResponse(entries)

    return trace_handler

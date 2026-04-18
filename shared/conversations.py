"""Shared conversations and trace handler factories used by all backend main modules.

Provides a single implementation of _read_jsonl, make_conversations_handler,
and make_trace_handler so that changes to authentication, pagination, or
response shape are applied once rather than across three separate copies.
"""
import asyncio
import hmac as hmac_mod
import json
import logging
import os
from collections import deque
from datetime import datetime

from starlette.requests import Request
from starlette.responses import JSONResponse

logger = logging.getLogger(__name__)


def auth_disabled_escape_hatch() -> bool:
    """Return True iff the operator has explicitly opted out of auth (#718).

    Accepts ``CONVERSATIONS_AUTH_DISABLED`` in {"1", "true", "yes", "on"} to
    allow local-dev and intentional public deployments to bypass the
    fail-closed behavior for an empty/unset token. Any other value (including
    "false"/"0"/unset) keeps auth required.
    """
    return os.environ.get("CONVERSATIONS_AUTH_DISABLED", "").strip().lower() in {
        "1",
        "true",
        "yes",
        "on",
    }


def _log_missing_token(handler_name: str) -> None:
    """Emit an ERROR when auth is missing without the explicit escape hatch (#718)."""
    logger.error(
        "%s: CONVERSATIONS_AUTH_TOKEN is unset or empty and CONVERSATIONS_AUTH_DISABLED "
        "is not set — endpoint will fail closed (503). Set a non-empty token, or set "
        "CONVERSATIONS_AUTH_DISABLED=true to acknowledge disabled auth for local dev.",
        handler_name,
    )


def _log_escape_hatch(handler_name: str) -> None:
    """Emit an ERROR when the escape hatch intentionally disables auth (#718)."""
    logger.error(
        "%s: CONVERSATIONS_AUTH_DISABLED=true — authentication is DISABLED and logs "
        "are readable by any caller. Use only for local development.",
        handler_name,
    )


def _warn_if_empty_token(auth_token: str | None, handler_name: str) -> None:
    """Emit a loud diagnostic for missing tokens at handler-factory time (#718).

    Fail-closed semantics now live in the handlers themselves; this function is
    retained so the factory still logs at construction (before any request)
    whether the deployment is configured safely.
    """
    if auth_token:
        return
    if auth_disabled_escape_hatch():
        _log_escape_hatch(handler_name)
    else:
        _log_missing_token(handler_name)


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


def _read_tool_audit_jsonl(
    path: str,
    since_dt: datetime | None,
    limit_n: int | None,
    decision: str | None,
    tool: str | None,
    session: str | None,
) -> list:
    """Read tool-audit.jsonl with per-row filters for decision / tool / session (#635).

    Accepts both ISO-8601 string timestamps (claude) and numeric epoch seconds
    (codex) so the same reader serves every backend without coercion in the
    caller. Entries whose ``ts`` can't be parsed are retained when no ``since``
    filter is active and skipped when one is, matching the existing
    ``_read_jsonl`` policy. Runs in a worker thread via ``asyncio.to_thread``.
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
                    ts_raw = entry.get("ts")
                    ts_ok = False
                    if isinstance(ts_raw, (int, float)):
                        try:
                            from datetime import timezone as _tz
                            ts = datetime.fromtimestamp(float(ts_raw), tz=_tz.utc)
                            ts_ok = ts >= since_dt
                        except (OverflowError, OSError, ValueError):
                            ts_ok = False
                    elif isinstance(ts_raw, str):
                        try:
                            ts = datetime.fromisoformat(ts_raw.replace("Z", "+00:00"))
                            ts_ok = ts >= since_dt
                        except ValueError:
                            ts_ok = False
                    if not ts_ok:
                        continue
                if decision:
                    row_decision = str(entry.get("decision") or "").lower()
                    if row_decision != decision.lower():
                        continue
                if tool:
                    row_tool = str(entry.get("tool_name") or entry.get("tool") or "")
                    if row_tool != tool:
                        continue
                if session:
                    row_session = str(entry.get("session_id") or "")
                    if row_session != session:
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
        # Fail-closed when the token is missing unless the operator explicitly
        # opted out via CONVERSATIONS_AUTH_DISABLED=true (#718). An empty token
        # previously short-circuited the gate, silently exposing logs.
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
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
        # Fail-closed when the token is missing unless CONVERSATIONS_AUTH_DISABLED
        # is explicitly set (#718).
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
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


def make_tool_audit_handler(
    auth_token: str,
    tool_audit_log: str,
):
    """Return an ASGI handler for GET /tool-audit (#635).

    Accepts the same ``since`` / ``limit`` contract as ``/conversations`` plus
    ``decision`` / ``tool`` / ``session`` row filters. Backends that do not
    (yet) write a tool-audit file return an empty list rather than 404 so the
    dashboard can fan out uniformly across the team.
    """
    _warn_if_empty_token(auth_token, "make_tool_audit_handler")

    async def tool_audit_handler(request: Request) -> JSONResponse:
        # Fail-closed when the token is missing unless CONVERSATIONS_AUTH_DISABLED
        # is explicitly set (#718).
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        decision = request.query_params.get("decision")
        tool = request.query_params.get("tool")
        session = request.query_params.get("session")
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
        if decision and decision.lower() not in ("allow", "warn", "deny"):
            return JSONResponse({"error": "invalid decision"}, status_code=400)
        entries = await asyncio.to_thread(
            _read_tool_audit_jsonl,
            tool_audit_log,
            since_dt,
            limit_n,
            decision,
            tool,
            session,
        )
        return JSONResponse(entries)

    return tool_audit_handler


def make_proxy_tool_audit_handler(
    auth_token: str,
    fetch_fn,
):
    """Return an ASGI handler for GET /tool-audit backed by a fan-out fetch fn (#635).

    fetch_fn is an async callable with the signature:
        async def fetch_fn(
            since: str | None,
            limit: int | None,
            decision: str | None,
            tool: str | None,
            session: str | None,
        ) -> list[dict]
    """
    _warn_if_empty_token(auth_token, "make_proxy_tool_audit_handler")

    async def tool_audit_handler(request: Request) -> JSONResponse:
        # Fail-closed when the token is missing unless CONVERSATIONS_AUTH_DISABLED
        # is explicitly set (#718).
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        decision = request.query_params.get("decision")
        tool = request.query_params.get("tool")
        session = request.query_params.get("session")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        if since:
            try:
                datetime.fromisoformat(since.replace("Z", "+00:00"))
            except ValueError:
                return JSONResponse({"error": "invalid since"}, status_code=400)
        if decision and decision.lower() not in ("allow", "warn", "deny"):
            return JSONResponse({"error": "invalid decision"}, status_code=400)
        entries = await fetch_fn(since, limit_n, decision, tool, session)
        return JSONResponse(entries)

    return tool_audit_handler


def make_proxy_conversations_handler(
    auth_token: str,
    fetch_fn,
):
    """Return an ASGI handler for GET /conversations backed by a fan-out fetch function.

    fetch_fn is an async callable with the signature:
        async def fetch_fn(since: str | None, limit: int | None) -> list[dict]

    This variant is used by harness, which fans out to multiple backend agents
    rather than reading a local JSONL file.
    """
    _warn_if_empty_token(auth_token, "make_proxy_conversations_handler")

    async def conversations_handler(request: Request) -> JSONResponse:
        # Fail-closed when the token is missing unless CONVERSATIONS_AUTH_DISABLED
        # is explicitly set (#718).
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
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
        # Fail-closed when the token is missing unless CONVERSATIONS_AUTH_DISABLED
        # is explicitly set (#718).
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
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

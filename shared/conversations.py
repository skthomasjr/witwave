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


# Per-file tail cache for _read_jsonl (#715). Maps path → (stat key, offset,
# buffered entries). When the file hasn't changed since last call, we
# short-circuit out. When it has grown, we seek to the cached offset and
# parse only the new tail. On rotation (size decreased / inode changed),
# we drop the cache and re-parse from the top. The cache is bounded
# per-path at _TAIL_CACHE_ENTRY_CAP entries so a very long-lived file does
# not hold the whole history in RAM — the deque(maxlen=limit_n) at
# request-time still clips to the caller's limit.
_TAIL_CACHE: dict[str, tuple[tuple[int, int, float], int, list]] = {}
_TAIL_CACHE_ENTRY_CAP = int(os.environ.get("JSONL_TAIL_CACHE_ENTRIES", "5000"))


def _read_jsonl(path: str, since_dt: datetime | None, limit_n: int | None) -> list:
    """Read a JSONL log file, optionally filtering by timestamp and limiting results.

    Designed to be called via asyncio.to_thread to avoid blocking the event loop.
    Uses deque(maxlen=limit_n) so only the last limit_n entries are kept in memory.

    Incremental tail read (#715): maintains an in-process cache keyed by
    (path, inode, size, mtime) so subsequent dashboard polls pay only
    the cost of the newly appended lines. Log rotation (size shrink /
    inode change) transparently invalidates and re-reads from the top.
    """
    try:
        st = os.stat(path)
    except FileNotFoundError:
        return []
    stat_key = (st.st_ino, st.st_size, st.st_mtime)
    cached = _TAIL_CACHE.get(path)
    if cached is not None:
        cached_key, cached_offset, cached_entries = cached
        # Same file identity + unchanged → serve directly.
        if cached_key == stat_key:
            return _filter_and_limit(cached_entries, since_dt, limit_n)
        # #1296: any identity change invalidates — covers truncate+rewrite
        # to the same size (mtime changed but size stable). Previous code
        # only noticed inode change or size shrink.
        cached = None

    entries: list = list(cached[2]) if cached else []
    offset = cached[1] if cached else 0
    try:
        with open(path) as f:
            if offset > 0:
                f.seek(offset)
            for line in f:
                line_stripped = line.strip()
                if not line_stripped:
                    continue
                try:
                    entry = json.loads(line_stripped)
                except json.JSONDecodeError:
                    continue
                entries.append(entry)
            new_offset = f.tell()
    except FileNotFoundError:
        return []
    # Trim buffered entries to cap so memory stays bounded on long-lived files.
    if len(entries) > _TAIL_CACHE_ENTRY_CAP:
        entries = entries[-_TAIL_CACHE_ENTRY_CAP:]
    _TAIL_CACHE[path] = (stat_key, new_offset, entries)
    return _filter_and_limit(entries, since_dt, limit_n)


def _filter_and_limit(entries: list, since_dt: datetime | None, limit_n: int | None) -> list:
    """Apply since_dt filter + limit to a pre-parsed entry list (#715)."""
    out: deque = deque(maxlen=limit_n)
    for entry in entries:
        if since_dt:
            try:
                ts = datetime.fromisoformat((entry.get("ts") or "").replace("Z", "+00:00"))
                if ts < since_dt:
                    continue
            except (ValueError, AttributeError, TypeError):
                continue
        out.append(entry)
    return list(out)


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

    #1295: use the same `_TAIL_CACHE` tail-read that `_read_jsonl` uses so a
    polling dashboard pays O(new-rows) per tick, not O(total-rows). The cache
    stores unfiltered parsed entries; per-request filters run against the
    cached list.
    """
    # Load parsed entries via the shared tail-cache machinery.
    try:
        st = os.stat(path)
    except FileNotFoundError:
        return []
    stat_key = (st.st_ino, st.st_size, st.st_mtime)
    cached = _TAIL_CACHE.get(path)
    if cached is not None:
        cached_key, cached_offset, cached_entries = cached
        if cached_key == stat_key:
            parsed_entries = cached_entries
        else:
            cached = None
            parsed_entries = None
    else:
        parsed_entries = None

    if parsed_entries is None:
        parsed_entries = list(cached[2]) if cached else []
        offset = cached[1] if cached else 0
        try:
            with open(path) as f:
                if offset > 0:
                    f.seek(offset)
                for line in f:
                    stripped = line.strip()
                    if not stripped:
                        continue
                    try:
                        parsed_entries.append(json.loads(stripped))
                    except json.JSONDecodeError:
                        continue
                new_offset = f.tell()
        except FileNotFoundError:
            return []
        if len(parsed_entries) > _TAIL_CACHE_ENTRY_CAP:
            parsed_entries = parsed_entries[-_TAIL_CACHE_ENTRY_CAP:]
        _TAIL_CACHE[path] = (stat_key, new_offset, parsed_entries)

    # Apply per-request filters against the (cached) parsed list.
    out: deque = deque(maxlen=limit_n)
    for entry in parsed_entries:
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
        out.append(entry)
    return list(out)


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

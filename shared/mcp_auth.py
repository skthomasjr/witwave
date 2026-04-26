"""Bearer-token authentication middleware for MCP tool servers (#771).

FastMCP's streamable-http listener binds 0.0.0.0 with no server-side
authentication by default. Any pod reachable via the Service can
invoke destructive tools (apply/delete, install/uninstall). This
module provides a lightweight ASGI middleware + helper so tool
servers can enforce a bearer token read from MCP_TOOL_AUTH_TOKEN.

When MCP_TOOL_AUTH_TOKEN is unset or empty, the middleware logs a
loud warning at first request and fails closed unless
MCP_TOOL_AUTH_DISABLED is explicitly set — mirroring the
fail-closed posture used by the backend /conversations endpoints
(#517/#718).

Usage (tool servers):

    from mcp_auth import require_bearer_token
    app = mcp.streamable_http_app()
    app = require_bearer_token(app)
    uvicorn.run(app, host="0.0.0.0", port=8000)
"""

from __future__ import annotations

import hmac as hmac_mod
import json
import logging
import os
from typing import Any, Awaitable, Callable

logger = logging.getLogger(__name__)

_FAIL_CLOSED_WARNED: set[tuple[bool, bool]] = set()  # #1585: (token_present, disabled)
# #1359: re-arm the one-shot warning every N rejects so long-running
# misconfig (days of 401s after a pod-start log line) re-surfaces in
# logs. Parity with shared/hook_events.py warn pattern.
_FAIL_CLOSED_REARM_EVERY = 500
_FAIL_CLOSED_COUNT_SINCE_WARN: int = 0
# #1406: check-and-reset on counter is not atomic under the GIL;
# protect the full sequence with a threading.Lock so concurrent
# rejects can't double-fire or skip the 500-threshold warn.
import threading as _threading
_FAIL_CLOSED_LOCK = _threading.Lock()


def _auth_disabled_escape_hatch() -> bool:
    return os.environ.get("MCP_TOOL_AUTH_DISABLED", "").strip().lower() in {
        "1", "true", "yes", "on",
    }


def require_bearer_token(
    app,
    info_provider: Callable[[], dict[str, Any]] | None = None,
):  # type: ignore[no-untyped-def]
    """Wrap an ASGI app so every request must carry a valid bearer token.

    Token source: MCP_TOOL_AUTH_TOKEN env var. Compared with
    hmac.compare_digest so the check is timing-safe. When unset /
    empty the server fails closed (401) at first request; operators
    who want to run without auth must set MCP_TOOL_AUTH_DISABLED=true.

    When ``info_provider`` is supplied, ``GET /info`` is served
    directly from the middleware as a bearer-gated JSON surface
    describing image / SDK / plugin versions and enabled features
    (#1122). Probe endpoints, dashboard triage, and CI preflight can
    then verify the pod is wired correctly without invoking a tool.
    """

    async def middleware(scope, receive, send) -> None:  # type: ignore[no-untyped-def]
        if scope.get("type") != "http":
            await app(scope, receive, send)
            return
        # /health is an unauthenticated liveness/readiness probe target
        # (#848). Kubernetes kubelet probes cannot carry bearer tokens,
        # and the endpoint exposes only static JSON so gating it would
        # add operational risk without security benefit.
        if scope.get("path") == "/health" and scope.get("method", "GET") in ("GET", "HEAD"):
            await _send_health(send)
            return
        token = os.environ.get("MCP_TOOL_AUTH_TOKEN", "").strip()
        disabled = _auth_disabled_escape_hatch()
        # One-shot warning per process per posture so operators see
        # the misconfig in kubectl logs without per-request spam.
        # #1585: key on the binary (token_present, disabled) tuple
        # directly. The previous implementation hashed to a 32-bit int
        # which the re-arm path only cleared for the *current* key, so
        # flipping posture left stale entries that silently suppressed
        # the warning when the posture flipped back.
        posture_key = (bool(token), bool(disabled))
        # #1359 + #1406: periodic re-arm, protected by a lock so concurrent
        # rejects can't double-fire or skip the threshold.
        global _FAIL_CLOSED_COUNT_SINCE_WARN
        with _FAIL_CLOSED_LOCK:
            should_warn = posture_key not in _FAIL_CLOSED_WARNED
            if not token and not disabled:
                _FAIL_CLOSED_COUNT_SINCE_WARN += 1
                if _FAIL_CLOSED_COUNT_SINCE_WARN >= _FAIL_CLOSED_REARM_EVERY:
                    should_warn = True
                    _FAIL_CLOSED_COUNT_SINCE_WARN = 0
                    _FAIL_CLOSED_WARNED.discard(posture_key)
            if should_warn:
                _FAIL_CLOSED_WARNED.add(posture_key)
        if should_warn:
            if not token and not disabled:
                logger.error(
                    "mcp_auth: MCP_TOOL_AUTH_TOKEN is unset/empty and "
                    "MCP_TOOL_AUTH_DISABLED is not set — refusing requests "
                    "(HTTP 401). Set a non-empty token or opt out explicitly."
                )
            elif not token and disabled:
                logger.error(
                    "mcp_auth: MCP_TOOL_AUTH_DISABLED=true — authentication "
                    "is DISABLED; any pod reachable on this Service can invoke "
                    "tools. Use only for local development or inside a "
                    "cluster-internal NetworkPolicy sandbox."
                )
        if disabled:
            await app(scope, receive, send)
            return
        if not token:
            await _send_401(send, "auth-not-configured")
            return
        # Enumerate every Authorization header (#921). The ASGI scope
        # represents headers as a list of (name, value) tuples and duplicates
        # are legal — proxies frequently append an empty Authorization when
        # the client didn't send one. dict(scope headers) silently kept
        # only the last value, which flipped a legitimate bearer request
        # into a 401 behind such a proxy. Gather all values instead and
        # accept if ANY non-empty value matches. Reject with a
        # dedicated reason when every Authorization is empty/malformed.
        auth_values: list[bytes] = [
            v for (k, v) in (scope.get("headers") or []) if k == b"authorization"
        ]
        if not auth_values:
            await _send_401(send, "missing-or-malformed-authorization-header")
            return
        non_empty = [v for v in auth_values if v and v.strip()]
        if not non_empty:
            await _send_401(send, "missing-or-malformed-authorization-header")
            return
        matched = False
        for raw in non_empty:
            if not raw.lower().startswith(b"bearer "):
                continue
            # #1617: decode strictly. The previous errors="replace" path
            # silently substituted U+FFFD for invalid UTF-8 bytes, which
            # let two distinct token byte sequences (one with invalid
            # UTF-8, one with literal U+FFFD chars) collide on the
            # caller-identity hash downstream. Reject the request with a
            # clear 400 so the caller knows to fix the encoding instead
            # of papering over it.
            try:
                presented = raw[7:].decode("utf-8", errors="strict").strip()
            except UnicodeDecodeError:
                await _send_400_invalid_bearer_encoding(send)
                return
            if hmac_mod.compare_digest(presented, token):
                matched = True
                break
        if not matched:
            # Distinguish "had a Bearer, wrong value" from "no Bearer at all"
            # for operator debuggability.
            if any(v.lower().startswith(b"bearer ") for v in non_empty):
                await _send_401(send, "invalid-token")
            else:
                await _send_401(send, "missing-or-malformed-authorization-header")
            return
        # /info is handled after auth so unauthenticated callers can't
        # scrape image SHAs / feature flags. Served from the middleware
        # so tool servers don't need to register a custom Starlette route.
        if (
            info_provider is not None
            and scope.get("path") == "/info"
            and scope.get("method", "GET") == "GET"
        ):
            await _send_info(send, info_provider)
            return
        await app(scope, receive, send)

    return middleware


async def _send_health(send: Callable[[dict], Awaitable[None]]) -> None:
    """Return 200 OK with a minimal JSON body for kubelet probes (#848)."""
    body = b'{"status":"ok"}'
    await send({
        "type": "http.response.start",
        "status": 200,
        "headers": [
            (b"content-type", b"application/json"),
            (b"content-length", str(len(body)).encode("ascii")),
            (b"cache-control", b"no-store"),
        ],
    })
    await send({"type": "http.response.body", "body": body, "more_body": False})


async def _send_info(
    send: Callable[[dict], Awaitable[None]],
    provider: Callable[[], dict[str, Any]],
) -> None:
    """Return the tool server's /info document as JSON (#1122).

    The provider is invoked per request so it picks up any state that
    changes at runtime (e.g. helm-diff is reinstalled without a pod
    restart). A failure in the provider is logged and surfaced as a
    500 with a terse error body rather than propagating the exception
    up the ASGI stack.
    """
    try:
        doc = provider() or {}
        body = json.dumps(doc, default=str).encode("utf-8")
        status = 200
    except Exception as exc:
        logger.warning("mcp /info provider raised: %r", exc)
        body = b'{"error":"info-provider-failed"}'
        status = 500
    await send({
        "type": "http.response.start",
        "status": status,
        "headers": [
            (b"content-type", b"application/json"),
            (b"content-length", str(len(body)).encode("ascii")),
            (b"cache-control", b"no-store"),
        ],
    })
    await send({"type": "http.response.body", "body": body, "more_body": False})


async def _send_400_invalid_bearer_encoding(
    send: Callable[[dict], Awaitable[None]],
) -> None:
    """Reject a request whose Authorization bearer is not valid UTF-8 (#1617).

    Using a JSON-RPC-style envelope (code -32600 / "Invalid Request")
    keeps the response shape consistent with the MCP wire format the
    rest of the tool surface speaks, so MCP clients surface a parseable
    error rather than a free-form string.
    """
    body = (
        b'{"jsonrpc":"2.0","error":{"code":-32600,'
        b'"message":"invalid bearer token encoding (must be UTF-8)"}}'
    )
    await send({
        "type": "http.response.start",
        "status": 400,
        "headers": [
            (b"content-type", b"application/json"),
            (b"content-length", str(len(body)).encode("ascii")),
            (b"cache-control", b"no-store"),
        ],
    })
    await send({"type": "http.response.body", "body": body, "more_body": False})


async def _send_401(send: Callable[[dict], Awaitable[None]], reason: str) -> None:
    body = f'{{"error": "{reason}"}}'.encode("utf-8")
    await send({
        "type": "http.response.start",
        "status": 401,
        "headers": [
            (b"content-type", b"application/json"),
            (b"content-length", str(len(body)).encode("ascii")),
            (b"www-authenticate", b'Bearer realm="mcp"'),
        ],
    })
    await send({"type": "http.response.body", "body": body, "more_body": False})

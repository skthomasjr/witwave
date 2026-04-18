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
import logging
import os
from typing import Awaitable, Callable

logger = logging.getLogger(__name__)

_FAIL_CLOSED_WARNED: set[int] = set()


def _auth_disabled_escape_hatch() -> bool:
    return os.environ.get("MCP_TOOL_AUTH_DISABLED", "").strip().lower() in {
        "1", "true", "yes", "on",
    }


def require_bearer_token(app):  # type: ignore[no-untyped-def]
    """Wrap an ASGI app so every request must carry a valid bearer token.

    Token source: MCP_TOOL_AUTH_TOKEN env var. Compared with
    hmac.compare_digest so the check is timing-safe. When unset /
    empty the server fails closed (401) at first request; operators
    who want to run without auth must set MCP_TOOL_AUTH_DISABLED=true.
    """

    async def middleware(scope, receive, send) -> None:  # type: ignore[no-untyped-def]
        if scope.get("type") != "http":
            await app(scope, receive, send)
            return
        # /health is an unauthenticated liveness/readiness probe target
        # (#848). Kubernetes kubelet probes cannot carry bearer tokens,
        # and the endpoint exposes only static JSON so gating it would
        # add operational risk without security benefit.
        if scope.get("path") == "/health" and scope.get("method", "GET") == "GET":
            await _send_health(send)
            return
        token = os.environ.get("MCP_TOOL_AUTH_TOKEN", "").strip()
        disabled = _auth_disabled_escape_hatch()
        # One-shot warning per process per posture so operators see
        # the misconfig in kubectl logs without per-request spam.
        posture_key = (hash(("tok" if token else "none", disabled))) & 0xFFFFFFFF
        if posture_key not in _FAIL_CLOSED_WARNED:
            _FAIL_CLOSED_WARNED.add(posture_key)
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
            presented = raw[7:].decode("utf-8", errors="replace").strip()
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

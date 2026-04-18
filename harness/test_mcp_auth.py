"""Unit tests for shared/mcp_auth.py bearer-token middleware (#959).

Drives the ASGI callable with synthesized scope / receive / send
channels and a stub downstream app. Covers:

1. /health GET bypass — returns 200 JSON without hitting the inner app
2. Fail-closed when token unset and MCP_TOOL_AUTH_DISABLED unset → 401
3. Fail-open opt-out when MCP_TOOL_AUTH_DISABLED=true → inner app invoked
4. Missing Authorization header → 401 missing-or-malformed
5. Empty Authorization header (proxy-inserted) → 401
6. Non-Bearer Authorization (e.g. Basic) → 401 missing-or-malformed
7. Bearer with wrong value → 401 invalid-token
8. Bearer with correct value → inner app invoked
9. Duplicate Authorization headers (proxy appends empty) — still accepts
   when one of them carries the right bearer (#921)
10. Non-HTTP scope passes straight through (websocket, lifespan)
"""

from __future__ import annotations

import asyncio
import importlib
import sys
from pathlib import Path

import pytest

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


def _reload(monkeypatch, token: str = "", disabled: str = ""):
    monkeypatch.setenv("MCP_TOOL_AUTH_TOKEN", token)
    monkeypatch.setenv("MCP_TOOL_AUTH_DISABLED", disabled)
    import mcp_auth as _m  # type: ignore
    importlib.reload(_m)
    # Clear the per-posture one-shot-warn memo so each test sees the
    # initial posture the middleware itself observes.
    _m._FAIL_CLOSED_WARNED.clear()
    return _m


class _FakeInner:
    """Downstream ASGI app — records a single invocation."""

    def __init__(self) -> None:
        self.invoked = False
        self.last_scope: dict | None = None

    async def __call__(self, scope, receive, send):
        self.invoked = True
        self.last_scope = scope
        # 200 empty for realism.
        await send({"type": "http.response.start", "status": 200, "headers": []})
        await send({"type": "http.response.body", "body": b"", "more_body": False})


class _ResponseSink:
    """Captures response start + body events fired by the middleware."""

    def __init__(self) -> None:
        self.events: list[dict] = []

    async def __call__(self, event):
        self.events.append(event)

    @property
    def status(self) -> int:
        for e in self.events:
            if e.get("type") == "http.response.start":
                return int(e.get("status", 0))
        return 0

    def body(self) -> bytes:
        return b"".join(
            e.get("body", b"") for e in self.events if e.get("type") == "http.response.body"
        )


async def _empty_receive():
    return {"type": "http.request", "body": b"", "more_body": False}


def _http_scope(path: str = "/mcp", method: str = "POST", headers=None) -> dict:
    return {
        "type": "http",
        "method": method,
        "path": path,
        "headers": headers or [],
    }


def _run(coro):
    return asyncio.get_event_loop_policy().new_event_loop().run_until_complete(coro)


# ----- 1. /health bypass -----------------------------------------


def test_health_bypass_returns_200(monkeypatch):
    m = _reload(monkeypatch, token="t", disabled="")
    inner = _FakeInner()
    sink = _ResponseSink()
    mw = m.require_bearer_token(inner)
    _run(mw(_http_scope(path="/health", method="GET"), _empty_receive, sink))
    assert not inner.invoked, "inner app must NOT be invoked for /health"
    assert sink.status == 200
    assert b'"status"' in sink.body()


# ----- 2 + 3. fail-closed / disabled escape hatch ----------------


def test_fail_closed_when_token_missing_and_not_disabled(monkeypatch):
    m = _reload(monkeypatch, token="", disabled="")
    inner = _FakeInner()
    sink = _ResponseSink()
    mw = m.require_bearer_token(inner)
    _run(mw(_http_scope(), _empty_receive, sink))
    assert sink.status == 401
    assert b"auth-not-configured" in sink.body()
    assert not inner.invoked


def test_escape_hatch_when_disabled_true(monkeypatch):
    m = _reload(monkeypatch, token="", disabled="true")
    inner = _FakeInner()
    sink = _ResponseSink()
    mw = m.require_bearer_token(inner)
    _run(mw(_http_scope(), _empty_receive, sink))
    assert inner.invoked, "inner app should run when MCP_TOOL_AUTH_DISABLED=true"


# ----- 4-7. header shape matrix ----------------------------------


def test_missing_authorization_header(monkeypatch):
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    _run(m.require_bearer_token(inner)(_http_scope(), _empty_receive, sink))
    assert sink.status == 401
    assert b"missing-or-malformed-authorization-header" in sink.body()


def test_empty_authorization_header(monkeypatch):
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    scope = _http_scope(headers=[(b"authorization", b"")])
    _run(m.require_bearer_token(inner)(scope, _empty_receive, sink))
    assert sink.status == 401
    assert b"missing-or-malformed-authorization-header" in sink.body()


def test_non_bearer_authorization_is_rejected(monkeypatch):
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    scope = _http_scope(headers=[(b"authorization", b"Basic dXNlcjpwYXNz")])
    _run(m.require_bearer_token(inner)(scope, _empty_receive, sink))
    assert sink.status == 401
    # Non-Bearer means the check short-circuits to missing-or-malformed.
    assert b"missing-or-malformed-authorization-header" in sink.body()


def test_wrong_bearer_value(monkeypatch):
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    scope = _http_scope(headers=[(b"authorization", b"Bearer wrong")])
    _run(m.require_bearer_token(inner)(scope, _empty_receive, sink))
    assert sink.status == 401
    assert b"invalid-token" in sink.body()


def test_correct_bearer_value(monkeypatch):
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    scope = _http_scope(headers=[(b"authorization", b"Bearer secret")])
    _run(m.require_bearer_token(inner)(scope, _empty_receive, sink))
    assert inner.invoked
    assert sink.status == 200


# ----- 9. duplicate Authorization headers (#921) -----------------


def test_multiple_authorization_headers_first_empty(monkeypatch):
    """Proxy-inserted empty Authorization + legitimate one should pass."""
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    scope = _http_scope(headers=[
        (b"authorization", b""),
        (b"authorization", b"Bearer secret"),
    ])
    _run(m.require_bearer_token(inner)(scope, _empty_receive, sink))
    assert inner.invoked, "middleware must accept when any header carries the right bearer (#921)"


# ----- 10. non-http scopes pass through --------------------------


def test_non_http_scope_passthrough(monkeypatch):
    m = _reload(monkeypatch, token="secret")
    inner = _FakeInner()
    sink = _ResponseSink()
    scope = {"type": "lifespan"}
    _run(m.require_bearer_token(inner)(scope, _empty_receive, sink))
    assert inner.invoked

"""Regression tests for harness /api/traces auth parity.

The harness trace aggregator is intentionally auth-compatible with
``/conversations`` and ``/trace``: inbound callers use
``CONVERSATIONS_AUTH_TOKEN`` and backend fan-out uses
``BACKEND_CONVERSATIONS_AUTH_TOKEN``. These source-level checks mirror
the existing A2A retry wiring tests and guard against future drift in
the nested handler code.
"""

from __future__ import annotations

from pathlib import Path

MAIN_SRC = (Path(__file__).parent / "main.py").read_text()


def _section(start_marker: str, end_marker: str) -> str:
    start = MAIN_SRC.index(start_marker)
    end = MAIN_SRC.index(end_marker, start)
    return MAIN_SRC[start:end]


def test_api_traces_handlers_fail_closed_like_conversations() -> None:
    helper = _section("    def _require_conversations_auth", "    async def _fetch_conversations")
    assert "auth_disabled_escape_hatch()" in helper
    assert 'JSONResponse({"error": "auth not configured"}, status_code=503)' in helper
    assert 'JSONResponse({"error": "unauthorized"}, status_code=401)' in helper

    handlers = [
        _section("    async def otel_traces_list_handler", "    async def otel_traces_detail_handler"),
        _section("    async def otel_traces_detail_handler", "    # ---- SSE event stream"),
    ]
    for handler in handlers:
        assert "unauthorized = _require_conversations_auth(request)" in handler
        assert "if unauthorized is not None:" in handler
        assert "return unauthorized" in handler


def test_remote_api_traces_fanout_forwards_backend_auth_header() -> None:
    fetchers = [
        _section("    async def _fetch_remote_traces", "    async def _fetch_remote_trace_by_id"),
        _section("    async def _fetch_remote_trace_by_id", "    # Short-TTL cache"),
    ]
    for fetcher in fetchers:
        assert "headers: dict[str, str] = {}" in fetcher
        assert 'headers["Authorization"] = f"Bearer {_backend_conversations_auth_token}"' in fetcher
        assert "headers=headers" in fetcher

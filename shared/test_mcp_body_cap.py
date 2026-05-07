"""Unit tests for ``shared/mcp_body_cap.read_capped_body`` (#1609, #1673, #1674).

The helper enforces a byte-cap on actual bytes received, regardless
of what the caller declares in ``Content-Length``. The tests target
the helper directly — no backend stubs needed since the helper is a
pure asyncio + Starlette utility.

Run with ``pytest shared/test_mcp_body_cap.py``.
"""

from __future__ import annotations

import asyncio

import pytest
from mcp_body_cap import read_capped_body
from starlette.requests import Request


def _make_request(chunks: list[bytes], declared_content_length: int | None) -> Request:
    """Build a minimal Starlette ``Request`` whose body arrives in
    arbitrary chunks regardless of declared Content-Length."""
    headers: list[tuple[bytes, bytes]] = [(b"content-type", b"application/json")]
    if declared_content_length is not None:
        headers.append((b"content-length", str(declared_content_length).encode()))

    scope = {
        "type": "http",
        "asgi": {"version": "3.0"},
        "http_version": "1.1",
        "method": "POST",
        "scheme": "http",
        "path": "/mcp",
        "raw_path": b"/mcp",
        "query_string": b"",
        "root_path": "",
        "headers": headers,
        "server": ("testserver", 80),
        "client": ("testclient", 12345),
    }

    queue: list[dict] = []
    if not chunks:
        # An empty body is still a real ASGI message — one terminator
        # chunk with body=b"" and more_body=False — not the absence of
        # any message at all (which Starlette reads as a client
        # disconnect).
        queue.append({"type": "http.request", "body": b"", "more_body": False})
    else:
        for i, chunk in enumerate(chunks):
            queue.append(
                {
                    "type": "http.request",
                    "body": chunk,
                    "more_body": i < len(chunks) - 1,
                }
            )

    async def _receive():
        if queue:
            return queue.pop(0)
        return {"type": "http.disconnect"}

    return Request(scope, _receive)


def test_streaming_overflow_rejected_with_body_too_large_reason():
    """Caller declares small Content-Length but streams >cap bytes.

    The streaming check MUST trip on actual bytes received and return
    ``body_too_large``, not silently buffer the oversize payload.
    """
    cap = 1024  # 1 KiB
    # Five 512-byte chunks = 2560 bytes total, well over the 1024 cap,
    # but Content-Length lies and says 100 so the fast-path doesn't
    # fire.
    chunks = [b"x" * 512 for _ in range(5)]
    req = _make_request(chunks, declared_content_length=100)

    body, reason = asyncio.run(read_capped_body(req, cap))

    assert body is None
    assert reason == "body_too_large"


def test_streaming_overflow_rejected_when_no_content_length_header():
    """No Content-Length at all (e.g. chunked transfer): streaming
    check is the ONLY enforcement and must trip."""
    cap = 256
    chunks = [b"a" * 100, b"b" * 200]  # 300 bytes > 256 cap
    req = _make_request(chunks, declared_content_length=None)

    body, reason = asyncio.run(read_capped_body(req, cap))

    assert body is None
    assert reason == "body_too_large"


def test_under_cap_request_succeeds_and_returns_concatenated_body():
    """Body well under the cap is returned intact for json.loads."""
    cap = 4 * 1024 * 1024
    chunks = [
        b'{"jsonrpc":',
        b'"2.0",',
        b'"method":"tools/list",',
        b'"id":1}',
    ]
    req = _make_request(chunks, declared_content_length=sum(len(c) for c in chunks))

    body, reason = asyncio.run(read_capped_body(req, cap))

    assert reason is None
    assert body == b"".join(chunks)
    import json

    parsed = json.loads(body)
    assert parsed["method"] == "tools/list"
    assert parsed["id"] == 1


def test_cap_boundary_exact_size_passes_one_byte_over_rejected():
    """Body exactly at the cap is allowed; one byte over is not."""
    cap = 1024
    exact = b"x" * cap

    req = _make_request([exact], declared_content_length=cap)
    body, reason = asyncio.run(read_capped_body(req, cap))
    assert reason is None
    assert body == exact

    over = b"x" * (cap + 1)
    req = _make_request([over], declared_content_length=cap + 1)
    body, reason = asyncio.run(read_capped_body(req, cap))
    assert body is None
    assert reason == "body_too_large"


def test_empty_body_returns_empty_bytes():
    """Zero-length body is valid and returns ``b''``."""
    cap = 1024
    req = _make_request([], declared_content_length=0)
    body, reason = asyncio.run(read_capped_body(req, cap))
    assert reason is None
    assert body == b""


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))

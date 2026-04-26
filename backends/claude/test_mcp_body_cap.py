"""Unit tests for the /mcp body-size cap (#1609).

Verifies that ``_read_capped_body`` enforces ``MCP_MAX_BODY_BYTES`` on
actual bytes received, even when the caller declares a small (or
absent) Content-Length and then streams more than the cap. Also
verifies the under-cap happy path returns the buffered body intact.

The cap is enforced in two places in ``_mcp_handler_inner``:

1. **Fast path** — Content-Length pre-check rejects an honest caller
   declaring a too-large payload before reading any body bytes.
2. **Streaming enforcement** — ``_read_capped_body`` counts actual bytes
   chunk-by-chunk and aborts BEFORE buffering them into ``json.loads``.
   This is the actual security boundary; (1) is just an optimisation.

Tests target (2) since (1) is trivially correct. Heavy dependencies
(``a2a``, ``conversations``, ``executor``, ``metrics``, ``prometheus_client``,
etc.) are stubbed before importing ``main`` so the module can load in a
plain unit-test env.

Run with ``pytest backends/claude/test_mcp_body_cap.py``.
"""
from __future__ import annotations

import asyncio
import os
import sys
import tempfile
import types
from pathlib import Path
from unittest.mock import MagicMock

import pytest


# ---------------------------------------------------------------------------
# Bootstrap: stub heavy imports + redirect log paths so ``import main``
# works in a plain unit-test env.
# ---------------------------------------------------------------------------
_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

_log_tmp_dir = tempfile.mkdtemp(prefix="claude-test-")
os.environ.setdefault("CONVERSATION_LOG", os.path.join(_log_tmp_dir, "conversation.jsonl"))
os.environ.setdefault("TRACE_LOG", os.path.join(_log_tmp_dir, "tool-activity.jsonl"))
os.environ.setdefault("AGENT_NAME", "claude-test")
os.environ.setdefault("AGENT_ID", "claude-test-0")
os.environ.setdefault("AGENT_OWNER", "test")


def _install_stub(name: str, module: types.ModuleType) -> None:
    sys.modules.setdefault(name, module)


# prometheus_client stub
if "prometheus_client" not in sys.modules:
    _pc = types.ModuleType("prometheus_client")

    class _Metric:
        def __init__(self, *a, **kw):
            pass

        def labels(self, *a, **kw):
            return self

        def inc(self, *a, **kw):
            pass

        def dec(self, *a, **kw):
            pass

        def set(self, *a, **kw):
            pass

        def observe(self, *a, **kw):
            pass

        def info(self, *a, **kw):
            pass

        def set_function(self, *a, **kw):
            pass

    _pc.Counter = _Metric
    _pc.Gauge = _Metric
    _pc.Histogram = _Metric
    _pc.Info = _Metric
    _exposition = types.ModuleType("prometheus_client.exposition")
    _exposition.generate_latest = lambda: b""
    _exposition.CONTENT_TYPE_LATEST = "text/plain"
    _pc.exposition = _exposition
    _install_stub("prometheus_client", _pc)
    _install_stub("prometheus_client.exposition", _exposition)

# uvicorn stub
if "uvicorn" not in sys.modules:
    _uv = types.ModuleType("uvicorn")
    _uv.run = lambda *a, **kw: None

    class _Config:
        def __init__(self, *a, **kw):
            pass

    class _Server:
        def __init__(self, *a, **kw):
            pass

        async def serve(self):
            pass

    _uv.Config = _Config
    _uv.Server = _Server
    _install_stub("uvicorn", _uv)

# a2a stubs
if "a2a" not in sys.modules:
    _a2a = types.ModuleType("a2a")
    _a2a_server = types.ModuleType("a2a.server")
    _a2a_apps = types.ModuleType("a2a.server.apps")
    _a2a_rh = types.ModuleType("a2a.server.request_handlers")
    _a2a_tasks = types.ModuleType("a2a.server.tasks")
    _a2a_types = types.ModuleType("a2a.types")

    class _A2AStarletteApplication:
        def __init__(self, *a, **kw):
            pass

        def build(self):
            return MagicMock()

    class _DefaultRequestHandler:
        def __init__(self, *a, **kw):
            pass

    class _InMemoryTaskStore:
        pass

    class _AgentCapabilities:
        def __init__(self, **kw):
            self.__dict__.update(kw)

    class _AgentCard:
        def __init__(self, **kw):
            self.__dict__.update(kw)

    class _AgentSkill:
        def __init__(self, **kw):
            self.__dict__.update(kw)

    _a2a_apps.A2AStarletteApplication = _A2AStarletteApplication
    _a2a_rh.DefaultRequestHandler = _DefaultRequestHandler
    _a2a_tasks.InMemoryTaskStore = _InMemoryTaskStore
    _a2a_types.AgentCapabilities = _AgentCapabilities
    _a2a_types.AgentCard = _AgentCard
    _a2a_types.AgentSkill = _AgentSkill
    _install_stub("a2a", _a2a)
    _install_stub("a2a.server", _a2a_server)
    _install_stub("a2a.server.apps", _a2a_apps)
    _install_stub("a2a.server.request_handlers", _a2a_rh)
    _install_stub("a2a.server.tasks", _a2a_tasks)
    _install_stub("a2a.types", _a2a_types)

# sqlite_task_store stub
if "sqlite_task_store" not in sys.modules:
    _sts = types.ModuleType("sqlite_task_store")

    class _SqliteTaskStore:
        def __init__(self, *a, **kw):
            pass

    _sts.SqliteTaskStore = _SqliteTaskStore
    _install_stub("sqlite_task_store", _sts)

# conversations stub (lives in shared/ but pulls in heavy deps)
if "conversations" not in sys.modules:
    _conv = types.ModuleType("conversations")
    _conv.auth_disabled_escape_hatch = lambda: False
    _conv.make_conversations_handler = lambda *a, **kw: (lambda req: None)
    _conv.make_trace_handler = lambda *a, **kw: (lambda req: None)
    _install_stub("conversations", _conv)

# executor stub
if "executor" not in sys.modules:
    _ex = types.ModuleType("executor")

    class _AgentExecutor:
        def __init__(self, *a, **kw):
            pass

    _ex.AgentExecutor = _AgentExecutor
    _install_stub("executor", _ex)

# session_binding stub
if "session_binding" not in sys.modules:
    _sb = types.ModuleType("session_binding")
    _sb.derive_session_id = lambda *a, **kw: "sid"
    _sb.set_fallback_counter = lambda *a, **kw: None
    _install_stub("session_binding", _sb)

# session_stream stub
if "session_stream" not in sys.modules:
    _ss = types.ModuleType("session_stream")
    _ss.make_session_stream_handler = lambda *a, **kw: (lambda req: None)
    _install_stub("session_stream", _ss)

# metrics stub — main.py imports many specific symbols by name
if "metrics" not in sys.modules:
    _m = types.ModuleType("metrics")
    for _sym in (
        "backend_event_loop_lag_seconds",
        "backend_health_checks_total",
        "backend_info",
        "backend_mcp_request_duration_seconds",
        "backend_mcp_requests_total",
        "backend_sdk_info",
        "backend_session_binding_fallback_total",
        "backend_session_caller_cardinality",
        "backend_startup_duration_seconds",
        "backend_task_restarts_total",
        "backend_up",
        "backend_uptime_seconds",
    ):
        setattr(_m, _sym, None)
    _install_stub("metrics", _m)


# Now the import succeeds.
import main  # noqa: E402

from starlette.requests import Request  # noqa: E402


# ---------------------------------------------------------------------------
# ASGI helpers: build a Request whose body arrives in arbitrary chunks,
# regardless of declared Content-Length.
# ---------------------------------------------------------------------------
def _make_request(chunks: list[bytes], declared_content_length: int | None) -> Request:
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
    for i, chunk in enumerate(chunks):
        queue.append({
            "type": "http.request",
            "body": chunk,
            "more_body": i < len(chunks) - 1,
        })

    async def _receive():
        if queue:
            return queue.pop(0)
        return {"type": "http.disconnect"}

    return Request(scope, _receive)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------
def test_streaming_overflow_rejected_with_body_too_large_reason():
    """Caller declares small Content-Length but streams >cap bytes.

    The streaming check MUST trip on actual bytes received and return
    'body_too_large' (which the handler maps to HTTP 413), not silently
    buffer the oversize payload into json.loads.
    """
    cap = 1024  # 1 KiB
    # Five 512-byte chunks = 2560 bytes total, well over the 1024 cap,
    # but Content-Length lies and says 100 so the fast-path doesn't fire.
    chunks = [b"x" * 512 for _ in range(5)]
    req = _make_request(chunks, declared_content_length=100)

    body, reason = asyncio.run(main._read_capped_body(req, cap))

    assert body is None
    assert reason == "body_too_large"


def test_streaming_overflow_rejected_when_no_content_length_header():
    """No Content-Length at all (e.g. chunked transfer): streaming
    check is the ONLY enforcement and must trip."""
    cap = 256
    chunks = [b"a" * 100, b"b" * 200]  # 300 bytes > 256 cap
    req = _make_request(chunks, declared_content_length=None)

    body, reason = asyncio.run(main._read_capped_body(req, cap))

    assert body is None
    assert reason == "body_too_large"


def test_under_cap_request_succeeds_and_returns_concatenated_body():
    """Body well under the cap is returned intact for json.loads."""
    cap = 4 * 1024 * 1024
    chunks = [b'{"jsonrpc":', b'"2.0",', b'"method":"tools/list",', b'"id":1}']
    req = _make_request(chunks, declared_content_length=sum(len(c) for c in chunks))

    body, reason = asyncio.run(main._read_capped_body(req, cap))

    assert reason is None
    assert body == b''.join(chunks)
    # And it parses as valid JSON-RPC.
    import json
    parsed = json.loads(body)
    assert parsed["method"] == "tools/list"
    assert parsed["id"] == 1


def test_cap_boundary_exact_size_passes():
    """Body exactly at the cap is allowed; one byte over is not."""
    cap = 1024
    exact = b"x" * cap

    req = _make_request([exact], declared_content_length=cap)
    body, reason = asyncio.run(main._read_capped_body(req, cap))
    assert reason is None
    assert body == exact

    over = b"x" * (cap + 1)
    req = _make_request([over], declared_content_length=cap + 1)
    body, reason = asyncio.run(main._read_capped_body(req, cap))
    assert body is None
    assert reason == "body_too_large"


def test_env_var_override_picked_up_at_module_load():
    """``MCP_MAX_BODY_BYTES`` env var overrides the 4 MiB default.

    This test asserts the constant exists and is a positive int. Since
    the env var is read at module import time, full override behaviour
    is exercised by the next process that imports ``main`` with the
    env set — but the contract (``int`` > 0) is verifiable here.
    """
    assert isinstance(main._MCP_MAX_BODY_BYTES, int)
    assert main._MCP_MAX_BODY_BYTES > 0


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))

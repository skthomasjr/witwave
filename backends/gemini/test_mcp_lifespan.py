"""Unit tests for gemini MCP lifespan + AFC plumbing (#640).

These tests stub out the external SDKs (``google-genai``, ``mcp``) and the
a2a-sdk base classes so the executor module can be imported and exercised
without a running container. The goal is to verify:

1. ``AgentExecutor._apply_mcp_config({})`` is a no-op.
2. ``AgentExecutor._apply_mcp_config({...stdio entry...})`` populates
   ``_live_mcp_servers`` (with ``stdio_client`` / ``ClientSession`` mocked).
3. ``run_query`` threads ``live_mcp_servers`` into
   ``GenerateContentConfig(tools=[...])`` on the mocked ``genai.Client``.
"""

from __future__ import annotations

import asyncio
import os
import sys
import types
import unittest
from contextlib import asynccontextmanager
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

# ---------------------------------------------------------------------------
# Lightweight shared/ + a2a-sdk shims so importing ``executor`` does not require
# the full runtime. Only what the module touches at import time is stubbed.
# ---------------------------------------------------------------------------
_HERE = Path(__file__).resolve().parent
# _HERE is backends/gemini/ — the repo root is two levels up, not one.
_REPO_ROOT = _HERE.parent.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

os.environ.setdefault("GEMINI_API_KEY", "test-key")
os.environ.setdefault("AGENT_NAME", "gemini-test")
os.environ.setdefault("AGENT_OWNER", "test")
os.environ.setdefault("AGENT_ID", "gemini")
# Redirect log paths off /home/agent (which doesn't exist in the test env) so
# log_entry / log_trace writes silently no-op into a tmp file rather than
# spamming stderr with ENOENT.
import tempfile as _tempfile

_log_tmp_dir = _tempfile.mkdtemp(prefix="gemini-test-")
os.environ.setdefault("CONVERSATION_LOG", os.path.join(_log_tmp_dir, "conversation.jsonl"))
os.environ.setdefault("TRACE_LOG", os.path.join(_log_tmp_dir, "tool-activity.jsonl"))
os.environ.setdefault("SESSION_STORE_DIR", os.path.join(_log_tmp_dir, "sessions"))
# Widen the MCP command allow-list so the stdio fixture (/bin/echo) passes
# the #730 / #862 guard in apply_mcp_config. The test only verifies lifespan
# plumbing — the allow-list itself has dedicated coverage elsewhere.
os.environ.setdefault("MCP_ALLOWED_COMMAND_PREFIXES", "/bin/echo,/usr/bin/,/bin/")


def _install_stub_module(name: str, module: types.ModuleType) -> None:
    sys.modules.setdefault(name, module)


# google.genai stub
if "google.genai" not in sys.modules:
    _genai = types.ModuleType("google.genai")
    _types = types.ModuleType("google.genai.types")

    class _GenerateContentConfig:
        def __init__(self, **kwargs):
            self.kwargs = kwargs
            for k, v in kwargs.items():
                setattr(self, k, v)

    class _Content:
        def __init__(self, role, parts):
            self.role = role
            self.parts = parts

    class _Part:
        def __init__(self, **kwargs):
            for k, v in kwargs.items():
                setattr(self, k, v)

    _types.GenerateContentConfig = _GenerateContentConfig
    _types.Content = _Content
    _types.Part = _Part

    class _Client:
        def __init__(self, *a, **kw):
            self.aio = MagicMock()

    _genai.Client = _Client
    _genai.types = _types
    _google = types.ModuleType("google")
    _google.genai = _genai
    _install_stub_module("google", _google)
    _install_stub_module("google.genai", _genai)
    _install_stub_module("google.genai.types", _types)

# a2a-sdk stubs (only the imports executor.py touches at module load).
if "a2a.server.agent_execution" not in sys.modules:
    _a2a = types.ModuleType("a2a")
    _a2a_server = types.ModuleType("a2a.server")
    _a2a_server_ae = types.ModuleType("a2a.server.agent_execution")
    _a2a_server_events = types.ModuleType("a2a.server.events")
    _a2a_utils = types.ModuleType("a2a.utils")

    class _AgentExecutor:
        pass

    class _RequestContext:
        pass

    class _EventQueue:
        pass

    _a2a_server_ae.AgentExecutor = _AgentExecutor
    _a2a_server_ae.RequestContext = _RequestContext
    _a2a_server_events.EventQueue = _EventQueue
    _a2a_utils.new_agent_text_message = lambda text: {"text": text}

    _install_stub_module("a2a", _a2a)
    _install_stub_module("a2a.server", _a2a_server)
    _install_stub_module("a2a.server.agent_execution", _a2a_server_ae)
    _install_stub_module("a2a.server.events", _a2a_server_events)
    _install_stub_module("a2a.utils", _a2a_utils)

# yaml stub — shared/hooks_engine.py imports ``yaml`` at module load.
if "yaml" not in sys.modules:
    _yaml = types.ModuleType("yaml")

    class _YAMLError(Exception):
        pass

    _yaml.YAMLError = _YAMLError
    _yaml.safe_load = lambda _s: {}
    _install_stub_module("yaml", _yaml)


# prometheus_client stub — avoid installing the real dep in the unit-test env.
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
    _install_stub_module("prometheus_client", _pc)


import executor  # noqa: E402
from executor import AgentExecutor, run_query  # noqa: E402


def _run(coro):
    return asyncio.get_event_loop().run_until_complete(coro) if False else asyncio.run(coro)


class ApplyMcpConfigEmptyTests(unittest.TestCase):
    def test_empty_config_is_noop(self):
        ex = AgentExecutor()
        _run(ex._apply_mcp_config({}))
        self.assertEqual(ex._live_mcp_servers, [])
        self.assertIsNone(ex._mcp_stack)


class ApplyMcpConfigStdioTests(unittest.TestCase):
    def test_stdio_entry_populates_live_servers(self):
        ex = AgentExecutor()

        fake_session = MagicMock(name="ClientSession")
        fake_session.initialize = AsyncMock()

        @asynccontextmanager
        async def fake_stdio_client(_params):
            yield ("read-stream", "write-stream")

        @asynccontextmanager
        async def fake_client_session_ctx(_read, _write):
            yield fake_session

        class _FakeClientSession:
            def __init__(self, *a, **kw):
                pass

            async def __aenter__(self_inner):
                return fake_session

            async def __aexit__(self_inner, *exc):
                return False

        class _FakeStdioParams:
            def __init__(self, **kwargs):
                self.__dict__.update(kwargs)

        # Install a fake ``mcp`` module so executor's deferred import succeeds.
        _mcp = types.ModuleType("mcp")
        _mcp.ClientSession = _FakeClientSession
        _mcp.StdioServerParameters = _FakeStdioParams
        _mcp_client = types.ModuleType("mcp.client")
        _mcp_client_stdio = types.ModuleType("mcp.client.stdio")
        _mcp_client_stdio.stdio_client = fake_stdio_client
        sys.modules["mcp"] = _mcp
        sys.modules["mcp.client"] = _mcp_client
        sys.modules["mcp.client.stdio"] = _mcp_client_stdio
        try:
            cfg = {"echo": {"command": "/bin/echo", "args": ["hi"]}}
            _run(ex._apply_mcp_config(cfg))
            self.assertEqual(len(ex._live_mcp_servers), 1)
            self.assertIs(ex._live_mcp_servers[0], fake_session)
            # Tear down
            _run(ex._apply_mcp_config({}))
            self.assertEqual(ex._live_mcp_servers, [])
        finally:
            for m in ("mcp", "mcp.client", "mcp.client.stdio"):
                sys.modules.pop(m, None)


class RunQueryPassesToolsTests(unittest.TestCase):
    def test_live_mcp_servers_flow_into_generate_content_config(self):
        captured_configs: list = []

        class _FakeChat:
            def __init__(self):
                self.history = []

            async def send_message_stream(self, _prompt):
                async def _gen():
                    return
                    yield  # unreachable

                return _gen()

        class _FakeAioChats:
            def create(self, *, model, config, history):
                captured_configs.append(config)
                return _FakeChat()

        class _FakeAio:
            def __init__(self):
                self.chats = _FakeAioChats()

        class _FakeClient:
            def __init__(self):
                self.aio = _FakeAio()

        fake_client = _FakeClient()
        fake_session = MagicMock(name="ClientSession-live")

        # Patch _get_client so run_query uses the fake.
        with (
            patch.object(executor, "_get_client", return_value=fake_client),
            patch.object(executor, "_load_history", return_value=[]),
            patch.object(executor, "_save_history", new=AsyncMock(return_value=None)),
        ):
            session_locks: dict = {}
            _run(
                run_query(
                    prompt="hello",
                    session_id="00000000-0000-0000-0000-000000000001",
                    agent_md_content="",
                    session_locks=session_locks,
                    history_save_failed=set(),
                    model=None,
                    max_tokens=None,
                    on_chunk=None,
                    live_mcp_servers=[fake_session],
                )
            )

        self.assertEqual(len(captured_configs), 1)
        cfg = captured_configs[0]
        self.assertTrue(hasattr(cfg, "tools"))
        self.assertEqual(cfg.tools, [fake_session])


if __name__ == "__main__":
    unittest.main()

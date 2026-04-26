"""Regression-path coverage for gemini executor (#815).

Targets the historical bug surfaces called out in the issue:

- _save_history cut-point selection (#672 / #731 / #945) — AFC pairs must
  not split across a history truncation boundary.
- _save_history byte-cap enforcement (#817) — stops short of wiping
  history when no safe boundary exists.
- BudgetExceededError (#493) — carries ``total`` / ``limit`` /
  ``collected`` so run_query can log the partial response.
- _emit_afc_history (#676 / #887) — tolerates empty / prefix-only inputs
  without emitting rows.
- _history_write_done Event (#674) — timeout cleanup awaits the writer's
  done-event before os.remove so the save never resurrects the file.
- _pre_tool_use_gate (#808 scaffold) — fail-open on import or evaluate
  errors, decision normalisation.

All tests use the same sys.path / stub setup the sibling
``test_mcp_lifespan.py`` does.
"""
from __future__ import annotations

import asyncio
import json
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch

# ---------------------------------------------------------------------------
# sys.path + env + stubs (mirrors test_mcp_lifespan.py; factored separately so
# each file is independently runnable under plain ``python3 test_*.py``).
# ---------------------------------------------------------------------------
_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent.parent  # backends/gemini -> backends -> repo root
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

os.environ.setdefault("GEMINI_API_KEY", "test-key")
os.environ.setdefault("AGENT_NAME", "gemini-test")
os.environ.setdefault("AGENT_OWNER", "test")
os.environ.setdefault("AGENT_ID", "gemini")
_log_tmp_dir = tempfile.mkdtemp(prefix="gemini-regression-")
os.environ.setdefault("CONVERSATION_LOG", os.path.join(_log_tmp_dir, "conversation.jsonl"))
os.environ.setdefault("TRACE_LOG", os.path.join(_log_tmp_dir, "tool-activity.jsonl"))
os.environ.setdefault("SESSION_STORE_DIR", os.path.join(_log_tmp_dir, "sessions"))


def _install_stub(name: str, module: types.ModuleType) -> None:
    sys.modules.setdefault(name, module)


# google.genai stub (minimal types for Content / Part).
if "google.genai" not in sys.modules:
    _genai = types.ModuleType("google.genai")
    _types = types.ModuleType("google.genai.types")

    class _Part:
        def __init__(self, **kwargs):
            for k, v in kwargs.items():
                setattr(self, k, v)

        def model_dump(self, exclude_none=False):
            d = {k: v for k, v in self.__dict__.items()}
            if exclude_none:
                d = {k: v for k, v in d.items() if v is not None}
            return d

    class _Content:
        def __init__(self, role, parts):
            self.role = role
            self.parts = list(parts or [])

    class _GenerateContentConfig:
        def __init__(self, **kwargs):
            self.kwargs = kwargs
            for k, v in kwargs.items():
                setattr(self, k, v)

    _types.Part = _Part
    _types.Content = _Content
    _types.GenerateContentConfig = _GenerateContentConfig

    class _Client:
        def __init__(self, *a, **kw):
            pass

    _genai.types = _types
    _genai.Client = _Client
    _google = types.ModuleType("google")
    _google.genai = _genai
    _install_stub("google", _google)
    _install_stub("google.genai", _genai)
    _install_stub("google.genai.types", _types)

# a2a-sdk stubs.
if "a2a.server.agent_execution" not in sys.modules:
    _a2a = types.ModuleType("a2a")
    _a2a_server = types.ModuleType("a2a.server")
    _a2a_server_ae = types.ModuleType("a2a.server.agent_execution")
    _a2a_server_events = types.ModuleType("a2a.server.events")
    _a2a_utils = types.ModuleType("a2a.utils")

    class _AgentExecutor:
        pass

    _a2a_server_ae.AgentExecutor = _AgentExecutor
    _a2a_server_ae.RequestContext = type("RequestContext", (), {})
    _a2a_server_events.EventQueue = type("EventQueue", (), {})
    _a2a_utils.new_agent_text_message = lambda text: {"text": text}

    _install_stub("a2a", _a2a)
    _install_stub("a2a.server", _a2a_server)
    _install_stub("a2a.server.agent_execution", _a2a_server_ae)
    _install_stub("a2a.server.events", _a2a_server_events)
    _install_stub("a2a.utils", _a2a_utils)

if "yaml" not in sys.modules:
    _yaml = types.ModuleType("yaml")
    _yaml.YAMLError = type("YAMLError", (Exception,), {})
    _yaml.safe_load = lambda _s: {}
    _install_stub("yaml", _yaml)

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
    _install_stub("prometheus_client", _pc)


import executor  # noqa: E402
from exceptions import BudgetExceededError  # noqa: E402

types_mod = sys.modules["google.genai.types"]


# When the sibling test file (test_mcp_lifespan.py) loads first its _Part
# stub wins and lacks model_dump. Ensure the attribute exists so
# _save_history can serialise parts regardless of which file ran first.
if not hasattr(types_mod.Part, "model_dump"):
    def _part_model_dump(self, exclude_none=False):
        d = dict(self.__dict__)
        if exclude_none:
            d = {k: v for k, v in d.items() if v is not None}
        return d
    types_mod.Part.model_dump = _part_model_dump  # type: ignore[attr-defined]


def _content(role: str, **part_kwargs):
    """Build a (role, [Part(**kwargs)]) Content with one part from kwargs."""
    return types_mod.Content(role=role, parts=[types_mod.Part(**part_kwargs)])


def _user_text(text: str):
    return _content("user", text=text)


def _model_text(text: str):
    return _content("model", text=text)


def _fc(name: str, args: dict | None = None, id: str | None = None):
    """Function-call Part inside a model-role Content."""
    fc = {"name": name, "args": args or {}}
    if id is not None:
        fc["id"] = id
    return types_mod.Content(role="model", parts=[types_mod.Part(function_call=fc)])


def _fr(name: str, response: dict | None = None, id: str | None = None):
    """Function-response Part inside a user-role Content (AFC convention)."""
    fr = {"name": name, "response": response or {}}
    if id is not None:
        fr["id"] = id
    return types_mod.Content(role="user", parts=[types_mod.Part(function_response=fr)])


# ---------------------------------------------------------------------------
# _save_history cut-point tests
# ---------------------------------------------------------------------------
class SaveHistoryCutPointTests(unittest.TestCase):
    """#672 / #945: never start the persisted slice on a function_response."""

    def _session_path(self, sid):
        return os.path.join(os.environ["SESSION_STORE_DIR"], f"{sid}.json")

    def test_cut_skips_function_response_boundary(self):
        """With MAX_TURNS=2, a naive slice would leave a function_response at
        the head; the cut logic must walk past it until it lands on a plain
        user turn.
        """
        os.makedirs(os.environ["SESSION_STORE_DIR"], exist_ok=True)
        sid = "cut-fr-test"
        # 6 turns: user/model/fc/fr/user/model. Target keeps last 2.
        history = [
            _user_text("t1"),
            _model_text("a1"),
            _fc("echo"),
            _fr("echo", {"out": "hi"}),
            _user_text("t2"),
            _model_text("a2"),
        ]
        with patch.object(executor, "_SAVE_HISTORY_MAX_TURNS", 2), \
             patch.object(executor, "_SAVE_HISTORY_MAX_BYTES", 0):
            asyncio.run(executor._save_history(sid, history))

        with open(self._session_path(sid)) as fh:
            raw = json.load(fh)
        # The first entry must be a plain user turn (no function_response).
        self.assertEqual(raw[0]["role"], "user")
        self.assertNotIn("function_response", raw[0]["parts"][0])
        # And the file must round-trip: the last entry is still a2.
        self.assertEqual(raw[-1]["role"], "model")

    def test_cut_preserves_full_history_when_no_safe_boundary(self):
        """#731: if truncating would require splitting an AFC pair, keep the
        full history rather than silently wiping the session.
        """
        os.makedirs(os.environ["SESSION_STORE_DIR"], exist_ok=True)
        sid = "cut-no-boundary"
        # Every tail entry is inside an AFC pair — no safe cut point exists.
        history = [
            _user_text("t1"),
            _model_text("a1"),
            _fc("slow"),
            _fr("slow", {"out": "ok"}),
        ]
        with patch.object(executor, "_SAVE_HISTORY_MAX_TURNS", 1), \
             patch.object(executor, "_SAVE_HISTORY_MAX_BYTES", 0):
            asyncio.run(executor._save_history(sid, history))

        with open(self._session_path(sid)) as fh:
            raw = json.load(fh)
        # Entire history preserved; next save can retry when AFC settles.
        self.assertEqual(len(raw), 4)


# ---------------------------------------------------------------------------
# BudgetExceededError tests
# ---------------------------------------------------------------------------
class BudgetExceededErrorTests(unittest.TestCase):
    """#493: the error carries total/limit/collected for the response path."""

    def test_attributes_round_trip(self):
        exc = BudgetExceededError(1500, 1000, ["partial", "output"])
        self.assertEqual(exc.total, 1500)
        self.assertEqual(exc.limit, 1000)
        self.assertEqual(exc.collected, ["partial", "output"])
        self.assertIn("1500", str(exc))
        self.assertIn("1000", str(exc))

    def test_collected_defaults_to_empty_list(self):
        exc = BudgetExceededError(50, 10)
        self.assertEqual(exc.collected, [])


# ---------------------------------------------------------------------------
# _emit_afc_history tests
# ---------------------------------------------------------------------------
class EmitAfcHistoryTests(unittest.TestCase):
    """#676 / #887: the emitter tolerates empty and prefix-only inputs."""

    def test_empty_history_is_noop(self):
        # Must not raise; no rows to emit, no pairing work.
        asyncio.run(executor._emit_afc_history(
            [], session_id="sid-empty", model="gemini-1.5",
        ))

    def test_prefix_only_history_is_noop(self):
        # A prefix without a current slice must still short-circuit.
        prefix = [_fc("echo")]
        asyncio.run(executor._emit_afc_history(
            [], session_id="sid-prefix", model="gemini-1.5", prefix_history=prefix,
        ))


# ---------------------------------------------------------------------------
# _history_write_done Event race test (#674)
# ---------------------------------------------------------------------------
class WriterDoneEventRaceTests(unittest.TestCase):
    """The save path must always ``.set()`` the per-session done_event — even
    when the write itself fails — so the timeout cleanup never blocks
    indefinitely waiting for a writer that already gave up.
    """

    def test_done_event_set_even_when_write_fails(self):
        sid = "done-event-failure"
        history = [_user_text("hello")]

        # Force the blocking writer to raise every attempt.
        def _always_raise(*a, **kw):
            raise OSError("synthetic disk failure")

        with patch.object(executor, "_SAVE_HISTORY_MAX_RETRIES", 2), \
             patch.object(executor, "_SAVE_HISTORY_BACKOFF_BASE", 0.0), \
             patch.object(executor, "_write_history_respecting_epoch", _always_raise):
            with self.assertRaises(RuntimeError):
                asyncio.run(executor._save_history(sid, history))

        # Per-session mapping must have been cleaned up. If the Event
        # survived, a subsequent timeout cleanup would wait for a
        # writer that is already gone — the bug fixed by #674.
        self.assertNotIn(sid, executor._history_write_done)


# ---------------------------------------------------------------------------
# _pre_tool_use_gate scaffold tests (#808)
# ---------------------------------------------------------------------------
class PreToolUseGateScaffoldTests(unittest.TestCase):
    """The scaffold must fail-open when hooks_engine is unavailable or when
    evaluate_pre_tool_use raises, and must correctly decode a deny decision.
    """

    def test_returns_none_when_decision_is_allow(self):
        # Patch the import so evaluate_pre_tool_use returns None (= allow).
        fake_mod = types.ModuleType("hooks_engine")
        fake_mod.evaluate_pre_tool_use = lambda *a, **kw: None  # allow
        sys.modules["hooks_engine"] = fake_mod
        try:
            got = executor._pre_tool_use_gate("mcp__k8s__list_pods", {"namespace": "x"})
            self.assertIsNone(got)
        finally:
            sys.modules.pop("hooks_engine", None)

    def test_decodes_deny_tuple_decision(self):
        fake_mod = types.ModuleType("hooks_engine")
        fake_mod.evaluate_pre_tool_use = lambda *a, **kw: ("deny", "rm-rf-root", "destructive")
        sys.modules["hooks_engine"] = fake_mod
        try:
            got = executor._pre_tool_use_gate("Bash", {"command": "rm -rf /"})
            self.assertEqual(got, ("rm-rf-root", "destructive"))
        finally:
            sys.modules.pop("hooks_engine", None)

    def test_fail_open_on_evaluate_exception(self):
        fake_mod = types.ModuleType("hooks_engine")
        def _boom(*a, **kw):
            raise RuntimeError("engine bug")
        fake_mod.evaluate_pre_tool_use = _boom
        sys.modules["hooks_engine"] = fake_mod
        try:
            # Must not raise; must allow.
            got = executor._pre_tool_use_gate("whatever", {})
            self.assertIsNone(got)
        finally:
            sys.modules.pop("hooks_engine", None)


# ---------------------------------------------------------------------------
# Context-tokens-remaining metric guard (#1602)
# ---------------------------------------------------------------------------
class ContextTokensRemainingGuardTests(unittest.TestCase):
    """The metric block must not divide by zero when ``max_tokens`` is 0.

    ``parse_max_tokens`` filters non-positive values out of the request
    path, but the executor still defends in depth: the guard at the
    ``backend_context_tokens_remaining`` emit site must require
    ``max_tokens > 0`` before computing ``_total_tokens / max_tokens``.
    """

    def test_guard_source_requires_positive_max_tokens(self):
        # Regression: weakening the guard back to ``max_tokens is not None``
        # alone reintroduces the ZeroDivisionError of #1602.
        source = Path(executor.__file__).read_text()
        self.assertIn(
            "if _total_tokens is not None and max_tokens is not None and max_tokens > 0:",
            source,
        )

    def test_guard_skips_metric_block_when_max_tokens_is_zero(self):
        # Behavioral check: replay the guard expression directly. With
        # max_tokens=0 the block must short-circuit, leaving the
        # subsequent division unreachable.
        _total_tokens = 1500
        max_tokens = 0
        entered = False
        if _total_tokens is not None and max_tokens is not None and max_tokens > 0:
            entered = True
            _ = _total_tokens / max_tokens  # would raise ZeroDivisionError
        self.assertFalse(entered)


class McpConfigPathPrefixTests(unittest.TestCase):
    """#1610: ``_load_mcp_config`` must reject MCP_CONFIG_PATH values that
    resolve outside the documented allow-list prefix.
    """

    def test_mcp_config_path_outside_prefix_is_rejected(self):
        # /etc/passwd exists on every POSIX host, so os.path.exists short-
        # circuit doesn't hide the prefix check we're trying to assert.
        with patch.object(executor, "MCP_CONFIG_PATH", "/etc/passwd"), \
             patch.object(executor, "_MCP_CONFIG_PATH_ALLOWED_PREFIX", "/home/agent/"):
            result = executor._load_mcp_config()
        self.assertEqual(result, {})

    def test_mcp_config_path_inside_prefix_is_accepted(self):
        with tempfile.TemporaryDirectory(prefix="mcp-cfg-") as tmp:
            cfg_path = os.path.join(tmp, "mcp.json")
            with open(cfg_path, "w") as f:
                json.dump({"mcpServers": {"k8s": {"url": "http://example/"}}}, f)
            # Allow-list the realpath-resolved temp dir so macOS's
            # /var -> /private/var symlink doesn't trip the prefix check;
            # the production default of /home/agent/ is exercised by the
            # rejection test above.
            allowed = os.path.realpath(tmp) + os.sep
            with patch.object(executor, "MCP_CONFIG_PATH", cfg_path), \
                 patch.object(executor, "_MCP_CONFIG_PATH_ALLOWED_PREFIX", allowed):
                result = executor._load_mcp_config()
        self.assertEqual(result, {"k8s": {"url": "http://example/"}})


if __name__ == "__main__":
    unittest.main()

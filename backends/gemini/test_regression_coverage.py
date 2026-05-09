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
        with patch.object(executor, "_SAVE_HISTORY_MAX_TURNS", 2), patch.object(executor, "_SAVE_HISTORY_MAX_BYTES", 0):
            asyncio.run(executor._save_history(sid, history))

        with open(self._session_path(sid)) as fh:
            raw = json.load(fh)
        # The first entry must be a plain user turn (no function_response).
        self.assertEqual(raw[0]["role"], "user")
        self.assertNotIn("function_response", raw[0]["parts"][0])
        # And the file must round-trip: the last entry is still a2.
        self.assertEqual(raw[-1]["role"], "model")

    def test_byte_cap_force_splits_when_no_safe_boundary(self):
        """#1622: when the entire byte-cap trim window is one giant AFC pair
        (no safe boundary exists), the force-split fallback must fire and
        bring the persisted file to or under _SAVE_HISTORY_MAX_BYTES rather
        than oscillating with an oversized payload.
        """
        os.makedirs(os.environ["SESSION_STORE_DIR"], exist_ok=True)
        sid = "byte-cap-force-split"
        # Build a history dominated by mid-AFC pairs:
        #   user(t1) / model(a1) / model(fc) / user(fr) / model(fc) /
        #   user(fr) / model(fc) / user(fr) / ...
        # Every user-role entry past index 0 carries a function_response,
        # so the safe-boundary search at indices >= 1 returns nxt >= n.
        # The function_response payloads are inflated so the byte cap is
        # exceeded comfortably and the force-split fallback is forced
        # to produce a smaller-than-cap slice.
        big_blob = "x" * 4096  # 4 KiB per response part
        history = [_user_text("t1"), _model_text("a1")]
        for i in range(40):
            history.append(_fc(f"tool_{i}"))
            history.append(_fr(f"tool_{i}", {"out": big_blob}))

        # Cap small enough that the force-split must drop entries and
        # large enough that a single AFC pair (~4 KiB serialised) fits.
        cap = 16 * 1024
        with (
            patch.object(executor, "_SAVE_HISTORY_MAX_TURNS", 0),
            patch.object(executor, "_SAVE_HISTORY_MAX_BYTES", cap),
        ):
            asyncio.run(executor._save_history(sid, history))

        path = self._session_path(sid)
        size = os.path.getsize(path)
        # Force-split brought us at or under the byte cap.
        self.assertLessEqual(
            size,
            cap,
            f"force-split fallback failed to bring file under cap: {size} > {cap}",
        )
        # Sanity-check the file is still valid JSON.
        with open(path) as fh:
            raw = json.load(fh)
        self.assertGreater(len(raw), 0)

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
        with patch.object(executor, "_SAVE_HISTORY_MAX_TURNS", 1), patch.object(executor, "_SAVE_HISTORY_MAX_BYTES", 0):
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
        asyncio.run(
            executor._emit_afc_history(
                [],
                session_id="sid-empty",
                model="gemini-1.5",
            )
        )

    def test_prefix_only_history_is_noop(self):
        # A prefix without a current slice must still short-circuit.
        prefix = [_fc("echo")]
        asyncio.run(
            executor._emit_afc_history(
                [],
                session_id="sid-prefix",
                model="gemini-1.5",
                prefix_history=prefix,
            )
        )

    def test_prefix_paired_fr_skips_duration_histogram(self):
        """#1727: when a current-slice function_response pairs with a
        function_call seeded from prefix_history, the duration histogram
        must NOT be observed — the original fc start time was lost across
        the persistence boundary, so the computed delta would be a bogus
        ~0s sample.

        Tool-call counter still fires so audit / counts stay correct.
        """
        prefix = [_fc("echo", id="fc-1")]
        current = [_fr("echo", response={"ok": True}, id="fc-1")]

        observed_durations = []
        observed_calls = []

        class _DurFake:
            def labels(self, **_kw):
                return self

            def observe(self, v):
                observed_durations.append(v)

        class _CallsFake:
            def labels(self, **_kw):
                return self

            def inc(self):
                observed_calls.append(1)

        with (
            patch.object(executor, "backend_sdk_tool_duration_seconds", _DurFake()),
            patch.object(executor, "backend_sdk_tool_calls_total", _CallsFake()),
        ):
            asyncio.run(
                executor._emit_afc_history(
                    current,
                    session_id="sid-prefix-pair",
                    model="gemini-1.5",
                    prefix_history=prefix,
                )
            )

        self.assertEqual(
            observed_durations,
            [],
            "duration histogram must not observe samples for prefix-paired AFC fr (#1727)",
        )
        self.assertEqual(
            len(observed_calls),
            1,
            "tool-call counter should still fire so audit/counts stay correct",
        )


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

        with (
            patch.object(executor, "_SAVE_HISTORY_MAX_RETRIES", 2),
            patch.object(executor, "_SAVE_HISTORY_BACKOFF_BASE", 0.0),
            patch.object(executor, "_write_history_respecting_epoch", _always_raise),
        ):
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
        # Patch the import so evaluate_pre_tool_use returns the
        # shared-engine "allow" shape: (decision_str, matched_rule=None).
        fake_mod = types.ModuleType("hooks_engine")
        fake_mod.evaluate_pre_tool_use = lambda *a, **kw: ("allow", None)
        sys.modules["hooks_engine"] = fake_mod
        try:
            got = executor._pre_tool_use_gate("mcp__k8s__list_pods", {"namespace": "x"})
            self.assertIsNone(got)
        finally:
            sys.modules.pop("hooks_engine", None)

    def test_decodes_deny_tuple_decision(self):
        # #1724: shared-engine contract is (decision: str, matched_rule).
        # matched_rule exposes .name and .reason.
        class _Rule:
            name = "rm-rf-root"
            reason = "destructive"

        fake_mod = types.ModuleType("hooks_engine")
        fake_mod.evaluate_pre_tool_use = lambda *a, **kw: ("deny", _Rule())
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

    def test_state_active_rules_passed_positionally(self):
        # #1724: confirm rules list is forwarded as a positional arg
        # (the engine's signature has no `state=` kwarg).
        captured = {}

        def _capture(tool_name, tool_input, rules):
            captured["tool"] = tool_name
            captured["rules"] = rules
            return ("allow", None)

        fake_mod = types.ModuleType("hooks_engine")
        fake_mod.evaluate_pre_tool_use = _capture
        sys.modules["hooks_engine"] = fake_mod

        class _State:
            def active_rules(self):
                return ["r1", "r2"]

        try:
            got = executor._pre_tool_use_gate("Bash", {"command": "ls"}, state=_State())
            self.assertIsNone(got)
            self.assertEqual(captured["rules"], ["r1", "r2"])
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
        with (
            patch.object(executor, "MCP_CONFIG_PATH", "/etc/passwd"),
            patch.object(executor, "_MCP_CONFIG_PATH_ALLOWED_PREFIX", "/home/agent/"),
        ):
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
            with (
                patch.object(executor, "MCP_CONFIG_PATH", cfg_path),
                patch.object(executor, "_MCP_CONFIG_PATH_ALLOWED_PREFIX", allowed),
            ):
                result = executor._load_mcp_config()
        self.assertEqual(result, {"k8s": {"url": "http://example/"}})


# ---------------------------------------------------------------------------
# Eviction backpressure must wait on pending save (#1611)
# ---------------------------------------------------------------------------
class EvictionBackpressureWaitsOnSaveTests(unittest.TestCase):
    """Under simulated backpressure (deferred-eviction set is at the cap),
    ``_track_session`` must NOT synchronously os.remove a session file
    while a history save is in flight. The fix queues an async waiter
    that joins the per-session ``_history_write_done`` Event before
    removing — preserving the writer/unlinker invariant the deferred
    path already upholds (#1611).
    """

    def test_no_sync_remove_while_save_in_flight(self):
        async def scenario():
            evicted_id = "evict-bp-pending"
            evicted_path = os.path.join(executor.SESSION_STORE_DIR, f"{evicted_id}.json")
            os.makedirs(executor.SESSION_STORE_DIR, exist_ok=True)
            # Materialise the file so a buggy synchronous remove would
            # actually unlink something observable.
            with open(evicted_path, "w") as fh:
                fh.write("[]")

            # Simulate a writer in flight: register a not-yet-set Event
            # in the per-session done map.
            done_event = asyncio.Event()
            executor._history_write_done[evicted_id] = done_event

            # Build a sessions OrderedDict that's already at MAX_SESSIONS,
            # with our target as the LRU. Adding a brand-new id will evict it.
            from collections import OrderedDict

            sessions: OrderedDict[str, float] = OrderedDict()
            # Force the eviction by setting MAX_SESSIONS = 1 for the call.
            sessions[evicted_id] = 0.0
            session_locks: dict = {}

            # Saturate the deferred-eviction set so we land in the
            # backpressure branch. Use a real Task that just awaits a
            # never-set Event so it stays pending for the duration.
            never = asyncio.Event()

            async def _idle():
                try:
                    await never.wait()
                except asyncio.CancelledError:
                    raise

            saturating: list[asyncio.Task] = []
            try:
                # Snapshot + clear so we control the cap exactly.
                preexisting = set(executor._EVICT_REMOVE_TASKS)
                executor._EVICT_REMOVE_TASKS.clear()
                for _ in range(executor._EVICT_REMOVE_TASKS_MAX):
                    t = asyncio.get_running_loop().create_task(_idle())
                    saturating.append(t)
                    executor._EVICT_REMOVE_TASKS.add(t)

                removed_calls: list[str] = []
                real_remove = os.remove

                def _spy_remove(path, *a, **kw):
                    removed_calls.append(path)
                    return real_remove(path, *a, **kw)

                with patch.object(executor, "MAX_SESSIONS", 1), patch.object(executor.os, "remove", _spy_remove):
                    executor._track_session(
                        sessions,
                        "fresh-session-id",
                        session_locks,
                    )

                    # The backpressure branch must NOT have synchronously
                    # removed our file: a save is in flight.
                    self.assertNotIn(evicted_path, removed_calls)
                    self.assertTrue(
                        os.path.exists(evicted_path),
                        "file was unlinked while save was in flight (regression)",
                    )

                    # A backpressure waiter task should have been queued.
                    # It joined our cap-saturating set — total count grew
                    # by exactly one.
                    self.assertEqual(
                        len(executor._EVICT_REMOVE_TASKS),
                        executor._EVICT_REMOVE_TASKS_MAX + 1,
                    )

                    # Now release the writer; the waiter should run and
                    # remove the file.
                    done_event.set()
                    # Drain: cycle the loop a few times until the waiter
                    # completes (use wait_for on a copy so we don't hang
                    # on the saturating idlers).
                    new_tasks = [t for t in executor._EVICT_REMOVE_TASKS if t not in saturating]
                    self.assertEqual(len(new_tasks), 1)
                    await asyncio.wait_for(new_tasks[0], timeout=5.0)

                    self.assertFalse(
                        os.path.exists(evicted_path),
                        "waiter should have removed file after save completed",
                    )
            finally:
                # Cleanup: cancel saturating tasks and restore set state.
                for t in saturating:
                    t.cancel()
                # Let cancellations settle.
                for t in saturating:
                    try:
                        await t
                    except (asyncio.CancelledError, BaseException):
                        pass
                executor._EVICT_REMOVE_TASKS.clear()
                executor._EVICT_REMOVE_TASKS.update(preexisting)
                executor._history_write_done.pop(evicted_id, None)

        asyncio.run(scenario())

    def test_sync_remove_path_when_no_save_in_flight(self):
        """When the deferred-eviction set is full AND no save is pending
        for the evicted session, the backpressure branch is allowed to
        take the synchronous remove fast path — no waiter overhead.
        """

        async def scenario():
            evicted_id = "evict-bp-no-pending"
            evicted_path = os.path.join(executor.SESSION_STORE_DIR, f"{evicted_id}.json")
            os.makedirs(executor.SESSION_STORE_DIR, exist_ok=True)
            with open(evicted_path, "w") as fh:
                fh.write("[]")

            # Critically: do NOT register a done-event for this session.
            executor._history_write_done.pop(evicted_id, None)

            from collections import OrderedDict

            sessions: OrderedDict[str, float] = OrderedDict()
            sessions[evicted_id] = 0.0
            session_locks: dict = {}

            never = asyncio.Event()

            async def _idle():
                try:
                    await never.wait()
                except asyncio.CancelledError:
                    raise

            saturating: list[asyncio.Task] = []
            preexisting = set(executor._EVICT_REMOVE_TASKS)
            executor._EVICT_REMOVE_TASKS.clear()
            try:
                for _ in range(executor._EVICT_REMOVE_TASKS_MAX):
                    t = asyncio.get_running_loop().create_task(_idle())
                    saturating.append(t)
                    executor._EVICT_REMOVE_TASKS.add(t)

                with patch.object(executor, "MAX_SESSIONS", 1):
                    executor._track_session(
                        sessions,
                        "fresh-session-id-2",
                        session_locks,
                    )

                # The fast path ran: file is gone, no new task queued.
                self.assertFalse(os.path.exists(evicted_path))
                self.assertEqual(
                    len(executor._EVICT_REMOVE_TASKS),
                    executor._EVICT_REMOVE_TASKS_MAX,
                )
            finally:
                for t in saturating:
                    t.cancel()
                for t in saturating:
                    try:
                        await t
                    except (asyncio.CancelledError, BaseException):
                        pass
                executor._EVICT_REMOVE_TASKS.clear()
                executor._EVICT_REMOVE_TASKS.update(preexisting)

        asyncio.run(scenario())


if __name__ == "__main__":
    unittest.main()

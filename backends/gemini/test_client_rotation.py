"""Rotation-safety tests for ``_get_client`` (#1621).

Asserts the build-then-swap behaviour added in #1621:

- Rotation success: new client replaces old, old is queued for teardown.
- Rotation failure: old client is preserved (callers never see ``None``),
  the failure is logged at ERROR, and the rotation flag is cleared so the
  fast path re-engages.

The sys.path / stub setup mirrors ``test_regression_coverage.py`` so the
file is independently runnable under plain ``python3 test_*.py``.
"""
from __future__ import annotations

import logging
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch


_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent.parent  # backends/gemini -> backends -> repo root
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

os.environ.setdefault("GEMINI_API_KEY", "test-key")
os.environ.setdefault("AGENT_NAME", "gemini-test")
os.environ.setdefault("AGENT_OWNER", "test")
os.environ.setdefault("AGENT_ID", "gemini")
_log_tmp_dir = tempfile.mkdtemp(prefix="gemini-rotation-")
os.environ.setdefault("CONVERSATION_LOG", os.path.join(_log_tmp_dir, "conversation.jsonl"))
os.environ.setdefault("TRACE_LOG", os.path.join(_log_tmp_dir, "tool-activity.jsonl"))
os.environ.setdefault("SESSION_STORE_DIR", os.path.join(_log_tmp_dir, "sessions"))


def _install_stub(name: str, module: types.ModuleType) -> None:
    sys.modules.setdefault(name, module)


# google.genai stub (shared shape with sibling test files).
if "google.genai" not in sys.modules:
    _genai = types.ModuleType("google.genai")
    _types = types.ModuleType("google.genai.types")

    class _Part:
        def __init__(self, **kwargs):
            for k, v in kwargs.items():
                setattr(self, k, v)

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
            self.api_key = kw.get("api_key")

    _genai.types = _types
    _genai.Client = _Client
    _google = types.ModuleType("google")
    _google.genai = _genai
    _install_stub("google", _google)
    _install_stub("google.genai", _genai)
    _install_stub("google.genai.types", _types)

# a2a-sdk stubs (mirrors sibling tests).
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


class _FakeClient:
    """Minimal stand-in for ``genai.Client`` so the test owns identity checks."""

    def __init__(self, api_key: str | None = None):
        self.api_key = api_key


class _ResetSingletonMixin:
    """Reset the module-level rotation state between cases."""

    def setUp(self):  # noqa: D401
        executor._genai_client = None
        executor._rotation_pending = False
        executor._clients_pending_close.clear()

    def tearDown(self):
        executor._genai_client = None
        executor._rotation_pending = False
        executor._clients_pending_close.clear()


class GetClientRotationSuccessTests(_ResetSingletonMixin, unittest.TestCase):
    def test_rotation_success_swaps_in_new_client(self):
        # Seed a "previous" client and arm rotation.
        prev = _FakeClient(api_key="old")
        executor._genai_client = prev
        executor._rotation_pending = True

        new = _FakeClient(api_key="new")
        os.environ["GEMINI_API_KEY"] = "new"
        with patch.object(executor.genai, "Client", return_value=new):
            got = executor._get_client()

        self.assertIs(got, new, "new client must replace the old singleton")
        self.assertIs(executor._genai_client, new)
        self.assertFalse(executor._rotation_pending)
        self.assertIn(prev, executor._clients_pending_close,
                      "previous client must be queued for teardown")


class GetClientRotationFailureTests(_ResetSingletonMixin, unittest.TestCase):
    def test_rotation_failure_preserves_old_client_when_constructor_raises(self):
        prev = _FakeClient(api_key="old")
        executor._genai_client = prev
        executor._rotation_pending = True

        os.environ["GEMINI_API_KEY"] = "rotated-but-bad"
        boom = RuntimeError("constructor refused new key")
        with patch.object(executor.genai, "Client", side_effect=boom), \
                self.assertLogs(executor.logger, level="ERROR") as logs:
            got = executor._get_client()

        self.assertIs(got, prev,
                      "failed rotation must return the previously-cached client")
        self.assertIs(executor._genai_client, prev,
                      "singleton must NOT be left as None after a failed rotation")
        self.assertIsNotNone(executor._genai_client,
                             "no caller may ever observe a None client (#1621)")
        self.assertFalse(executor._rotation_pending,
                         "rotation flag must clear so the fast path re-engages")
        self.assertNotIn(prev, executor._clients_pending_close,
                         "old client must not be queued for teardown when rotation failed")
        joined = "\n".join(logs.output)
        self.assertIn("#1621", joined)

    def test_rotation_failure_when_env_unset_preserves_old_client(self):
        prev = _FakeClient(api_key="old")
        executor._genai_client = prev
        executor._rotation_pending = True

        # Simulate operator clearing both env vars during rotation.
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("GEMINI_API_KEY", None)
            os.environ.pop("GOOGLE_API_KEY", None)
            with self.assertLogs(executor.logger, level="ERROR") as logs:
                got = executor._get_client()

        self.assertIs(got, prev,
                      "missing key during rotation must fall back to the cached client")
        self.assertIs(executor._genai_client, prev)
        self.assertFalse(executor._rotation_pending)
        self.assertIn("#1621", "\n".join(logs.output))


class GetClientColdStartTests(_ResetSingletonMixin, unittest.TestCase):
    def test_cold_start_failure_with_no_prior_client_propagates(self):
        # No prior client: a build failure must propagate so the caller
        # sees the underlying error instead of a confusing None.
        executor._genai_client = None
        executor._rotation_pending = False

        os.environ["GEMINI_API_KEY"] = "k"
        boom = RuntimeError("cold start failed")
        with patch.object(executor.genai, "Client", side_effect=boom):
            with self.assertRaises(RuntimeError):
                executor._get_client()
        # And the singleton stays None for the next attempt.
        self.assertIsNone(executor._genai_client)


if __name__ == "__main__":  # pragma: no cover
    logging.basicConfig(level=logging.DEBUG)
    unittest.main()

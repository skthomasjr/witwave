"""Standalone smoke test for POST /internal/events/hook-decision (#641).

This repo does not yet carry a pytest harness for ``harness/`` — the
``tests/`` directory is markdown specs exercised against a running cluster.
This file is a self-contained unittest module that exercises the new handler
in-process, without spinning up uvicorn, so it can be run as:

    cd harness && HOOK_EVENTS_AUTH_TOKEN=test-token \
        python3 -m unittest test_hook_decision_event -v

Covers the acceptance cases called out in the gap spec:
    * valid POST → 202, ``publish_hook_decision`` called with the event
    * missing or wrong bearer token → 401
    * malformed JSON body → 400

When starlette is not installed locally (the harness runtime deps only live
in the container image), every test is skipped with a clear reason. The
container-side CI path installs the harness requirements and runs this
module as part of its startup check.
"""

from __future__ import annotations

import json
import os
import sys
import types
import unittest
from unittest import mock

try:  # pragma: no cover — environment gate
    import starlette  # noqa: F401
    _HAVE_STARLETTE = True
except Exception:
    _HAVE_STARLETTE = False


def _install_stub_modules() -> None:
    """Stub the heavy imports that ``main.py`` pulls in at module load."""

    class _AutoMock(types.ModuleType):
        def __getattr__(self, name: str):  # type: ignore[override]
            m = mock.MagicMock(name=f"{self.__name__}.{name}")
            setattr(self, name, m)
            return m

    heavy = [
        "a2a", "a2a.server", "a2a.server.apps", "a2a.server.agent_execution",
        "a2a.server.events", "a2a.server.request_handlers",
        "a2a.server.tasks", "a2a.types", "a2a.utils",
        "sqlite_task_store",
        "continuations", "jobs", "tasks", "triggers", "webhooks",
        "executor", "heartbeat",
        "conversations", "conversations_proxy", "metrics_proxy",
        "prometheus_client", "prometheus_client.exposition",
        "uvicorn",
    ]
    for name in heavy:
        if name not in sys.modules:
            sys.modules[name] = _AutoMock(name)


@unittest.skipUnless(_HAVE_STARLETTE, "starlette not installed locally; run inside the harness image")
class HookDecisionEventHandlerTests(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls) -> None:
        os.environ["HOOK_EVENTS_AUTH_TOKEN"] = "test-token"
        _here = os.path.dirname(os.path.abspath(__file__))
        if _here not in sys.path:
            sys.path.insert(0, _here)
        _install_stub_modules()

    async def _call(self, *, auth: str | None, body: bytes) -> tuple[int, dict, list]:
        from starlette.requests import Request
        import importlib

        headers = [
            (b"content-type", b"application/json"),
            (b"content-length", str(len(body)).encode()),
        ]
        if auth is not None:
            headers.append((b"authorization", auth.encode()))
        scope = {
            "type": "http", "method": "POST",
            "path": "/internal/events/hook-decision",
            "headers": headers, "query_string": b"",
        }

        _sent = False

        async def receive():
            nonlocal _sent
            if _sent:
                return {"type": "http.disconnect"}
            _sent = True
            return {"type": "http.request", "body": body, "more_body": False}

        request = Request(scope, receive)
        if "main" in sys.modules:
            main_mod = importlib.reload(sys.modules["main"])
        else:
            main_mod = importlib.import_module("main")

        captured: list = []
        with mock.patch.object(main_mod, "publish_hook_decision", side_effect=captured.append):
            response = await main_mod.hook_decision_event_handler(request)
        try:
            body_json = json.loads(bytes(response.body).decode("utf-8"))
        except Exception:
            body_json = {}
        return response.status_code, body_json, captured

    async def test_valid_post_publishes_event(self) -> None:
        payload = {
            "agent": "iris",
            "session_id": "sess-1",
            "tool": "Bash",
            "decision": "deny",
            "rule_name": "no-sudo",
            "reason": "sudo is denied by baseline policy",
            "source": "baseline",
            "traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
        }
        status, body, captured = await self._call(
            auth="Bearer test-token",
            body=json.dumps(payload).encode("utf-8"),
        )
        self.assertEqual(status, 202, body)
        self.assertEqual(len(captured), 1)
        event = captured[0]
        self.assertEqual(event.agent, "iris")
        self.assertEqual(event.decision, "deny")
        self.assertEqual(event.tool, "Bash")
        self.assertEqual(event.rule_name, "no-sudo")
        self.assertEqual(event.traceparent, payload["traceparent"])

    async def test_missing_auth_rejected(self) -> None:
        payload = {"agent": "iris", "session_id": "s", "tool": "Bash",
                   "decision": "allow", "rule_name": "", "reason": "", "source": ""}
        status, _body, captured = await self._call(
            auth=None,
            body=json.dumps(payload).encode("utf-8"),
        )
        self.assertEqual(status, 401)
        self.assertEqual(captured, [])

    async def test_wrong_auth_rejected(self) -> None:
        payload = {"agent": "iris", "session_id": "s", "tool": "Bash",
                   "decision": "allow", "rule_name": "", "reason": "", "source": ""}
        status, _body, captured = await self._call(
            auth="Bearer wrong-token",
            body=json.dumps(payload).encode("utf-8"),
        )
        self.assertEqual(status, 401)
        self.assertEqual(captured, [])

    async def test_malformed_json_returns_400(self) -> None:
        status, body, captured = await self._call(
            auth="Bearer test-token",
            body=b"{not valid json",
        )
        self.assertEqual(status, 400)
        self.assertIn("error", body)
        self.assertEqual(captured, [])


if __name__ == "__main__":
    unittest.main()

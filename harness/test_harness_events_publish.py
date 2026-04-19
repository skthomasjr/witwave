"""Tests for POST /internal/events/publish (#1110 phase 3).

Exercises the ``harness/events.parse_and_publish_envelope`` kernel that
backs the real Starlette handler in ``harness/main.py``. Keeping the
bearer-auth + body-size concerns in the handler and the parse/validate/
fan-out concerns in the kernel lets us test the contract without
pulling in the A2A SDK / uvicorn / prometheus_client at import time.

A small auth-layer unit test covers the bearer check by re-implementing
the same hmac.compare_digest flow the handler uses.
"""

from __future__ import annotations

import asyncio
import hmac as _hmac
import json
import os
import sys
import unittest

_HERE = os.path.dirname(os.path.abspath(__file__))
_SHARED = os.path.abspath(os.path.join(_HERE, "..", "shared"))
for p in (_HERE, _SHARED):
    if p not in sys.path:
        sys.path.insert(0, p)


class _MockCounter:
    def __init__(self) -> None:
        self.labelled: dict[tuple, int] = {}

    def inc(self) -> None:  # standalone — kernel calls .labels(...).inc()
        pass

    def labels(self, **kwargs):
        key = tuple(sorted(kwargs.items()))
        parent = self

        class _Child:
            def inc(self) -> None:
                parent.labelled[key] = parent.labelled.get(key, 0) + 1

        return _Child()


def _fresh_stream():
    import events  # type: ignore

    return events.reset_event_stream_for_tests()


# ---------------------------------------------------------------------------
# Auth layer — reimplementation of the handler's bearer check so we can
# exercise the 401 contract without importing main.py.
# ---------------------------------------------------------------------------


def _auth_ok(header: str, expected_token: str) -> bool:
    if not expected_token:
        return False
    return _hmac.compare_digest(f"Bearer {expected_token}", header)


class BearerAuthTests(unittest.TestCase):
    def test_rejects_when_token_empty(self) -> None:
        self.assertFalse(_auth_ok("Bearer whatever", ""))

    def test_rejects_on_mismatch(self) -> None:
        self.assertFalse(_auth_ok("Bearer wrong", "correct"))

    def test_accepts_on_exact_match(self) -> None:
        self.assertTrue(_auth_ok("Bearer correct", "correct"))


# ---------------------------------------------------------------------------
# parse_and_publish_envelope — validation + fan-out.
# ---------------------------------------------------------------------------


class PublishKernelTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.stream = _fresh_stream()
        self.rejected = _MockCounter()

    def _call(self, body: bytes):
        from events import parse_and_publish_envelope

        return parse_and_publish_envelope(
            body, stream=self.stream, rejected_counter=self.rejected
        )

    def test_400_on_malformed_json(self) -> None:
        status, err = self._call(b"{not json")
        self.assertEqual(status, 400)
        self.assertIn("malformed", err or "")
        self.assertEqual(
            self.rejected.labelled.get((("reason", "malformed_json"),), 0), 1
        )

    def test_400_on_non_object_body(self) -> None:
        status, _ = self._call(b"[1,2,3]")
        self.assertEqual(status, 400)

    def test_400_on_missing_type(self) -> None:
        status, _ = self._call(json.dumps({"payload": {}}).encode())
        self.assertEqual(status, 400)
        self.assertEqual(
            self.rejected.labelled.get((("reason", "validation"),), 0), 1
        )

    def test_400_on_payload_not_object(self) -> None:
        body = {"type": "tool.use", "payload": []}
        status, _ = self._call(json.dumps(body).encode())
        self.assertEqual(status, 400)

    def test_400_on_version_non_integer(self) -> None:
        body = {"type": "tool.use", "version": "one", "payload": {}}
        status, _ = self._call(json.dumps(body).encode())
        self.assertEqual(status, 400)

    def test_400_on_payload_schema_failure(self) -> None:
        # conversation.turn without required content_bytes.
        body = {
            "type": "conversation.turn",
            "version": 1,
            "agent_id": "iris",
            "payload": {
                "session_id_hash": "abcdef012345",
                "role": "user",
                # missing content_bytes
            },
        }
        status, _ = self._call(json.dumps(body).encode())
        self.assertEqual(status, 400)
        self.assertGreaterEqual(
            self.rejected.labelled.get((("reason", "validation"),), 0), 1
        )

    async def test_204_on_valid_tool_use_fans_out(self) -> None:
        sub = self.stream.subscribe()
        sub_gen = sub.__aiter__()

        body = {
            "type": "tool.use",
            "version": 1,
            "agent_id": "iris",
            "payload": {
                "session_id_hash": "abcdef012345",
                "tool": "Bash",
                "duration_ms": 12,
                "outcome": "ok",
                "result_size_bytes": 42,
            },
        }
        status, err = self._call(json.dumps(body).encode())
        self.assertEqual(status, 204, msg=f"err={err!r}")

        env = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
        self.assertEqual(env.type, "tool.use")
        self.assertEqual(env.agent_id, "iris")
        self.assertEqual(env.payload["tool"], "Bash")
        self.assertEqual(env.payload["outcome"], "ok")

    async def test_204_on_valid_conversation_turn(self) -> None:
        sub = self.stream.subscribe()
        sub_gen = sub.__aiter__()

        body = {
            "type": "conversation.turn",
            "version": 1,
            "agent_id": "iris",
            "payload": {
                "session_id_hash": "abcdef012345",
                "role": "assistant",
                "content_bytes": 512,
                "model": "claude-opus-4-6",
            },
        }
        status, err = self._call(json.dumps(body).encode())
        self.assertEqual(status, 204, msg=f"err={err!r}")

        env = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
        self.assertEqual(env.type, "conversation.turn")
        self.assertEqual(env.payload["role"], "assistant")

    async def test_204_on_valid_trace_span(self) -> None:
        sub = self.stream.subscribe()
        sub_gen = sub.__aiter__()

        body = {
            "type": "trace.span",
            "version": 1,
            "agent_id": "iris",
            "payload": {
                "span_name": "llm.request",
                "duration_ms": 120,
                "status": "ok",
                "service": "claude-backend",
            },
        }
        status, err = self._call(json.dumps(body).encode())
        self.assertEqual(status, 204, msg=f"err={err!r}")

        env = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
        self.assertEqual(env.type, "trace.span")
        self.assertEqual(env.payload["span_name"], "llm.request")

    def test_field_cap_truncates_long_strings(self) -> None:
        from events import MAX_EVENT_PUBLISH_FIELD_BYTES

        # conversation.turn with a `model` string far over the cap.
        oversized = "m" * (MAX_EVENT_PUBLISH_FIELD_BYTES + 64)
        body = {
            "type": "conversation.turn",
            "version": 1,
            "agent_id": "iris",
            "payload": {
                "session_id_hash": "abcdef012345",
                "role": "user",
                "content_bytes": 3,
                "model": oversized,
            },
        }
        status, _ = self._call(json.dumps(body).encode())
        self.assertEqual(status, 204)
        # Inspect the published ring for the truncation marker.
        ring = list(self.stream._ring)  # type: ignore[attr-defined]
        self.assertTrue(ring)
        capped = ring[-1].payload["model"]
        self.assertTrue(capped.endswith("...[truncated]"))
        self.assertLessEqual(
            len(capped), MAX_EVENT_PUBLISH_FIELD_BYTES + len("...[truncated]")
        )


if __name__ == "__main__":  # pragma: no cover
    unittest.main()

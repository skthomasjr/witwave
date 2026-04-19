"""Unit tests for the in-process SSE event stream (#1110).

Covers:
* Publish + single-subscriber roundtrip (ordered delivery).
* Bounded ring: replay_from("0") returns only the last ring_max events.
* Last-Event-ID resume returns the right tail of the ring.
* Slow-subscriber eviction hands out a terminal stream.overrun envelope
  and does not affect other subscribers.
* Schema validation drops malformed payloads without fanning out and
  bumps the validation counter.
* Multi-subscriber fanout: all live subscribers see every event.

The tests run without starlette / uvicorn / the A2A SDK — they import
the emitter library and its schema validator directly. The suite also
avoids prometheus_client by wiring lightweight mock counter objects.
"""

from __future__ import annotations

import asyncio
import os
import sys
import unittest
from unittest import mock


_HERE = os.path.dirname(os.path.abspath(__file__))
_SHARED = os.path.abspath(os.path.join(_HERE, "..", "shared"))
for p in (_HERE, _SHARED):
    if p not in sys.path:
        sys.path.insert(0, p)


class _MockCounter:
    def __init__(self) -> None:
        self.value = 0
        self.labelled: dict[tuple, int] = {}

    def inc(self) -> None:
        self.value += 1

    def labels(self, **kwargs):
        key = tuple(sorted(kwargs.items()))

        parent = self

        class _Child:
            def inc(self) -> None:  # noqa: D401
                parent.labelled[key] = parent.labelled.get(key, 0) + 1

        return _Child()


class _MockGauge:
    def __init__(self) -> None:
        self.value = 0

    def set(self, v: float) -> None:
        self.value = v


def _fresh_stream(queue_max: int = 1000, ring_max: int = 1000):
    # Import lazily so sys.path is set up.
    from events import EventStream
    return EventStream(queue_max=queue_max, ring_max=ring_max)


class EventStreamTests(unittest.IsolatedAsyncioTestCase):
    async def test_publish_subscribe_roundtrip(self) -> None:
        stream = _fresh_stream()
        sub = stream.subscribe()
        sub_gen = sub.__aiter__()

        for i in range(3):
            stream.publish(
                "job.fired",
                {
                    "name": f"j{i}",
                    "schedule": "* * * * *",
                    "duration_ms": i,
                    "outcome": "success",
                },
                agent_id="iris",
            )

        for i in range(3):
            envelope = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
            self.assertEqual(envelope.type, "job.fired")
            self.assertEqual(envelope.payload["name"], f"j{i}")
            self.assertEqual(envelope.agent_id, "iris")
            self.assertEqual(envelope.id, str(i + 1))

    async def test_bounded_ring(self) -> None:
        stream = _fresh_stream(ring_max=3)
        for i in range(5):
            stream.publish(
                "job.fired",
                {
                    "name": f"j{i}",
                    "schedule": "*/5 * * * *",
                    "duration_ms": 0,
                    "outcome": "success",
                },
                agent_id="iris",
            )

        out = stream.replay_from("0")
        self.assertEqual(len(out), 3)
        self.assertEqual([e.payload["name"] for e in out], ["j2", "j3", "j4"])

    async def test_last_event_id_resume(self) -> None:
        stream = _fresh_stream(ring_max=10)
        for i in range(5):
            stream.publish(
                "heartbeat.fired",
                {"duration_ms": i, "outcome": "success"},
                agent_id="iris",
            )

        # Subscriber says "last saw id=2"; ring should replay 3,4,5.
        replayed = stream.replay_from("2")
        self.assertEqual([e.id for e in replayed], ["3", "4", "5"])

        # Live publish after replay should land on a subscriber created now.
        sub = stream.subscribe()
        sub_gen = sub.__aiter__()
        stream.publish(
            "heartbeat.fired",
            {"duration_ms": 10, "outcome": "success"},
            agent_id="iris",
        )
        envelope = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
        self.assertEqual(envelope.id, "6")

    async def test_slow_subscriber_eviction(self) -> None:
        stream = _fresh_stream(queue_max=2)

        fast = stream.subscribe()
        slow = stream.subscribe()
        fast_gen = fast.__aiter__()
        slow_gen = slow.__aiter__()  # do NOT drain — force queue full

        overrun_counter = _MockCounter()
        stream.attach_metrics(overruns_total=overrun_counter)

        # Drain fast between every publish; slow never drains and its
        # queue (cap=2) fills on the third publish so it is evicted.
        fast_events = []
        for i in range(5):
            stream.publish(
                "heartbeat.fired",
                {"duration_ms": i, "outcome": "success"},
                agent_id="iris",
            )
            fast_events.append(await asyncio.wait_for(fast_gen.__anext__(), timeout=1.0))
        # stream.overrun envelopes injected mid-stream bump ids for the
        # evicted subscriber, so fast may see a different id set — all we
        # need is that it received 5 published envelopes.
        self.assertEqual(len(fast_events), 5)
        self.assertTrue(all(e.type == "heartbeat.fired" for e in fast_events))

        # Slow subscriber's stream terminates after seeing stream.overrun.
        async def _drain_slow() -> list:
            out: list = []
            try:
                async for ev in slow_gen:
                    out.append(ev)
                    if ev.type == "stream.overrun":
                        break
            except StopAsyncIteration:
                pass
            return out

        drained = await asyncio.wait_for(_drain_slow(), timeout=2.0)
        self.assertTrue(
            any(ev.type == "stream.overrun" for ev in drained),
            f"expected a stream.overrun envelope, got {[e.type for e in drained]}",
        )
        self.assertGreaterEqual(overrun_counter.value, 1)

    async def test_schema_validation_drops_malformed(self) -> None:
        stream = _fresh_stream()
        err_counter = _MockCounter()
        drop_counter = _MockCounter()
        stream.attach_metrics(
            validation_errors_total=err_counter,
            dropped_total=drop_counter,
        )

        sub = stream.subscribe()
        sub_gen = sub.__aiter__()

        # Missing required 'outcome' field → validation must drop.
        out = stream.publish(
            "job.fired",
            {"name": "oops", "schedule": "* * * * *", "duration_ms": 1},
            agent_id="iris",
        )
        self.assertIsNone(out)
        self.assertEqual(
            err_counter.labelled.get((("type", "job.fired"),), 0), 1
        )
        self.assertEqual(
            drop_counter.labelled.get((("reason", "validation"),), 0), 1
        )

        # A well-formed publish right after must still deliver and must be
        # the first thing the subscriber sees.
        stream.publish(
            "job.fired",
            {
                "name": "ok",
                "schedule": "* * * * *",
                "duration_ms": 5,
                "outcome": "success",
            },
            agent_id="iris",
        )
        envelope = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
        self.assertEqual(envelope.payload["name"], "ok")

    async def test_multi_subscriber_fanout(self) -> None:
        stream = _fresh_stream()

        subs = [stream.subscribe() for _ in range(3)]
        iters = [s.__aiter__() for s in subs]

        stream.publish(
            "heartbeat.fired",
            {"duration_ms": 0, "outcome": "success"},
            agent_id="iris",
        )

        for it in iters:
            envelope = await asyncio.wait_for(it.__anext__(), timeout=1.0)
            self.assertEqual(envelope.type, "heartbeat.fired")
            self.assertEqual(envelope.id, "1")


class EventSchemaTests(unittest.TestCase):
    """Direct coverage of the shared validator."""

    def _env(self, type_: str, payload: dict) -> dict:
        return {
            "type": type_,
            "version": 1,
            "id": "1",
            "ts": "2026-04-18T00:00:00.000Z",
            "agent_id": "iris",
            "payload": payload,
        }

    def test_job_fired_missing_field(self) -> None:
        from event_schema import validate_envelope
        err = validate_envelope(self._env("job.fired", {"name": "j"}))
        self.assertIsNotNone(err)

    def test_webhook_delivered_host_only(self) -> None:
        from event_schema import validate_envelope
        ok = validate_envelope(self._env(
            "webhook.delivered",
            {
                "name": "sub1",
                "url_host": "example.com",
                "status_code": 200,
                "duration_ms": 17,
            },
        ))
        self.assertIsNone(ok)

    def test_hook_decision_backend_enum(self) -> None:
        from event_schema import validate_envelope
        err = validate_envelope(self._env(
            "hook.decision",
            {
                "backend": "anthropic",
                "session_id_hash": "abc123",
                "tool": "Bash",
                "decision": "allow",
            },
        ))
        self.assertIsNotNone(err)

    def test_unknown_type_rejected(self) -> None:
        from event_schema import validate_envelope
        err = validate_envelope(self._env("made.up", {}))
        self.assertIsNotNone(err)

    def test_continuation_fired_upstream_kind(self) -> None:
        from event_schema import validate_envelope
        ok = validate_envelope(self._env(
            "continuation.fired",
            {
                "name": "followup",
                "upstream_kind": "job",
                "upstream_name": "daily-report",
                "duration_ms": 3,
                "outcome": "success",
            },
        ))
        self.assertIsNone(ok)


if __name__ == "__main__":
    unittest.main()

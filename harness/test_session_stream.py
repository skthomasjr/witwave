"""Tests for the per-session SSE broadcaster (#1110 phase 4).

Covers:
* publish/subscribe roundtrip with a single subscriber
* Last-Event-ID replay from the bounded ring
* slow-subscriber eviction + terminal stream.overrun envelope
* grace-period cleanup via sweep_idle_streams()
* per-session isolation (two sessions, disjoint subscribers)
* invalid payloads are dropped without raising
"""

from __future__ import annotations

import asyncio
import os
import sys
import unittest

_HERE = os.path.dirname(os.path.abspath(__file__))
_SHARED = os.path.abspath(os.path.join(_HERE, "..", "shared"))
for p in (_HERE, _SHARED):
    if p not in sys.path:
        sys.path.insert(0, p)


def _fresh():
    # Import lazily so sys.path is set up.
    import session_stream as ss

    ss.reset_session_streams_for_tests()
    return ss


class SessionStreamRoundtripTests(unittest.IsolatedAsyncioTestCase):
    async def test_publish_subscribe_roundtrip(self) -> None:
        ss = _fresh()
        stream = ss.get_session_stream("sess-a", agent_id="iris")
        sub_gen = stream.subscribe().__aiter__()

        for i in range(3):
            stream.publish_chunk(
                role="assistant",
                seq=i,
                content=f"chunk-{i}",
                final=(i == 2),
            )

        for i in range(3):
            env = await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
            self.assertEqual(env.type, "conversation.chunk")
            self.assertEqual(env.payload["seq"], i)
            self.assertEqual(env.payload["content"], f"chunk-{i}")
            self.assertEqual(env.agent_id, "iris")

    async def test_invalid_payload_dropped(self) -> None:
        ss = _fresh()
        stream = ss.get_session_stream("sess-x")
        # Missing required fields — validator should drop, no raise.
        env = stream.publish("conversation.chunk", {"role": "assistant"})
        self.assertIsNone(env)

    async def test_per_session_isolation(self) -> None:
        ss = _fresh()
        sa = ss.get_session_stream("sess-a")
        sb = ss.get_session_stream("sess-b")
        self.assertIsNot(sa, sb)

        sub_a = sa.subscribe().__aiter__()
        sub_b = sb.subscribe().__aiter__()

        sa.publish_chunk(role="assistant", seq=0, content="for-a", final=False)
        sb.publish_chunk(role="assistant", seq=0, content="for-b", final=True)

        got_a = await asyncio.wait_for(sub_a.__anext__(), timeout=1.0)
        got_b = await asyncio.wait_for(sub_b.__anext__(), timeout=1.0)
        self.assertEqual(got_a.payload["content"], "for-a")
        self.assertEqual(got_b.payload["content"], "for-b")

        # Session A's subscriber must not observe session B's publish.
        with self.assertRaises(asyncio.TimeoutError):
            await asyncio.wait_for(sub_a.__anext__(), timeout=0.1)

    async def test_ring_replay_via_last_event_id(self) -> None:
        ss = _fresh()
        # Small ring so we can force wrap-around.
        stream = ss.SessionStream("sess-r", ring_max=3)

        for i in range(5):
            stream.publish_chunk(role="assistant", seq=i, content=f"c{i}", final=False)

        # Ring holds only the last 3 events.
        self.assertEqual(stream.ring_size, 3)
        # replay_from("2") returns those with id > 2 still in ring.
        replayed = stream.replay_from("2")
        self.assertEqual([e.id for e in replayed], ["3", "4", "5"])

        # Empty / unknown last_id returns full ring tail.
        all_ring = stream.replay_from(None)
        self.assertEqual(len(all_ring), 3)

    async def test_slow_subscriber_eviction(self) -> None:
        ss = _fresh()
        stream = ss.SessionStream("sess-slow", queue_max=2)
        slow = stream.subscribe().__aiter__()

        # Fill the queue beyond the cap without draining.
        for i in range(5):
            stream.publish_chunk(role="assistant", seq=i, content=f"c{i}", final=False)

        # Drain what's available; the stream must end with an overrun
        # envelope and then close.
        got: list = []

        async def _drain():
            try:
                async for ev in slow:
                    got.append(ev)
                    if ev.type == "stream.overrun":
                        break
            except StopAsyncIteration:
                pass

        await asyncio.wait_for(_drain(), timeout=2.0)
        self.assertTrue(
            any(e.type == "stream.overrun" for e in got),
            f"expected overrun, got {[e.type for e in got]}",
        )
        # Subscriber was removed on eviction.
        self.assertEqual(stream.subscriber_count, 0)

    async def test_grace_period_cleanup(self) -> None:
        ss = _fresh()
        stream = ss.get_session_stream("sess-idle")
        self.assertEqual(ss.registry_size(), 1)

        # Attach + detach a subscriber so idle_since is set.
        sub_gen = stream.subscribe().__aiter__()
        # Publish + consume one event so the generator body enters its
        # yield-loop; then aclose() drives the finally block that calls
        # _remove_subscriber.
        stream.publish_chunk(role="user", seq=0, content="hi", final=True)
        await asyncio.wait_for(sub_gen.__anext__(), timeout=1.0)
        await sub_gen.aclose()
        # idle_since should now be set; force eviction with grace=0.
        dropped = ss.sweep_idle_streams(grace_sec=0.0)
        self.assertGreaterEqual(dropped, 1)
        self.assertEqual(ss.registry_size(), 0)

    async def test_grace_period_newly_created_idle_bootstraps(self) -> None:
        ss = _fresh()
        ss.get_session_stream("sess-cold")
        # #1147: the idle clock is set at construction, so a broadcaster
        # with no subscribers and no publishes is already sweep-eligible
        # under grace=0.  Previous behaviour (lazy bootstrap on first
        # inspection) is explicitly replaced because it cost one sweeper
        # tick of eviction latency on idle sessions.
        self.assertEqual(ss.sweep_idle_streams(grace_sec=0.0), 1)
        self.assertEqual(ss.registry_size(), 0)

    async def test_grace_period_respects_active_subscribers(self) -> None:
        ss = _fresh()
        stream = ss.get_session_stream("sess-active")
        _sub_gen = stream.subscribe().__aiter__()  # noqa: F841

        # With an active subscriber, even grace=0 must not evict.
        dropped = ss.sweep_idle_streams(grace_sec=0.0)
        self.assertEqual(dropped, 0)
        self.assertEqual(ss.registry_size(), 1)

    async def test_get_session_stream_create_false(self) -> None:
        ss = _fresh()
        self.assertIsNone(ss.get_session_stream("nope", create=False))
        # Implicit creation still works via default.
        stream = ss.get_session_stream("nope")
        self.assertIsNotNone(stream)
        # And now create=False returns the existing broadcaster.
        self.assertIs(stream, ss.get_session_stream("nope", create=False))

    async def test_session_id_hash_shape(self) -> None:
        ss = _fresh()
        h = ss.session_id_hash("any-raw-id")
        self.assertEqual(len(h), 12)
        # Deterministic.
        self.assertEqual(h, ss.session_id_hash("any-raw-id"))
        # And matches what the broadcaster uses in payloads.
        stream = ss.get_session_stream("any-raw-id")
        env = stream.publish_chunk(role="user", seq=0, content="hi", final=True)
        self.assertIsNotNone(env)
        self.assertEqual(env.payload["session_id_hash"], h)


class SessionStreamCapClampTests(unittest.TestCase):
    """#1645: SESSION_STREAM_MAX_PER_CALLER must clamp at zero.

    A negative value would otherwise make the gate at
    ``if _per_caller_max > 0`` skip entirely, letting one authed caller
    open unlimited concurrent SSE streams (FD exhaustion vector).
    """

    def _captured_per_caller_max(self) -> int:
        import session_stream as ss

        handler = ss.make_session_stream_handler(auth_token="t")
        # The factory captures _per_caller_max in the inner handler's
        # closure; pull it back out by name to assert clamp behaviour
        # without standing up a full Starlette request.
        names = handler.__code__.co_freevars
        cells = handler.__closure__
        assert cells is not None, "handler must capture closure cells"
        idx = names.index("_per_caller_max")
        return cells[idx].cell_contents

    def test_stream_cap_negative_env_clamps_to_zero(self) -> None:
        prev = os.environ.get("SESSION_STREAM_MAX_PER_CALLER")
        os.environ["SESSION_STREAM_MAX_PER_CALLER"] = "-1"
        try:
            self.assertEqual(self._captured_per_caller_max(), 0)
        finally:
            if prev is None:
                os.environ.pop("SESSION_STREAM_MAX_PER_CALLER", None)
            else:
                os.environ["SESSION_STREAM_MAX_PER_CALLER"] = prev

    def test_stream_cap_default_value_unaffected(self) -> None:
        prev = os.environ.pop("SESSION_STREAM_MAX_PER_CALLER", None)
        try:
            self.assertEqual(self._captured_per_caller_max(), 8)
        finally:
            if prev is not None:
                os.environ["SESSION_STREAM_MAX_PER_CALLER"] = prev


class RegistryCapTests(unittest.IsolatedAsyncioTestCase):
    """#1735: belt-and-braces LRU eviction at insertion time.

    Without this cap the periodic sweeper is the only thing keeping
    ``_registry`` bounded — if the sweeper task ever wedges, the
    registry grows unbounded until OOMKill. The LRU cap fires
    synchronously at insertion so growth is hard-bounded regardless
    of sweeper health.
    """

    async def test_lru_eviction_when_cap_reached(self) -> None:
        ss = _fresh()
        # Pin a tiny cap on the module constant for this test only.
        prev_cap = ss.CONVERSATION_STREAM_REGISTRY_MAX
        ss.CONVERSATION_STREAM_REGISTRY_MAX = 3
        try:
            for sid in ("a", "b", "c"):
                ss.get_session_stream(sid)
            self.assertEqual(ss.registry_size(), 3)
            # Inserting a 4th evicts the oldest ("a").
            ss.get_session_stream("d")
            self.assertEqual(ss.registry_size(), 3)
            self.assertIsNone(ss.get_session_stream("a", create=False))
            for sid in ("b", "c", "d"):
                self.assertIsNotNone(ss.get_session_stream(sid, create=False))
        finally:
            ss.CONVERSATION_STREAM_REGISTRY_MAX = prev_cap


class IdleSweeperTaskTests(unittest.IsolatedAsyncioTestCase):
    """#1735: ``run_idle_sweeper`` cancels cleanly and calls
    ``sweep_idle_streams`` on each iteration."""

    async def test_sweeper_runs_and_cancels(self) -> None:
        ss = _fresh()
        ss.get_session_stream("doomed")
        self.assertEqual(ss.registry_size(), 1)
        # Tight interval + zero grace so the first tick reaps the
        # idle broadcaster.
        task = asyncio.create_task(ss.run_idle_sweeper(interval_sec=0.01, grace_sec=0.0))
        try:
            # Give the sweeper a couple of ticks.
            for _ in range(50):
                await asyncio.sleep(0.01)
                if ss.registry_size() == 0:
                    break
            self.assertEqual(ss.registry_size(), 0)
        finally:
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task


if __name__ == "__main__":
    unittest.main()

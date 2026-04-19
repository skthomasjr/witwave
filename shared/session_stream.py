"""Per-session SSE broadcaster for backend drill-down streams (#1110 phase 4).

Each backend (`claude`, `codex`, `gemini`) exposes
``GET /api/sessions/<session_id>/stream``; this module provides the
fan-out plumbing that feeds that endpoint.  The wire contract is the
same SSE envelope used by ``GET /events/stream`` on the harness — the
only differences are:

* **Scope.**  Events are scoped to one ``session_id`` and one backend
  process.  No cross-pod sharing; no tee to harness.  Chunk-level
  traffic would flood the multiplexed harness timeline; keeping it
  per-backend matches the drill-down use case.
* **Lifecycle.**  Broadcasters are created lazily when the first chunk
  is published for a session (or when the first subscriber arrives)
  and linger ``CONVERSATION_STREAM_GRACE_SEC`` seconds after the last
  subscriber disconnects so brief reconnects can resume from the ring.

Public surface:

* :class:`SessionStream` — per-session broadcaster.
* :func:`get_session_stream` — registry lookup + lazy creation.
* :func:`drop_session_stream` — test/cleanup hook.
* :func:`session_id_hash` — 12-char SHA-256 prefix helper; callers use
  this in every event payload so the raw session_id never leaves the
  process.
"""

from __future__ import annotations

import asyncio
import hashlib
import logging
import os
import time
from collections import deque
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, AsyncIterator

try:  # pragma: no cover — validator import guard
    from event_schema import validate_envelope  # type: ignore[import-not-found]
except Exception:  # pragma: no cover
    from shared.event_schema import validate_envelope  # type: ignore[no-redef]

logger = logging.getLogger(__name__)


CONVERSATION_STREAM_QUEUE_MAX = int(
    os.environ.get("CONVERSATION_STREAM_QUEUE_MAX", "500")
)
CONVERSATION_STREAM_RING_MAX = int(
    os.environ.get("CONVERSATION_STREAM_RING_MAX", "200")
)
CONVERSATION_STREAM_KEEPALIVE_SEC = float(
    os.environ.get("CONVERSATION_STREAM_KEEPALIVE_SEC", "15")
)
CONVERSATION_STREAM_GRACE_SEC = float(
    os.environ.get("CONVERSATION_STREAM_GRACE_SEC", "60")
)


@dataclass
class SessionStreamEnvelope:
    """One published envelope — serialised into a single SSE frame."""

    type: str
    version: int
    id: str
    ts: str
    agent_id: str | None
    payload: dict

    def to_dict(self) -> dict:
        return {
            "type": self.type,
            "version": self.version,
            "id": self.id,
            "ts": self.ts,
            "agent_id": self.agent_id,
            "payload": dict(self.payload),
        }


_OVERRUN_SENTINEL: Any = object()


class _Subscriber:
    __slots__ = ("queue", "closed")

    def __init__(self, queue: asyncio.Queue) -> None:
        self.queue = queue
        self.closed: bool = False

    def __hash__(self) -> int:
        return id(self)

    def __eq__(self, other: object) -> bool:
        return self is other


def session_id_hash(session_id: str) -> str:
    """Return the 12-char sha256 prefix used in event payloads."""
    if not isinstance(session_id, str):
        session_id = str(session_id or "")
    digest = hashlib.sha256(session_id.encode("utf-8")).hexdigest()
    return digest[:12]


def _now_iso_ms() -> str:
    now = datetime.now(timezone.utc)
    return now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"


class SessionStream:
    """Per-session broadcaster.

    Not thread-safe; expected to be driven from a single asyncio loop.
    Methods are safe to call concurrently from that loop.
    """

    def __init__(
        self,
        session_id: str,
        *,
        queue_max: int = CONVERSATION_STREAM_QUEUE_MAX,
        ring_max: int = CONVERSATION_STREAM_RING_MAX,
        agent_id: str | None = None,
    ) -> None:
        self.session_id = session_id
        self.session_hash = session_id_hash(session_id)
        self._queue_max = max(1, queue_max)
        self._ring_max = max(1, ring_max)
        self._ring: deque[SessionStreamEnvelope] = deque(maxlen=self._ring_max)
        self._subscribers: set[_Subscriber] = set()
        self._next_id: int = 0
        self._agent_id = agent_id
        # Per-session assistant chunk sequence number.  Resets each
        # turn via :meth:`reset_assistant_seq` — callers invoke it at
        # the start of each assistant stream so observers see monotonic
        # seq within a turn.
        self._assistant_seq: int = 0
        # Last-unsubscribed wall-time; used by the registry to decide
        # when the grace period has elapsed.
        self._idle_since: float | None = None

    # ---------- subscription ----------

    def subscribe(self) -> AsyncIterator[SessionStreamEnvelope]:
        sub = _Subscriber(asyncio.Queue(maxsize=self._queue_max))
        self._subscribers.add(sub)
        self._idle_since = None
        return self._iterate(sub)

    async def _iterate(
        self, sub: _Subscriber
    ) -> AsyncIterator[SessionStreamEnvelope]:
        try:
            while True:
                item = await sub.queue.get()
                if item is _OVERRUN_SENTINEL:
                    return
                yield item
                if sub.closed and sub.queue.empty():
                    return
        finally:
            self._remove_subscriber(sub)

    def _remove_subscriber(self, sub: _Subscriber) -> None:
        if sub in self._subscribers:
            self._subscribers.discard(sub)
        if not self._subscribers:
            self._idle_since = time.monotonic()

    # ---------- publish path ----------

    def publish_chunk(
        self,
        *,
        role: str,
        seq: int,
        content: str,
        final: bool,
    ) -> SessionStreamEnvelope | None:
        """Emit a ``conversation.chunk`` envelope for this session."""
        return self.publish(
            "conversation.chunk",
            {
                "session_id_hash": self.session_hash,
                "role": role,
                "seq": int(seq),
                "content": str(content),
                "final": bool(final),
            },
        )

    def next_assistant_seq(self) -> int:
        """Allocate + return the next assistant seq for this turn."""
        n = self._assistant_seq
        self._assistant_seq += 1
        return n

    def reset_assistant_seq(self) -> None:
        """Reset the assistant chunk counter.  Call at the start of each turn."""
        self._assistant_seq = 0

    def publish(
        self,
        type_: str,
        payload: dict,
        *,
        version: int = 1,
    ) -> SessionStreamEnvelope | None:
        """Validate + fan out a single envelope.  Never raises."""
        self._next_id += 1
        envelope = SessionStreamEnvelope(
            type=type_,
            version=version,
            id=str(self._next_id),
            ts=_now_iso_ms(),
            agent_id=self._agent_id,
            payload=dict(payload),
        )
        err = validate_envelope(envelope.to_dict())
        if err is not None:
            logger.warning(
                "session_stream: dropping invalid %r envelope for session %s: %s",
                type_,
                self.session_hash,
                err,
            )
            self._next_id -= 1
            return None

        self._ring.append(envelope)
        self._fanout(envelope)
        return envelope

    def _fanout(self, envelope: SessionStreamEnvelope) -> None:
        for sub in list(self._subscribers):
            if sub.closed:
                continue
            try:
                sub.queue.put_nowait(envelope)
            except asyncio.QueueFull:
                self._evict_slow(sub)

    def _evict_slow(self, sub: _Subscriber) -> None:
        sub.closed = True
        self._next_id += 1
        overrun = SessionStreamEnvelope(
            type="stream.overrun",
            version=1,
            id=str(self._next_id),
            ts=_now_iso_ms(),
            agent_id=self._agent_id,
            payload={
                "queue_depth": sub.queue.qsize(),
                "queue_max": self._queue_max,
                "reason": "subscriber queue full; evicted",
            },
        )
        try:
            _ = sub.queue.get_nowait()
        except asyncio.QueueEmpty:
            pass
        try:
            sub.queue.put_nowait(overrun)
        except asyncio.QueueFull:
            pass
        try:
            sub.queue.put_nowait(_OVERRUN_SENTINEL)
        except asyncio.QueueFull:
            pass
        logger.warning(
            "session_stream: subscriber for session %s evicted — queue full (cap=%d)",
            self.session_hash,
            self._queue_max,
        )
        if sub in self._subscribers:
            self._subscribers.discard(sub)
            if not self._subscribers:
                self._idle_since = time.monotonic()

    # ---------- replay ----------

    def replay_from(
        self, last_id: str | None
    ) -> list[SessionStreamEnvelope]:
        if not last_id:
            return list(self._ring)
        try:
            last_n = int(last_id)
        except (TypeError, ValueError):
            return list(self._ring)
        out: list[SessionStreamEnvelope] = []
        for ev in self._ring:
            try:
                if int(ev.id) > last_n:
                    out.append(ev)
            except ValueError:
                continue
        return out

    # ---------- introspection ----------

    @property
    def subscriber_count(self) -> int:
        return len(self._subscribers)

    @property
    def ring_size(self) -> int:
        return len(self._ring)

    def is_idle_past(self, grace_sec: float) -> bool:
        """True iff no subscribers and idle for at least ``grace_sec``."""
        if self._subscribers:
            return False
        if self._idle_since is None:
            # Newly created broadcaster with no subscribers yet — start
            # the idle clock now rather than treating "never had one" as
            # forever-idle.
            self._idle_since = time.monotonic()
            return False
        return (time.monotonic() - self._idle_since) >= grace_sec


# ---------------------------------------------------------------------------
# Process-wide registry
# ---------------------------------------------------------------------------
_registry: dict[str, SessionStream] = {}


def get_session_stream(
    session_id: str, *, agent_id: str | None = None, create: bool = True
) -> SessionStream | None:
    """Return the broadcaster for *session_id*, creating one if absent.

    When ``create=False`` returns ``None`` for unknown sessions — useful
    for the SSE handler to distinguish "unknown session" from "known
    but idle".
    """
    stream = _registry.get(session_id)
    if stream is not None:
        return stream
    if not create:
        return None
    stream = SessionStream(session_id, agent_id=agent_id)
    _registry[session_id] = stream
    return stream


def drop_session_stream(session_id: str) -> None:
    """Forcibly evict a broadcaster from the registry."""
    _registry.pop(session_id, None)


def sweep_idle_streams(
    grace_sec: float = CONVERSATION_STREAM_GRACE_SEC,
) -> int:
    """Drop any broadcasters that have been idle past ``grace_sec``.

    Intended to be called from a periodic sweeper task.  Returns the
    number of broadcasters evicted.
    """
    dropped: list[str] = []
    for sid, stream in list(_registry.items()):
        if stream.is_idle_past(grace_sec):
            dropped.append(sid)
    for sid in dropped:
        _registry.pop(sid, None)
    if dropped:
        logger.debug("session_stream: swept %d idle broadcasters", len(dropped))
    return len(dropped)


def reset_session_streams_for_tests() -> None:
    """Clear the registry.  Tests only."""
    _registry.clear()


def registry_size() -> int:
    return len(_registry)


# ---------------------------------------------------------------------------
# SSE route factory
# ---------------------------------------------------------------------------


def _sse_serialise(envelope: SessionStreamEnvelope) -> bytes:
    import json as _json

    body = _json.dumps(envelope.to_dict(), separators=(",", ":"))
    return (
        f"event: {envelope.type}\n"
        f"id: {envelope.id}\n"
        f"data: {body}\n\n"
    ).encode("utf-8")


def make_session_stream_handler(
    auth_token: str,
    *,
    agent_id: str | None = None,
    keepalive_sec: float = CONVERSATION_STREAM_KEEPALIVE_SEC,
):
    """Return a Starlette ASGI handler for ``GET /api/sessions/{id}/stream``.

    The factory keeps the three backend ``main.py`` wirings identical
    (same auth posture, same keepalive cadence, same Last-Event-ID
    resume).  Each backend passes its own ``CONVERSATIONS_AUTH_TOKEN``
    and agent identity for the envelope metadata.
    """
    import hmac as _hmac
    from starlette.requests import Request
    from starlette.responses import JSONResponse, StreamingResponse

    try:
        from conversations import auth_disabled_escape_hatch  # type: ignore
    except Exception:  # pragma: no cover
        from shared.conversations import auth_disabled_escape_hatch  # type: ignore

    async def handler(request: "Request"):
        # Auth — parity with /conversations, /trace, /mcp, /api/traces.
        if not auth_token:
            if not auth_disabled_escape_hatch():
                return JSONResponse(
                    {"error": "auth not configured"}, status_code=503
                )
        else:
            header = request.headers.get("Authorization", "")
            if not _hmac.compare_digest(f"Bearer {auth_token}", header):
                return JSONResponse({"error": "unauthorized"}, status_code=401)

        session_id = request.path_params.get("session_id") or ""
        if not session_id:
            return JSONResponse({"error": "missing session_id"}, status_code=400)

        stream = get_session_stream(session_id, agent_id=agent_id, create=True)

        last_event_id = request.headers.get("Last-Event-ID") or request.query_params.get("last_event_id")
        replay = stream.replay_from(last_event_id) if last_event_id else []

        async def _body():
            # Initial comment so proxies flush headers early.
            yield b": stream-start\n\n"
            # Replay ring tail (if requested).
            for ev in replay:
                yield _sse_serialise(ev)
            sub_iter = stream.subscribe()
            try:
                while True:
                    try:
                        envelope = await asyncio.wait_for(
                            sub_iter.__anext__(), timeout=keepalive_sec
                        )
                    except asyncio.TimeoutError:
                        yield b": keepalive\n\n"
                        continue
                    except StopAsyncIteration:
                        break
                    yield _sse_serialise(envelope)
                    if envelope.type == "stream.overrun":
                        break
            except asyncio.CancelledError:
                raise
            finally:
                # Best-effort close; iterator __aexit__ triggers
                # _remove_subscriber() which starts the idle clock.
                _aclose = getattr(sub_iter, "aclose", None)
                if _aclose is not None:
                    try:
                        await _aclose()
                    except Exception:
                        pass

        return StreamingResponse(
            _body(),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "X-Accel-Buffering": "no",
                "Connection": "keep-alive",
            },
        )

    return handler

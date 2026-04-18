"""Internal async message bus for the harness scheduler.

Dedup semantics (#615)
----------------------
Two enqueue paths exist, and the choice between them is deliberate:

* :meth:`MessageBus.send` — always enqueues, always awaits the result.
  Used by trigger dispatch, ad-hoc run endpoints, continuations, and any
  caller that must not silently drop a request. The ``_pending_kinds``
  membership is set on enqueue but does **not** dedup — a second call
  with the same ``kind`` still enqueues and the caller still gets its
  own future.

* :meth:`MessageBus.try_send` — dedups against ``_pending_kinds``;
  returns ``False`` when a message of the same ``kind`` is already
  in-flight. Used today only by the heartbeat scheduler, whose
  semantics are "fire at most one heartbeat per tick, silently skip
  ticks that overlap a still-running one". Job / task / trigger /
  continuation runners intentionally use ``send`` so overlapping
  schedules do not silently coalesce.

Dedup usage is already observable via ``agent_bus_dedup_total{kind}``
so operators can see which kinds actually experience dedup without
auditing call sites.
"""

import asyncio
import logging
import os
import time
from dataclasses import dataclass, field
from typing import Any, Callable

from metrics import agent_bus_dedup_total, agent_bus_pending_kinds, agent_bus_queue_depth
from utils import ConsensusEntry

BUS_MAX_QUEUE_DEPTH = int(os.environ.get("BUS_MAX_QUEUE_DEPTH", "100"))
BUS_SEND_TIMEOUT = float(os.environ.get("BUS_SEND_TIMEOUT", "30.0"))

logger = logging.getLogger(__name__)


@dataclass
class Message:
    prompt: str
    session_id: str | None = None
    kind: str = "a2a"  # "a2a", "heartbeat", "job:<name>", "task:<name>", "trigger:<endpoint>", "continuation:<name>"
    # TODO(#71): Bus fairness — if triggers ever need to be serialized with scheduled work, consider per-kind queue lanes.
    model: str | None = None
    backend_id: str | None = None
    consensus: list[ConsensusEntry] = field(default_factory=list)  # non-empty = fan-out to matching backends
    max_tokens: int | None = None  # per-dispatch token budget; backends stop when exceeded
    enqueued_at: float = 0.0
    result: asyncio.Future | None = field(default=None)
    metadata: dict[str, Any] = field(default_factory=dict)
    # W3C trace context attached to this message (#468). Carried through every
    # downstream A2A relay and webhook delivery. None for messages that
    # originate inside the harness without an inbound request — callers can
    # mint a fresh context via trace.new_context() when needed.
    trace_context: Any = None  # avoid forward-importing harness.trace


class MessageBus:
    def __init__(self):
        self._queue: asyncio.Queue[Message] = asyncio.Queue(maxsize=BUS_MAX_QUEUE_DEPTH)
        self._pending_kinds: set[str] = set()

    async def send(self, message: Message) -> str:
        if message.result is None:
            message.result = asyncio.get_running_loop().create_future()
        self._pending_kinds.add(message.kind)
        if agent_bus_pending_kinds is not None:
            agent_bus_pending_kinds.set(len(self._pending_kinds))
        message.enqueued_at = time.monotonic()
        try:
            try:
                await asyncio.wait_for(self._queue.put(message), timeout=BUS_SEND_TIMEOUT)
            except asyncio.TimeoutError:
                self._pending_kinds.discard(message.kind)
                if agent_bus_pending_kinds is not None:
                    agent_bus_pending_kinds.set(len(self._pending_kinds))
                logger.error(f"Bus send timed out after {BUS_SEND_TIMEOUT}s — queue full (depth={self._queue.qsize()})")
                raise asyncio.QueueFull()
            # Update queue depth unconditionally after put() succeeds. Placing
            # this update here (before awaiting result) ensures the gauge is
            # correct regardless of whether the awaiting coroutine is cancelled
            # or raises — the message is physically in the queue from this point.
            if agent_bus_queue_depth is not None:
                agent_bus_queue_depth.set(self._queue.qsize())
            try:
                return await message.result
            except BaseException:
                self._pending_kinds.discard(message.kind)
                if agent_bus_pending_kinds is not None:
                    agent_bus_pending_kinds.set(len(self._pending_kinds))
                raise
        except asyncio.CancelledError:
            self._pending_kinds.discard(message.kind)
            if agent_bus_pending_kinds is not None:
                agent_bus_pending_kinds.set(len(self._pending_kinds))
            raise

    def try_send(self, message: Message) -> bool:
        """Enqueue message only if no message of the same kind is already pending. Returns True if enqueued."""
        if message.kind in self._pending_kinds:
            if agent_bus_dedup_total is not None:
                agent_bus_dedup_total.labels(kind=message.kind).inc()
            if agent_bus_queue_depth is not None:
                agent_bus_queue_depth.set(self._queue.qsize())
            return False
        if message.result is None:
            message.result = asyncio.get_running_loop().create_future()
        self._pending_kinds.add(message.kind)
        if agent_bus_pending_kinds is not None:
            agent_bus_pending_kinds.set(len(self._pending_kinds))
        message.enqueued_at = time.monotonic()
        try:
            self._queue.put_nowait(message)
        except asyncio.QueueFull:
            self._pending_kinds.discard(message.kind)
            if agent_bus_pending_kinds is not None:
                agent_bus_pending_kinds.set(len(self._pending_kinds))
            return False
        if agent_bus_queue_depth is not None:
            agent_bus_queue_depth.set(self._queue.qsize())
        return True

    async def receive(self) -> Message:
        message = await self._queue.get()
        if agent_bus_queue_depth is not None:
            agent_bus_queue_depth.set(self._queue.qsize())
        return message

    def release_pending(self, kind: str) -> None:
        """Release the dedup slot for ``kind``.

        The ``_pending_kinds`` lifetime spans enqueue through the end of
        ``process_bus`` so ``try_send`` correctly dedups a second scheduled
        fire while the first is still executing (#514). Callers (the bus
        worker) must invoke this in a ``finally`` after processing so that
        both success and error paths clear the slot — otherwise a failed
        message would starve all future ``try_send`` for that kind.
        """
        self._pending_kinds.discard(kind)
        if agent_bus_pending_kinds is not None:
            agent_bus_pending_kinds.set(len(self._pending_kinds))


# ---------------------------------------------------------------------------
# Side-channel events (#633)
# ---------------------------------------------------------------------------
# The queue-based prompt path above deliberately deduplicates by ``kind`` and
# carries a ``result`` future the caller awaits.  Observability fan-out
# (webhooks, metrics sinks) has neither requirement: the producer should not
# block, listeners should not be deduped against one another, and a missing
# listener must not back-pressure the caller.  We keep those semantics in a
# separate lightweight callback registry rather than reusing ``MessageBus``.
#
# As of #633 the only event shape defined on this channel is
# :class:`HookDecisionEvent`.  Backends (which run in a separate process from
# the harness today) cannot reach this registry directly — a follow-up gap
# will file the cross-process transport.  The in-process scaffold exists so
# harness-side call sites and unit tests have a stable API to target before
# that transport lands.


@dataclass
class HookDecisionEvent:
    """Structured record of a single PreToolUse hook decision (#633).

    Emitted whenever an claude hook evaluator finalises a decision
    (allow / warn / deny).  Mirrors the attribute set stamped onto the OTel
    span event so downstream consumers see the same shape regardless of the
    transport.  ``traceparent`` is a serialised W3C trace-context header so
    webhook receivers can correlate the event with the originating trace.
    """

    agent: str
    session_id: str
    tool: str
    decision: str  # "allow" | "warn" | "deny"
    rule_name: str
    reason: str
    source: str  # "baseline" | "extension" | ...
    traceparent: str | None = None


# Registered listener callbacks.  Each is invoked synchronously from
# :func:`publish_hook_decision`; a listener that must do async work should
# schedule it onto its own loop (e.g. ``asyncio.create_task``).  Failures in
# any one listener must not prevent the others from running and must never
# propagate out of ``publish_hook_decision``.
_hook_decision_listeners: list[Callable[[HookDecisionEvent], None]] = []


def subscribe_hook_decision(listener: Callable[[HookDecisionEvent], None]) -> None:
    """Register *listener* for future :class:`HookDecisionEvent` publications."""
    _hook_decision_listeners.append(listener)


def unsubscribe_hook_decision(listener: Callable[[HookDecisionEvent], None]) -> None:
    """Remove a previously registered listener. No-op if not registered."""
    try:
        _hook_decision_listeners.remove(listener)
    except ValueError:
        pass


def publish_hook_decision(event: HookDecisionEvent) -> None:
    """Fan ``event`` out to every registered listener. Never raises."""
    for listener in list(_hook_decision_listeners):
        try:
            listener(event)
        except Exception as exc:  # pragma: no cover — best-effort side channel
            logger.warning("hook.decision listener %r raised: %r", listener, exc)

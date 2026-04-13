import asyncio
import logging
import os
import time
from dataclasses import dataclass, field
from typing import Any

from metrics import agent_bus_dedup_total, agent_bus_pending_kinds, agent_bus_queue_depth

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
    consensus: bool = False  # when True, nyx fans out to all backends and aggregates responses
    enqueued_at: float = 0.0
    result: asyncio.Future | None = field(default=None)
    metadata: dict[str, Any] = field(default_factory=dict)


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
        self._pending_kinds.discard(message.kind)
        if agent_bus_pending_kinds is not None:
            agent_bus_pending_kinds.set(len(self._pending_kinds))
        return message

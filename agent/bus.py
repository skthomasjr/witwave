import asyncio
import os
import time
from dataclasses import dataclass, field
from typing import Any

from metrics import agent_bus_dedup_total, agent_bus_pending_kinds, agent_bus_queue_depth

BUS_MAX_QUEUE_DEPTH = int(os.environ.get("BUS_MAX_QUEUE_DEPTH", "100"))


@dataclass
class Message:
    prompt: str
    session_id: str | None = None
    kind: str = "a2a"  # "a2a", "heartbeat", "agenda"
    model: str | None = None
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
            await self._queue.put(message)
            if agent_bus_queue_depth is not None:
                agent_bus_queue_depth.set(self._queue.qsize())
            return await message.result
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

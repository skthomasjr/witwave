import asyncio
import logging
import os
from dataclasses import dataclass
from pathlib import Path

from bus import Message, MessageBus
from metrics import (
    agent_continuation_fires_total,
    agent_continuation_items_registered,
    agent_continuation_parse_errors_total,
    agent_continuation_reloads_total,
    agent_continuation_runs_total,
    agent_file_watcher_restarts_total,
    agent_watcher_events_total,
)
from utils import parse_duration, parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

CONTINUATIONS_DIR = os.environ.get("CONTINUATIONS_DIR", "/home/agent/.nyx/continuations")


@dataclass
class ContinuationItem:
    path: str
    name: str
    continues_after: str      # e.g. "job:code-review", "task:standup", "a2a", "continuation:foo", "*"
    content: str              # prompt body
    on_success: bool = True   # fire on successful upstream completion
    on_error: bool = False    # fire on upstream error
    trigger_when: str | None = None   # only fire if upstream response contains this string
    delay: float | None = None        # seconds to wait before firing
    session_id: str | None = None     # if None, inherit upstream session_id at fire time
    model: str | None = None
    backend_id: str | None = None
    description: str | None = None


def parse_continuation_file(path: str) -> ContinuationItem | None:
    try:
        with open(path) as f:
            raw = f.read()

        fields, content = parse_frontmatter(raw)

        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
            if not enabled:
                logger.info(f"Continuation file {path}: disabled, skipping.")
                return None

        continues_after = fields.get("continues-after") or ""
        if not continues_after.strip():
            logger.warning(f"Continuation file {path}: missing required 'continues-after' field, skipping.")
            return None

        filename = Path(path).stem
        name = fields.get("name") or filename

        on_success = True
        if "on-success" in fields:
            on_success = str(fields["on-success"]).lower() not in ("false", "")

        on_error = False
        if "on-error" in fields:
            on_error = str(fields["on-error"]).lower() not in ("false", "")

        trigger_when = fields.get("trigger-when") or None

        delay: float | None = None
        delay_raw = fields.get("delay")
        if delay_raw:
            try:
                delay = parse_duration(str(delay_raw))
            except ValueError as e:
                logger.warning(f"Continuation file {path}: invalid 'delay': {e}, ignoring.")

        session_id = fields.get("session") or None
        model = fields.get("model") or None
        backend_id = fields.get("agent") or None
        description = fields.get("description") or None

        return ContinuationItem(
            path=path,
            name=name,
            continues_after=continues_after,
            content=content,
            on_success=on_success,
            on_error=on_error,
            trigger_when=trigger_when,
            delay=delay,
            session_id=session_id,
            model=model,
            backend_id=backend_id,
            description=description,
        )

    except Exception as e:
        if agent_continuation_parse_errors_total is not None:
            agent_continuation_parse_errors_total.inc()
        logger.error(f"Continuation file {path}: failed to parse — {e}, skipping.")
        return None


async def _fire(item: ContinuationItem, session_id: str, bus: MessageBus) -> None:
    if item.delay is not None:
        await asyncio.sleep(item.delay)
    prompt = f"Continuation: {item.name}\n\n{item.content}"
    resolved_session = item.session_id or session_id
    try:
        response = await bus.send(Message(
            prompt=prompt,
            session_id=resolved_session,
            kind=f"continuation:{item.name}",
            model=item.model,
            backend_id=item.backend_id,
        ))
        if agent_continuation_runs_total is not None:
            agent_continuation_runs_total.labels(name=item.name, status="success").inc()
        logger.info(f"Continuation '{item.name}' completed successfully. Response: {response!r}")
    except Exception as e:
        if agent_continuation_runs_total is not None:
            agent_continuation_runs_total.labels(name=item.name, status="error").inc()
        logger.error(f"Continuation '{item.name}' error: {e}")


class ContinuationRunner:
    def __init__(self):
        self._items: dict[str, ContinuationItem] = {}

    def _register(self, path: str) -> None:
        item = parse_continuation_file(path)
        self._unregister(path)
        if item is None:
            return
        self._items[path] = item
        if agent_continuation_items_registered is not None:
            agent_continuation_items_registered.set(len(self._items))
        logger.info(f"Continuation '{item.name}' registered (continues-after: {item.continues_after}).")

    def _unregister(self, path: str) -> None:
        existing = self._items.pop(path, None)
        if existing is not None:
            logger.info(f"Continuation '{existing.name}' unregistered.")
        if agent_continuation_items_registered is not None:
            agent_continuation_items_registered.set(len(self._items))

    async def _scan(self) -> None:
        if not os.path.isdir(CONTINUATIONS_DIR):
            return
        try:
            filenames = os.listdir(CONTINUATIONS_DIR)
        except OSError:
            return
        for filename in filenames:
            if filename.endswith(".md"):
                self._register(os.path.join(CONTINUATIONS_DIR, filename))

    def notify(self, kind: str, session_id: str, success: bool, response: str, bus: MessageBus) -> None:
        """Called by on_prompt_completed() when an upstream completes. Non-blocking."""
        for item in list(self._items.values()):
            # TODO: fan-in — single upstream only for now; fan-in deferred
            upstream_matches = item.continues_after == "*" or item.continues_after == kind
            outcome_matches = (success and item.on_success) or (not success and item.on_error)
            content_matches = item.trigger_when is None or item.trigger_when in response
            if upstream_matches and outcome_matches and content_matches:
                if agent_continuation_fires_total is not None:
                    agent_continuation_fires_total.labels(upstream_kind=kind).inc()
                asyncio.ensure_future(_fire(item, session_id, bus))

    async def run(self) -> None:
        logger.info(f"Continuation runner watching {CONTINUATIONS_DIR}")

        while True:
            if not os.path.isdir(CONTINUATIONS_DIR):
                logger.info("Continuations directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            asyncio.ensure_future(self._scan())
            async for changes in awatch(CONTINUATIONS_DIR):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="continuations").inc()
                for _, path in changes:
                    if not path.endswith(".md"):
                        continue
                    if os.path.exists(path):
                        logger.info(f"Continuation file changed: {path}")
                        if agent_continuation_reloads_total is not None:
                            agent_continuation_reloads_total.inc()
                        self._register(path)
                    else:
                        logger.info(f"Continuation file removed: {path}")
                        if agent_continuation_reloads_total is not None:
                            agent_continuation_reloads_total.inc()
                        self._unregister(path)

            logger.warning("Continuations directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="continuations").inc()
            for path in list(self._items.keys()):
                self._unregister(path)
            await asyncio.sleep(10)

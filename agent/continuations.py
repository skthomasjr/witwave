import asyncio
import logging
import os
from dataclasses import dataclass
from fnmatch import fnmatch
from pathlib import Path

from bus import Message, MessageBus
from metrics import (
    agent_continuation_fires_total,
    agent_continuation_items_registered,
    agent_continuation_parse_errors_total,
    agent_continuation_reloads_total,
    agent_continuation_runs_total,
    agent_continuation_throttled_total,
    agent_file_watcher_restarts_total,
    agent_watcher_events_total,
)
from utils import parse_duration, parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

CONTINUATIONS_DIR = os.environ.get("CONTINUATIONS_DIR", "/home/agent/.nyx/continuations")

# Sentinel returned by parse_continuation_file() when the file is explicitly
# disabled (enabled: false).  Distinct from None (parse error) so that
# _register() can unregister a disabled continuation rather than preserving it.
_DISABLED = object()

# Global default cap on concurrent in-flight fires per continuation.
# Overridable per-continuation via the max-concurrent-fires frontmatter field.
CONTINUATION_MAX_CONCURRENT_FIRES = int(os.environ.get("CONTINUATION_MAX_CONCURRENT_FIRES", "5"))


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
    consensus: bool = False
    max_tokens: int | None = None
    max_concurrent_fires: int = CONTINUATION_MAX_CONCURRENT_FIRES


def parse_continuation_file(path: str) -> "ContinuationItem | object | None":
    """Parse a continuation file. Returns:
    - ContinuationItem on success
    - _DISABLED sentinel when enabled: false or continues-after is missing/empty
    - None on parse error (caller should preserve last known good registration)
    """
    try:
        with open(path) as f:
            raw = f.read()

        fields, content = parse_frontmatter(raw)

        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
            if not enabled:
                logger.info(f"Continuation file {path}: disabled, skipping.")
                return _DISABLED

        continues_after = fields.get("continues-after") or ""
        if not continues_after.strip():
            logger.warning(f"Continuation file {path}: missing required 'continues-after' field, skipping.")
            return _DISABLED

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
        consensus = str(fields.get("consensus", "false")).lower() not in ("false", "")
        max_tokens: int | None = None
        max_tokens_raw = fields.get("max-tokens") or fields.get("max_tokens")
        if max_tokens_raw is not None:
            try:
                max_tokens = max(1, int(max_tokens_raw))
            except (ValueError, TypeError):
                logger.warning(f"Continuation file {path}: invalid 'max-tokens' value {max_tokens_raw!r}, ignoring.")

        max_concurrent_fires = CONTINUATION_MAX_CONCURRENT_FIRES
        max_fires_raw = fields.get("max-concurrent-fires") or fields.get("max_concurrent_fires")
        if max_fires_raw is not None:
            try:
                max_concurrent_fires = max(1, int(max_fires_raw))
            except (ValueError, TypeError):
                logger.warning(
                    f"Continuation file {path}: invalid 'max-concurrent-fires' value {max_fires_raw!r}, "
                    f"using default {CONTINUATION_MAX_CONCURRENT_FIRES}."
                )

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
            consensus=consensus,
            max_tokens=max_tokens,
            max_concurrent_fires=max_concurrent_fires,
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
            consensus=item.consensus,
            max_tokens=item.max_tokens,
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
        self._active_fires: set[asyncio.Task] = set()
        # Per-continuation in-flight tasks, keyed by continuation name.
        self._fires_by_name: dict[str, set[asyncio.Task]] = {}

    def _register(self, path: str) -> None:
        result = parse_continuation_file(path)
        if result is _DISABLED:
            self._unregister(path)
            return
        if result is None:
            # Parse error — preserve the last known good registration.
            return
        item = result
        self._unregister(path)
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

    def items(self) -> list[dict]:
        """Return a serializable snapshot of currently registered continuation items."""
        result = []
        for item in self._items.values():
            result.append({
                "name": item.name,
                "continues_after": item.continues_after,
                "on_success": item.on_success,
                "on_error": item.on_error,
                "trigger_when": item.trigger_when,
                "delay": item.delay,
                "description": item.description,
                "backend_id": item.backend_id,
                "model": item.model,
                "consensus": item.consensus,
                "max_tokens": item.max_tokens,
                "max_concurrent_fires": item.max_concurrent_fires,
                "active_fires": len(self._fires_by_name.get(item.name, set())),
            })
        return result

    def notify(self, kind: str, session_id: str, success: bool, response: str, bus: MessageBus) -> None:
        """Called by on_prompt_completed() when an upstream completes. Non-blocking."""
        for item in list(self._items.values()):
            # TODO: fan-in — single upstream only for now; fan-in deferred
            upstream_matches = item.continues_after == "*" or fnmatch(kind, item.continues_after)
            outcome_matches = (success and item.on_success) or (not success and item.on_error)
            content_matches = item.trigger_when is None or item.trigger_when in response
            if upstream_matches and outcome_matches and content_matches:
                # Throttle: skip this fire if the per-continuation in-flight
                # count already equals max_concurrent_fires.
                fires = self._fires_by_name.setdefault(item.name, set())
                if len(fires) >= item.max_concurrent_fires:
                    logger.warning(
                        f"Continuation '{item.name}': max_concurrent_fires ({item.max_concurrent_fires}) "
                        f"reached — skipping fire for upstream '{kind}'."
                    )
                    if agent_continuation_throttled_total is not None:
                        agent_continuation_throttled_total.labels(name=item.name).inc()
                    continue
                if agent_continuation_fires_total is not None:
                    agent_continuation_fires_total.labels(upstream_kind=kind).inc()
                _t = asyncio.ensure_future(_fire(item, session_id, bus))
                self._active_fires.add(_t)
                fires.add(_t)
                def _cleanup(t: asyncio.Task, _name: str = item.name) -> None:
                    self._active_fires.discard(t)
                    self._fires_by_name.get(_name, set()).discard(t)
                _t.add_done_callback(_cleanup)

    async def run(self) -> None:
        logger.info(f"Continuation runner watching {CONTINUATIONS_DIR}")

        while True:
            if not os.path.isdir(CONTINUATIONS_DIR):
                logger.info("Continuations directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            _scan_task = asyncio.ensure_future(self._scan())

            def _scan_done(t: asyncio.Task) -> None:
                if not t.cancelled() and t.exception() is not None:
                    logger.error("Continuation runner _scan crashed: %r", t.exception())

            _scan_task.add_done_callback(_scan_done)
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

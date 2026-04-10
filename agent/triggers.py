import asyncio
import logging
import os
import re
import uuid
from dataclasses import dataclass
from pathlib import Path

from metrics import (
    agent_file_watcher_restarts_total,
    agent_triggers_items_registered,
    agent_triggers_parse_errors_total,
    agent_triggers_reloads_total,
    agent_watcher_events_total,
)
from utils import parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

TRIGGERS_DIR = os.environ.get("TRIGGERS_DIR", "/home/agent/.nyx/triggers")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")

_ENDPOINT_RE = re.compile(r"^[a-z0-9][a-z0-9-]*$")

# Sentinel returned by parse_trigger_file when the file is explicitly disabled.
# Distinct from None (parse error) so _register can unregister on disable.
_DISABLED = object()


@dataclass
class TriggerItem:
    path: str
    name: str
    endpoint: str
    session_id: str
    content: str
    enabled: bool = True
    secret_env_var: str | None = None
    model: str | None = None
    backend_id: str | None = None
    description: str | None = None


def parse_trigger_file(path: str) -> TriggerItem | object | None:
    """Parse a trigger file. Returns:
    - TriggerItem on success
    - _DISABLED sentinel when enabled: false is set
    - None on parse error (caller should preserve existing registration)
    """
    try:
        with open(path) as f:
            raw = f.read()

        fields, content = parse_frontmatter(raw)

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
        if not enabled:
            logger.info(f"Trigger file {path}: disabled, skipping.")
            return _DISABLED

        endpoint = fields.get("endpoint") or None
        if not endpoint:
            logger.warning(f"Trigger file {path}: missing 'endpoint' in frontmatter, skipping.")
            return None
        if not _ENDPOINT_RE.match(endpoint):
            logger.warning(
                f"Trigger file {path}: 'endpoint' {endpoint!r} is invalid — "
                "must match ^[a-z0-9][a-z0-9-]*$, skipping."
            )
            return None

        filename = Path(path).stem
        name = fields.get("name") or filename
        session_id = fields.get("session") or str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.{endpoint}"))
        secret_env_var = fields.get("secret-env-var") or fields.get("secret_env_var") or None
        model = fields.get("model") or None
        backend_id = fields.get("agent") or None
        description = fields.get("description") or None

        return TriggerItem(
            path=path,
            name=name,
            endpoint=endpoint,
            session_id=session_id,
            content=content,
            enabled=enabled,
            secret_env_var=secret_env_var,
            model=model,
            backend_id=backend_id,
            description=description,
        )

    except Exception as e:
        if agent_triggers_parse_errors_total is not None:
            agent_triggers_parse_errors_total.inc()
        logger.error(f"Trigger file {path}: failed to parse — {e}, skipping.")
        return None


class TriggerRunner:
    def __init__(self):
        self._items: dict[str, TriggerItem] = {}
        self._running: set[str] = set()

    def _register(self, path: str, *, count_reload: bool = False) -> None:
        result = parse_trigger_file(path)
        if result is _DISABLED:
            self._unregister(path, count_reload=count_reload)
            return
        if result is None:
            # Parse error — preserve the last known good registration.
            return
        item = result
        self._unregister(path)
        self._items[path] = item
        if agent_triggers_items_registered is not None:
            agent_triggers_items_registered.set(len(self._items))
        if count_reload and agent_triggers_reloads_total is not None:
            agent_triggers_reloads_total.inc()
        logger.info(f"Trigger '{item.name}' registered at endpoint /{item.endpoint}.")

    def _unregister(self, path: str, *, count_reload: bool = False) -> None:
        existing = self._items.pop(path, None)
        if existing:
            logger.info(f"Trigger '{existing.name}' unregistered.")
            if agent_triggers_items_registered is not None:
                agent_triggers_items_registered.set(len(self._items))
            if count_reload and agent_triggers_reloads_total is not None:
                agent_triggers_reloads_total.inc()

    async def _scan(self) -> None:
        if not os.path.isdir(TRIGGERS_DIR):
            return
        try:
            filenames = os.listdir(TRIGGERS_DIR)
        except OSError:
            return
        for filename in filenames:
            if filename.endswith(".md"):
                self._register(os.path.join(TRIGGERS_DIR, filename))

    def items_by_endpoint(self) -> dict[str, TriggerItem]:
        return {item.endpoint: item for item in self._items.values()}

    async def run(self) -> None:
        logger.info(f"Trigger runner watching {TRIGGERS_DIR}")

        while True:
            if not os.path.isdir(TRIGGERS_DIR):
                logger.info("Triggers directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            asyncio.ensure_future(self._scan())
            async for changes in awatch(TRIGGERS_DIR):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="triggers").inc()
                for _, path in changes:
                    if not path.endswith(".md"):
                        continue
                    if os.path.exists(path):
                        logger.info(f"Trigger file changed: {path}")
                        self._register(path, count_reload=True)
                    else:
                        logger.info(f"Trigger file removed: {path}")
                        self._unregister(path, count_reload=True)

            logger.warning("Triggers directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="triggers").inc()
            for path in list(self._items.keys()):
                self._unregister(path)
            await asyncio.sleep(10)

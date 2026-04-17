import asyncio
import logging
import os
import re
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path

from metrics import (
    agent_file_watcher_restarts_total,
    agent_triggers_items_registered,
    agent_triggers_parse_errors_total,
    agent_triggers_reloads_total,
    agent_watcher_events_total,
)
from utils import (
    ConsensusEntry,
    parse_consensus,
    parse_frontmatter,
    parse_frontmatter_raw,
    run_awatch_loop,
)

logger = logging.getLogger(__name__)

TRIGGERS_DIR = os.environ.get("TRIGGERS_DIR", "/home/agent/.nyx/triggers")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx")

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
    consensus: list[ConsensusEntry] = field(default_factory=list)
    max_tokens: int | None = None


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
        raw_fields, _ = parse_frontmatter_raw(raw)

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
        consensus = parse_consensus(raw_fields.get("consensus"))

        max_tokens: int | None = None
        max_tokens_raw = fields.get("max-tokens") or fields.get("max_tokens")
        if max_tokens_raw is not None:
            try:
                max_tokens = max(1, int(max_tokens_raw))
            except (ValueError, TypeError):
                logger.warning(f"Trigger file {path}: invalid 'max-tokens' value {max_tokens_raw!r}, ignoring.")

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
            consensus=consensus,
            max_tokens=max_tokens,
        )

    except Exception as e:
        if agent_triggers_parse_errors_total is not None:
            agent_triggers_parse_errors_total.inc()
        logger.error(f"Trigger file {path}: failed to parse — {e}, skipping.")
        return None


class TriggerRunner:
    def __init__(self):
        self._items: dict[str, TriggerItem] = {}
        # Endpoints currently executing. Safe as a plain set because asyncio
        # runs on a single thread — the check-then-add in the request handler
        # has no await between them, so no concurrent coroutine can interleave.
        # If multi-worker (multi-threaded) uvicorn is ever used, replace with a
        # thread-safe structure (e.g. threading.Lock + set).
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

    def items(self) -> list[dict]:
        """Return a serializable snapshot of currently registered trigger items."""
        result = []
        for item in self._items.values():
            result.append({
                "name": item.name,
                "endpoint": item.endpoint,
                "description": item.description,
                "session_id": item.session_id,
                "backend_id": item.backend_id,
                "model": item.model,
                "consensus": [asdict(e) for e in item.consensus],
                "max_tokens": item.max_tokens,
                "running": item.endpoint in self._running,
                # Expose enabled and HMAC-signed status so the UI can render
                # them — previously dropped between model and wire (#461).
                "enabled": item.enabled,
                "signed": bool(item.secret_env_var),
            })
        return result

    def items_by_endpoint(self) -> dict[str, TriggerItem]:
        return {item.endpoint: item for item in self._items.values()}

    async def run(self) -> None:
        logger.info(f"Trigger runner watching {TRIGGERS_DIR}")

        def _on_change(path: str) -> None:
            logger.info(f"Trigger file changed: {path}")
            self._register(path, count_reload=True)

        def _on_delete(path: str) -> None:
            logger.info(f"Trigger file removed: {path}")
            self._unregister(path, count_reload=True)

        def _cleanup() -> None:
            for path in list(self._items.keys()):
                self._unregister(path)

        await run_awatch_loop(
            directory=TRIGGERS_DIR,
            watcher_name="triggers",
            scan=self._scan,
            on_change=_on_change,
            on_delete=_on_delete,
            cleanup=_cleanup,
            logger_=logger,
            not_found_message="Triggers directory not found — retrying in 10s.",
            watcher_exited_message="Triggers directory watcher exited — directory deleted or unavailable. Retrying in 10s.",
            watcher_events_metric=agent_watcher_events_total,
            file_watcher_restarts_metric=agent_file_watcher_restarts_total,
        )

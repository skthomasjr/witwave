import asyncio
import json
import logging
import os
import time
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path

from bus import Message, MessageBus
from croniter import croniter
from metrics import (
    agent_agenda_checkpoint_stale_total,
    agent_agenda_duration_seconds,
    agent_agenda_error_duration_seconds,
    agent_agenda_item_last_error_timestamp_seconds,
    agent_agenda_item_last_run_timestamp_seconds,
    agent_agenda_item_last_success_timestamp_seconds,
    agent_agenda_items_registered,
    agent_agenda_lag_seconds,
    agent_agenda_parse_errors_total,
    agent_agenda_reloads_total,
    agent_agenda_running_items,
    agent_agenda_runs_total,
    agent_agenda_skips_total,
    agent_checkpoint_write_errors_total,
    agent_file_watcher_restarts_total,
    agent_watcher_events_total,
)
from utils import parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

AGENDA_DIR = os.environ.get("AGENDA_DIR", "/home/agent/.nyx/agenda")
CHECKPOINT_DIR = os.path.join(AGENDA_DIR, ".checkpoints")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")
_AGENDA_MAX_CONCURRENT = int(os.environ.get("AGENDA_MAX_CONCURRENT", "0"))


@dataclass
class AgendaItem:
    path: str
    name: str
    schedule: str
    session_id: str
    content: str
    model: str | None = None
    task: asyncio.Task | None = field(default=None, compare=False)
    running: bool = False


def parse_agenda_file(path: str) -> AgendaItem | None:
    try:
        with open(path) as f:
            raw = f.read()

        enabled = True

        fields, content = parse_frontmatter(raw)
        name = fields.get("name") or None
        schedule = fields.get("schedule") or None
        session_id = fields.get("session") or None
        model = fields.get("model") or None
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")

        if not enabled:
            logger.info(f"Agenda file {path}: disabled, skipping.")
            return None

        if not schedule:
            logger.warning(f"Agenda file {path}: missing 'schedule' in frontmatter, skipping.")
            return None

        if not croniter.is_valid(schedule):
            logger.warning(f"Agenda file {path}: invalid cron expression '{schedule}', skipping.")
            return None

        filename = Path(path).stem
        name = name or filename
        if not session_id:
            # Generate a deterministic UUID from the agent name and filename
            session_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.{filename}"))

        return AgendaItem(path=path, name=name, schedule=schedule, session_id=session_id, content=content, model=model)

    except Exception as e:
        if agent_agenda_parse_errors_total is not None:
            agent_agenda_parse_errors_total.inc()
        logger.error(f"Agenda file {path}: failed to parse — {e}, skipping.")
        return None


async def run_agenda_item(item: AgendaItem, bus: MessageBus, semaphore: asyncio.Semaphore | None = None) -> None:
    cron = croniter(item.schedule, datetime.now(timezone.utc))
    while True:
        next_run = cron.get_next(datetime)
        now = datetime.now(timezone.utc)
        delay = (next_run - now).total_seconds()
        logger.info(f"Agenda '{item.name}' next run in {delay:.0f}s at {next_run.isoformat()}")
        await asyncio.sleep(delay)

        if agent_agenda_lag_seconds is not None:
            lag = (datetime.now(timezone.utc) - next_run).total_seconds()
            agent_agenda_lag_seconds.observe(lag)

        if item.running:
            logger.warning(f"Agenda '{item.name}' still running from previous turn, skipping.")
            if agent_agenda_skips_total is not None:
                agent_agenda_skips_total.labels(name=item.name).inc()
            continue

        if semaphore is not None:
            await semaphore.acquire()

        item.running = True
        if agent_agenda_running_items is not None:
            agent_agenda_running_items.inc()
        checkpoint_path = None
        try:
            os.makedirs(CHECKPOINT_DIR, exist_ok=True)
            checkpoint_path = os.path.join(CHECKPOINT_DIR, Path(item.path).stem + ".running.json")
            with open(checkpoint_path, "w") as f:
                json.dump(
                    {
                        "started_at": datetime.now(timezone.utc).isoformat(),
                        "name": item.name,
                        "session_id": item.session_id,
                    },
                    f,
                )
        except Exception as e:
            if agent_checkpoint_write_errors_total is not None:
                agent_checkpoint_write_errors_total.inc()
            logger.error(f"Agenda '{item.name}' checkpoint write failed: {e}")
        try:
            prompt = f"Agenda item: {item.name}\n\n{item.content}"
            logger.info(f"Agenda '{item.name}' firing.")
            _agenda_start = time.monotonic()
            message = Message(prompt=prompt, session_id=item.session_id, kind=f"agenda:{item.name}", model=item.model)
            if agent_agenda_item_last_run_timestamp_seconds is not None:
                agent_agenda_item_last_run_timestamp_seconds.labels(name=item.name).set(time.time())
            _send_task = asyncio.ensure_future(bus.send(message))

            def _log_send_result(t: asyncio.Task, _name: str = item.name) -> None:
                exc = t.exception() if not t.cancelled() else None
                if exc is not None:
                    logger.error(f"Agenda '{_name}' background bus.send failed: {exc}")

            _send_task.add_done_callback(_log_send_result)
            await asyncio.shield(_send_task)
            if agent_agenda_duration_seconds is not None:
                agent_agenda_duration_seconds.labels(name=item.name).observe(time.monotonic() - _agenda_start)
            if agent_agenda_runs_total is not None:
                agent_agenda_runs_total.labels(name=item.name, status="success").inc()
            if agent_agenda_item_last_success_timestamp_seconds is not None:
                agent_agenda_item_last_success_timestamp_seconds.labels(name=item.name).set(time.time())
        except asyncio.CancelledError:
            logger.info(f"Agenda '{item.name}' cancelled — bus.send continues in background supervised.")
            raise
        except Exception as e:
            logger.error(f"Agenda '{item.name}' error: {e}")
            if agent_agenda_runs_total is not None:
                agent_agenda_runs_total.labels(name=item.name, status="error").inc()
            if agent_agenda_error_duration_seconds is not None:
                agent_agenda_error_duration_seconds.labels(name=item.name).observe(time.monotonic() - _agenda_start)
            if agent_agenda_item_last_error_timestamp_seconds is not None:
                agent_agenda_item_last_error_timestamp_seconds.labels(name=item.name).set(time.time())
        finally:
            item.running = False
            if agent_agenda_running_items is not None:
                agent_agenda_running_items.dec()
            if semaphore is not None:
                semaphore.release()
            if checkpoint_path is not None:
                try:
                    os.remove(checkpoint_path)
                except FileNotFoundError:
                    pass
                except Exception as e:
                    if agent_checkpoint_write_errors_total is not None:
                        agent_checkpoint_write_errors_total.inc()
                    logger.warning(f"Agenda '{item.name}' checkpoint delete failed: {e}")


class AgendaRunner:
    def __init__(self, bus: MessageBus):
        self._bus = bus
        self._items: dict[str, AgendaItem] = {}
        self._semaphore: asyncio.Semaphore | None = (
            asyncio.Semaphore(_AGENDA_MAX_CONCURRENT) if _AGENDA_MAX_CONCURRENT > 0 else None
        )
        if self._semaphore is not None:
            logger.info(f"Agenda concurrency limit: {_AGENDA_MAX_CONCURRENT} concurrent items")

    async def _register(self, path: str) -> None:
        item = parse_agenda_file(path)
        if not item:
            return
        cancelled = self._unregister(path)
        if cancelled is not None:
            await asyncio.gather(cancelled, return_exceptions=True)
        task = asyncio.create_task(run_agenda_item(item, self._bus, self._semaphore))
        item.task = task
        self._items[path] = item
        if agent_agenda_items_registered is not None:
            agent_agenda_items_registered.set(len(self._items))
        logger.info(f"Agenda '{item.name}' registered. Schedule: {item.schedule}")

    def _unregister(self, path: str) -> asyncio.Task | None:
        existing = self._items.pop(path, None)
        if existing and existing.task:
            if existing.running:
                logger.info(f"Agenda '{existing.name}' unregistered — cancelling while run is in progress.")
            else:
                logger.info(f"Agenda '{existing.name}' unregistered.")
            existing.task.cancel()
            if agent_agenda_items_registered is not None:
                agent_agenda_items_registered.set(len(self._items))
            return existing.task
        if agent_agenda_items_registered is not None:
            agent_agenda_items_registered.set(len(self._items))
        return None

    async def _scan(self) -> None:
        if os.path.isdir(CHECKPOINT_DIR):
            try:
                cp_filenames = os.listdir(CHECKPOINT_DIR)
            except OSError:
                cp_filenames = []
            for cp_filename in cp_filenames:
                if cp_filename.endswith(".running.json"):
                    cp_path = os.path.join(CHECKPOINT_DIR, cp_filename)
                    try:
                        with open(cp_path) as f:
                            data = json.load(f)
                        name = data.get("name") or Path(cp_filename).stem
                    except Exception:
                        name = Path(cp_filename).stem
                    logger.warning(f"Agenda '{name}': stale checkpoint at {cp_path} — run may have been interrupted")
                    if agent_agenda_checkpoint_stale_total is not None:
                        agent_agenda_checkpoint_stale_total.inc()
                    try:
                        os.remove(cp_path)
                    except Exception as rm_err:
                        logger.warning(f"Agenda '{name}': failed to remove stale checkpoint {cp_path}: {rm_err}")
        if not os.path.isdir(AGENDA_DIR):
            return
        try:
            agenda_files = os.listdir(AGENDA_DIR)
        except OSError:
            return
        for filename in agenda_files:
            if filename.endswith(".md"):
                await self._register(os.path.join(AGENDA_DIR, filename))

    async def run(self) -> None:
        logger.info(f"Agenda runner watching {AGENDA_DIR}")

        while True:
            if not os.path.isdir(AGENDA_DIR):
                logger.info("Agenda directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            await self._scan()
            async for changes in awatch(AGENDA_DIR):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="agenda").inc()
                for _, path in changes:
                    if not path.endswith(".md"):
                        continue
                    if os.path.exists(path):
                        logger.info(f"Agenda file changed: {path}")
                        if agent_agenda_reloads_total is not None:
                            agent_agenda_reloads_total.inc()
                        await self._register(path)
                    else:
                        logger.info(f"Agenda file removed: {path}")
                        if agent_agenda_reloads_total is not None:
                            agent_agenda_reloads_total.inc()
                        self._unregister(path)

            logger.warning("Agenda directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="agenda").inc()
            cancelled = [t for path in list(self._items.keys()) if (t := self._unregister(path)) is not None]
            if cancelled:
                await asyncio.gather(*cancelled, return_exceptions=True)
            await asyncio.sleep(10)

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
    agent_job_checkpoint_stale_total,
    agent_job_duration_seconds,
    agent_job_error_duration_seconds,
    agent_job_item_last_error_timestamp_seconds,
    agent_job_item_last_run_timestamp_seconds,
    agent_job_item_last_success_timestamp_seconds,
    agent_job_items_registered,
    agent_job_lag_seconds,
    agent_job_parse_errors_total,
    agent_job_reloads_total,
    agent_job_running_items,
    agent_job_runs_total,
    agent_job_skips_total,
    agent_checkpoint_write_errors_total,
    agent_file_watcher_restarts_total,
    agent_watcher_events_total,
)
from utils import parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

JOBS_DIR = os.environ.get("JOBS_DIR", "/home/agent/.nyx/jobs")
CHECKPOINT_DIR = os.path.join(JOBS_DIR, ".checkpoints")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-harness")
_JOBS_MAX_CONCURRENT = int(os.environ.get("JOBS_MAX_CONCURRENT", "0"))


@dataclass
class JobItem:
    path: str
    name: str
    schedule: str | None
    session_id: str
    content: str
    model: str | None = None
    backend_id: str | None = None
    consensus: bool = False
    max_tokens: int | None = None
    task: asyncio.Task | None = field(default=None, compare=False)
    running: bool = False


def parse_job_file(path: str) -> JobItem | None:
    try:
        with open(path) as f:
            raw = f.read()

        enabled = True

        fields, content = parse_frontmatter(raw)
        name = fields.get("name") or None
        schedule = fields.get("schedule") or None
        session_id = fields.get("session") or None
        model = fields.get("model") or None
        backend_id = fields.get("agent") or None
        consensus = str(fields.get("consensus", "false")).lower() not in ("false", "")
        max_tokens: int | None = None
        max_tokens_raw = fields.get("max-tokens") or fields.get("max_tokens")
        if max_tokens_raw is not None:
            try:
                max_tokens = max(1, int(max_tokens_raw))
            except (ValueError, TypeError):
                logger.warning(f"Job file {path}: invalid 'max-tokens' value {max_tokens_raw!r}, ignoring.")
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")

        if not enabled:
            logger.info(f"Job file {path}: disabled, skipping.")
            return None

        if schedule and not croniter.is_valid(schedule):
            logger.warning(f"Job file {path}: invalid cron expression '{schedule}', skipping.")
            return None

        filename = Path(path).stem
        name = name or filename
        if not session_id:
            # Generate a deterministic UUID from the agent name and filename
            session_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.{filename}"))

        return JobItem(path=path, name=name, schedule=schedule, session_id=session_id, content=content, model=model, backend_id=backend_id, consensus=consensus, max_tokens=max_tokens)

    except Exception as e:
        if agent_job_parse_errors_total is not None:
            agent_job_parse_errors_total.inc()
        logger.error(f"Job file {path}: failed to parse — {e}, skipping.")
        return None


async def _execute_job(item: JobItem, bus: MessageBus, semaphore: asyncio.Semaphore | None) -> None:
    """Fire the job once. Called from both the cron loop and run-once mode."""
    _semaphore_acquired = False
    if semaphore is not None:
        await semaphore.acquire()
        _semaphore_acquired = True

    item.running = True
    if agent_job_running_items is not None:
        agent_job_running_items.inc()
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
        logger.error(f"Job '{item.name}' checkpoint write failed: {e}")
    _send_task: asyncio.Task | None = None
    try:
        prompt = f"Job: {item.name}\n\n{item.content}"
        logger.info(f"Job '{item.name}' firing.")
        _job_start = time.monotonic()
        message = Message(prompt=prompt, session_id=item.session_id, kind=f"job:{item.name}", model=item.model, backend_id=item.backend_id, consensus=item.consensus, max_tokens=item.max_tokens)
        if agent_job_item_last_run_timestamp_seconds is not None:
            agent_job_item_last_run_timestamp_seconds.labels(name=item.name).set(time.time())
        _send_task = asyncio.ensure_future(bus.send(message))

        def _log_send_result(t: asyncio.Task, _name: str = item.name) -> None:
            exc = t.exception() if not t.cancelled() else None
            if exc is not None:
                logger.error(f"Job '{_name}' background bus.send failed: {exc}")

        _send_task.add_done_callback(_log_send_result)
        await asyncio.shield(_send_task)
        if agent_job_duration_seconds is not None:
            agent_job_duration_seconds.labels(name=item.name).observe(time.monotonic() - _job_start)
        if agent_job_runs_total is not None:
            agent_job_runs_total.labels(name=item.name, status="success").inc()
        if agent_job_item_last_success_timestamp_seconds is not None:
            agent_job_item_last_success_timestamp_seconds.labels(name=item.name).set(time.time())
    except asyncio.CancelledError:
        if _send_task is not None and not _send_task.done():
            logger.info(f"Job '{item.name}' cancelled — awaiting in-flight bus.send before clearing running flag.")
            await asyncio.gather(_send_task, return_exceptions=True)
        else:
            logger.info(f"Job '{item.name}' cancelled.")
        raise
    except Exception as e:
        logger.error(f"Job '{item.name}' error: {e}")
        if agent_job_runs_total is not None:
            agent_job_runs_total.labels(name=item.name, status="error").inc()
        if agent_job_error_duration_seconds is not None:
            agent_job_error_duration_seconds.labels(name=item.name).observe(time.monotonic() - _job_start)
        if agent_job_item_last_error_timestamp_seconds is not None:
            agent_job_item_last_error_timestamp_seconds.labels(name=item.name).set(time.time())
    finally:
        item.running = False
        if agent_job_running_items is not None:
            agent_job_running_items.dec()
        if semaphore is not None and _semaphore_acquired:
            semaphore.release()
        if checkpoint_path is not None:
            try:
                os.remove(checkpoint_path)
            except FileNotFoundError:
                pass
            except Exception as e:
                if agent_checkpoint_write_errors_total is not None:
                    agent_checkpoint_write_errors_total.inc()
                logger.warning(f"Job '{item.name}' checkpoint delete failed: {e}")


async def run_job(item: JobItem, bus: MessageBus, semaphore: asyncio.Semaphore | None = None, backends_ready: asyncio.Event | None = None) -> None:
    if backends_ready is not None:
        await backends_ready.wait()

    if item.schedule is None:
        # Run-once mode: fire once and exit.
        logger.info(f"Job '{item.name}' run-once: firing immediately.")
        await _execute_job(item, bus, semaphore)
        return

    cron = croniter(item.schedule, datetime.now(timezone.utc))
    while True:
        next_run = cron.get_next(datetime)
        now = datetime.now(timezone.utc)
        delay = (next_run - now).total_seconds()
        logger.info(f"Job '{item.name}' next run in {delay:.0f}s at {next_run.isoformat()}")
        await asyncio.sleep(delay)

        if agent_job_lag_seconds is not None:
            lag = (datetime.now(timezone.utc) - next_run).total_seconds()
            agent_job_lag_seconds.observe(lag)

        if item.running:
            logger.warning(f"Job '{item.name}' still running from previous turn, skipping.")
            if agent_job_skips_total is not None:
                agent_job_skips_total.labels(name=item.name).inc()
            continue

        await _execute_job(item, bus, semaphore)


class JobRunner:
    def __init__(self, bus: MessageBus, backends_ready: asyncio.Event | None = None):
        self._bus = bus
        self._backends_ready = backends_ready
        self._items: dict[str, JobItem] = {}
        self._semaphore: asyncio.Semaphore | None = (
            asyncio.Semaphore(_JOBS_MAX_CONCURRENT) if _JOBS_MAX_CONCURRENT > 0 else None
        )
        if self._semaphore is not None:
            logger.info(f"Job concurrency limit: {_JOBS_MAX_CONCURRENT} concurrent items")

    async def _register(self, path: str) -> None:
        item = parse_job_file(path)
        if not item:
            return
        cancelled = self._unregister(path)
        if cancelled is not None:
            await asyncio.gather(cancelled, return_exceptions=True)
        task = asyncio.create_task(run_job(item, self._bus, self._semaphore, self._backends_ready))

        def _task_done_callback(t: asyncio.Task, _name: str = item.name) -> None:
            if not t.cancelled() and t.exception() is not None:
                logger.error(f"Job '{_name}' task crashed: {t.exception()!r}")
                if agent_job_runs_total is not None:
                    agent_job_runs_total.labels(name=_name, status="error").inc()

        task.add_done_callback(_task_done_callback)
        item.task = task
        self._items[path] = item
        if agent_job_items_registered is not None:
            agent_job_items_registered.set(len(self._items))
        if item.schedule:
            logger.info(f"Job '{item.name}' registered. Schedule: {item.schedule}")
        else:
            logger.info(f"Job '{item.name}' registered. Mode: run-once")

    def items(self) -> list[dict]:
        """Return a serializable snapshot of currently registered job items."""
        result = []
        for item in self._items.values():
            result.append({
                "name": item.name,
                "schedule": item.schedule,
                "session_id": item.session_id,
                "backend_id": item.backend_id,
                "model": item.model,
                "consensus": item.consensus,
                "max_tokens": item.max_tokens,
                "running": item.running,
            })
        return result

    def _unregister(self, path: str) -> asyncio.Task | None:
        existing = self._items.pop(path, None)
        if existing and existing.task:
            if existing.running:
                logger.info(f"Job '{existing.name}' unregistered — cancelling while run is in progress.")
            else:
                logger.info(f"Job '{existing.name}' unregistered.")
            existing.task.cancel()
            if agent_job_items_registered is not None:
                agent_job_items_registered.set(len(self._items))
            return existing.task
        if agent_job_items_registered is not None:
            agent_job_items_registered.set(len(self._items))
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
                    logger.warning(f"Job '{name}': stale checkpoint at {cp_path} — run may have been interrupted")
                    if agent_job_checkpoint_stale_total is not None:
                        agent_job_checkpoint_stale_total.inc()
                    try:
                        os.remove(cp_path)
                    except Exception as rm_err:
                        logger.warning(f"Job '{name}': failed to remove stale checkpoint {cp_path}: {rm_err}")
        if not os.path.isdir(JOBS_DIR):
            return
        try:
            job_files = os.listdir(JOBS_DIR)
        except OSError:
            return
        for filename in job_files:
            if filename.endswith(".md"):
                await self._register(os.path.join(JOBS_DIR, filename))

    async def run(self) -> None:
        logger.info(f"Job runner watching {JOBS_DIR}")

        while True:
            if not os.path.isdir(JOBS_DIR):
                logger.info("Jobs directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            # Close the TOCTOU race: schedule _scan() as a concurrent task so
            # it runs after awatch() has entered its RustNotify context manager
            # (i.e. after the OS-level watch is registered). Any files added
            # between watch registration and scan completion are already tracked
            # by the watcher. _scan() + _register() are idempotent so duplicate
            # events from both the scan and the watcher are safe.
            _scan_task = asyncio.ensure_future(self._scan())

            def _scan_done(t: asyncio.Task) -> None:
                if not t.cancelled() and t.exception() is not None:
                    logger.error("Job runner _scan crashed: %r", t.exception())

            _scan_task.add_done_callback(_scan_done)
            async for changes in awatch(JOBS_DIR):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="jobs").inc()
                for _, path in changes:
                    if not path.endswith(".md"):
                        continue
                    if os.path.exists(path):
                        logger.info(f"Job file changed: {path}")
                        if agent_job_reloads_total is not None:
                            agent_job_reloads_total.inc()
                        await self._register(path)
                    else:
                        logger.info(f"Job file removed: {path}")
                        if agent_job_reloads_total is not None:
                            agent_job_reloads_total.inc()
                        self._unregister(path)

            logger.warning("Jobs directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="jobs").inc()
            cancelled = [t for path in list(self._items.keys()) if (t := self._unregister(path)) is not None]
            if cancelled:
                await asyncio.gather(*cancelled, return_exceptions=True)
            await asyncio.sleep(10)

import asyncio
import json
import logging
import os
import re
import time
import uuid
from dataclasses import dataclass, field
from datetime import date, datetime, time as dtime, timedelta, timezone
from pathlib import Path
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from bus import Message, MessageBus
from croniter import croniter
from metrics import (
    agent_sched_task_checkpoint_stale_total,
    agent_sched_task_duration_seconds,
    agent_sched_task_error_duration_seconds,
    agent_sched_task_item_last_error_timestamp_seconds,
    agent_sched_task_item_last_run_timestamp_seconds,
    agent_sched_task_item_last_success_timestamp_seconds,
    agent_sched_task_items_registered,
    agent_sched_task_lag_seconds,
    agent_sched_task_parse_errors_total,
    agent_sched_task_reloads_total,
    agent_sched_task_running_items,
    agent_sched_task_runs_total,
    agent_sched_task_skips_total,
    agent_checkpoint_write_errors_total,
    agent_file_watcher_restarts_total,
    agent_watcher_events_total,
)
from utils import parse_frontmatter, parse_duration
from watchfiles import awatch

logger = logging.getLogger(__name__)

TASKS_DIR = os.environ.get("TASKS_DIR", "/home/agent/.nyx/tasks")
CHECKPOINT_DIR = os.path.join(TASKS_DIR, ".checkpoints")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")
_TASKS_MAX_CONCURRENT = int(os.environ.get("TASKS_MAX_CONCURRENT", "0"))

_DAY_ABBREVS = {
    "sun": "0", "mon": "1", "tue": "2", "wed": "3",
    "thu": "4", "fri": "5", "sat": "6",
}


def _translate_day_abbrevs(expr: str) -> str:
    """Replace three-letter day abbreviations with cron numeric equivalents."""
    def _replace(m):
        return _DAY_ABBREVS.get(m.group(0).lower(), m.group(0))
    return re.sub(r"\b(Sun|Mon|Tue|Wed|Thu|Fri|Sat)\b", _replace, expr, flags=re.IGNORECASE)


@dataclass
class TaskItem:
    path: str
    name: str
    days_expr: str
    tz: ZoneInfo
    window_start: dtime | None        # None = run-once mode (fire immediately, no schedule)
    window_end: dtime | None          # close time derived from window_start + window_duration
    loop: bool
    loop_gap: float | None            # seconds
    done_when: str | None
    content: str
    start: date | None = None
    end: date | None = None
    model: str | None = None
    backend_id: str | None = None
    task: asyncio.Task | None = field(default=None, compare=False)
    running: bool = False


def parse_task_file(path: str) -> TaskItem | None:
    try:
        with open(path) as f:
            raw = f.read()

        fields, content = parse_frontmatter(raw)

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
        if not enabled:
            logger.info(f"Task file {path}: disabled, skipping.")
            return None

        # name
        filename = Path(path).stem
        name = fields.get("name") or filename

        # timezone
        tz_str = fields.get("timezone") or "UTC"
        try:
            tz = ZoneInfo(tz_str)
        except ZoneInfoNotFoundError:
            logger.warning(f"Task file {path}: unknown timezone {tz_str!r}, skipping.")
            if agent_sched_task_parse_errors_total is not None:
                agent_sched_task_parse_errors_total.inc()
            return None

        # days
        days_raw = str(fields.get("days") or "*").strip()
        days_expr = _translate_day_abbrevs(days_raw)
        if not croniter.is_valid(f"0 0 * * {days_expr}"):
            logger.warning(f"Task file {path}: invalid days expression {days_raw!r}, skipping.")
            if agent_sched_task_parse_errors_total is not None:
                agent_sched_task_parse_errors_total.inc()
            return None

        # window-start (optional — omitting enables run-once mode)
        ws_raw = fields.get("window-start") or fields.get("window_start")
        window_start: dtime | None = None
        if ws_raw:
            try:
                h, m = str(ws_raw).split(":")
                window_start = dtime(int(h), int(m), tzinfo=None)
            except Exception:
                logger.warning(f"Task file {path}: invalid 'window-start' {ws_raw!r}, skipping.")
                if agent_sched_task_parse_errors_total is not None:
                    agent_sched_task_parse_errors_total.inc()
                return None

        # window-duration
        wd_raw = fields.get("window-duration") or fields.get("window_duration")
        window_end: dtime | None = None

        if wd_raw:
            try:
                duration_secs = parse_duration(str(wd_raw))
            except ValueError as e:
                logger.warning(f"Task file {path}: invalid 'window-duration': {e}, skipping.")
                if agent_sched_task_parse_errors_total is not None:
                    agent_sched_task_parse_errors_total.inc()
                return None
            ws_dt = datetime.combine(date.today(), window_start)
            we_dt = ws_dt + timedelta(seconds=duration_secs)
            window_end = we_dt.time()

        # loop
        loop = str(fields.get("loop", "false")).lower() not in ("false", "")
        if loop and window_end is None:
            logger.warning(f"Task file {path}: 'loop: true' requires 'window-duration' — disabling loop.")
            loop = False

        # loop-gap
        loop_gap: float | None = None
        lg_raw = fields.get("loop-gap") or fields.get("loop_gap")
        if lg_raw:
            try:
                loop_gap = parse_duration(str(lg_raw))
            except ValueError as e:
                logger.warning(f"Task file {path}: invalid 'loop-gap': {e}, ignoring.")

        # done-when
        done_when = fields.get("done-when") or fields.get("done_when") or None

        # start / end dates
        start: date | None = None
        end: date | None = None
        start_raw = fields.get("start")
        end_raw = fields.get("end")
        if start_raw:
            try:
                start = date.fromisoformat(str(start_raw))
            except ValueError:
                logger.warning(f"Task file {path}: invalid 'start' date {start_raw!r}, ignoring.")
        if end_raw:
            try:
                end = date.fromisoformat(str(end_raw))
            except ValueError:
                logger.warning(f"Task file {path}: invalid 'end' date {end_raw!r}, ignoring.")

        # model
        model = fields.get("model") or None

        # backend
        backend_id = fields.get("agent") or None

        return TaskItem(
            path=path,
            name=name,
            days_expr=days_expr,
            tz=tz,
            window_start=window_start,
            window_end=window_end,
            loop=loop,
            loop_gap=loop_gap,
            done_when=done_when,
            content=content,
            start=start,
            end=end,
            model=model,
            backend_id=backend_id,
        )

    except Exception as e:
        if agent_sched_task_parse_errors_total is not None:
            agent_sched_task_parse_errors_total.inc()
        logger.error(f"Task file {path}: failed to parse — {e}, skipping.")
        return None


def _day_session_id(item: TaskItem, today: date) -> str:
    """Deterministic session ID scoped to agent + task filename + calendar date."""
    filename = Path(item.path).stem
    return str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.{filename}.{today.isoformat()}"))


def _day_matches(item: TaskItem, d: date) -> bool:
    """Return True if date d matches the task's days expression."""
    # croniter interprets weekday 0 as Sunday; Python's weekday() is 0=Monday.
    # Use a dummy cron expression pinned to 00:00 on day d and check if it fires.
    cron_expr = f"0 0 * * {item.days_expr}"
    # Check whether croniter considers midnight on d as a match by finding the
    # previous fire before d+1 and seeing if it equals d.
    dt_start = datetime.combine(d, dtime(0, 0))
    c = croniter(cron_expr, dt_start - timedelta(seconds=1))
    next_fire = c.get_next(datetime)
    return next_fire.date() == d


def _now_in_tz(tz: ZoneInfo) -> datetime:
    return datetime.now(tz)


def _next_window_open(item: TaskItem, after: datetime) -> datetime | None:
    """Return the next datetime (in item.tz) when the task window opens, starting after `after`.

    Returns None if no future eligible day exists.
    """
    tz = item.tz
    # Normalise `after` to item's timezone
    after_local = after.astimezone(tz)
    # Start checking from the next minute to avoid re-triggering the same moment
    check_date = after_local.date()
    # If we're past window_start today, start from tomorrow
    if after_local.time() >= item.window_start:
        check_date += timedelta(days=1)

    for _ in range(366 * 2):  # guard against infinite loop — max 2 years
        if item.start and check_date < item.start:
            check_date += timedelta(days=1)
            continue
        if item.end and check_date > item.end:
            return None
        if _day_matches(item, check_date):
            return datetime.combine(check_date, item.window_start, tzinfo=tz)
        check_date += timedelta(days=1)

    return None


def _inside_window(item: TaskItem) -> bool:
    """Return True if current time is inside the task's daily window on an eligible day."""
    now = _now_in_tz(item.tz)
    today = now.date()
    if item.start and today < item.start:
        return False
    if item.end and today > item.end:
        return False
    if not _day_matches(item, today):
        return False
    if now.time() < item.window_start:
        return False
    if item.window_end and now.time() >= item.window_end:
        return False
    return True


async def run_task(item: TaskItem, bus: MessageBus, semaphore: asyncio.Semaphore | None = None) -> None:
    filename = Path(item.path).stem
    checkpoint_path = os.path.join(CHECKPOINT_DIR, filename + ".running.json")

    # --- Run-once mode: no window-start, fire immediately and exit ---
    if item.window_start is None:
        logger.info(f"Task '{item.name}' run-once: firing immediately.")
        _semaphore_acquired = False
        if semaphore is not None:
            await semaphore.acquire()
            _semaphore_acquired = True
        item.running = True
        if agent_sched_task_running_items is not None:
            agent_sched_task_running_items.inc()
        session_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.{filename}"))
        try:
            prompt = f"Task: {item.name}\n\n{item.content}"
            _task_start = time.monotonic()
            if agent_sched_task_item_last_run_timestamp_seconds is not None:
                agent_sched_task_item_last_run_timestamp_seconds.labels(name=item.name).set(time.time())
            message = Message(prompt=prompt, session_id=session_id, kind=f"task:{item.name}", model=item.model, backend_id=item.backend_id)
            _send_task = asyncio.ensure_future(bus.send(message))

            def _log_send_result(t: asyncio.Task, _name: str = item.name) -> None:
                exc = t.exception() if not t.cancelled() else None
                if exc is not None:
                    logger.error(f"Task '{_name}' background bus.send failed: {exc}")

            _send_task.add_done_callback(_log_send_result)
            await asyncio.shield(_send_task)
            if agent_sched_task_duration_seconds is not None:
                agent_sched_task_duration_seconds.labels(name=item.name).observe(time.monotonic() - _task_start)
            if agent_sched_task_runs_total is not None:
                agent_sched_task_runs_total.labels(name=item.name, status="success").inc()
            if agent_sched_task_item_last_success_timestamp_seconds is not None:
                agent_sched_task_item_last_success_timestamp_seconds.labels(name=item.name).set(time.time())
        except asyncio.CancelledError:
            raise
        except Exception as e:
            logger.error(f"Task '{item.name}' error: {e}")
            if agent_sched_task_runs_total is not None:
                agent_sched_task_runs_total.labels(name=item.name, status="error").inc()
            if agent_sched_task_error_duration_seconds is not None:
                agent_sched_task_error_duration_seconds.labels(name=item.name).observe(time.monotonic() - _task_start)
            if agent_sched_task_item_last_error_timestamp_seconds is not None:
                agent_sched_task_item_last_error_timestamp_seconds.labels(name=item.name).set(time.time())
        finally:
            item.running = False
            if agent_sched_task_running_items is not None:
                agent_sched_task_running_items.dec()
            if semaphore is not None and _semaphore_acquired:
                semaphore.release()
        return

    # --- Startup: handle restart-within-window logic ---
    stale_checkpoint = os.path.exists(checkpoint_path)
    if stale_checkpoint:
        if _inside_window(item):
            logger.info(f"Task '{item.name}': stale checkpoint found — firing immediately (interrupted run).")
            if agent_sched_task_checkpoint_stale_total is not None:
                agent_sched_task_checkpoint_stale_total.inc()
            # Remove checkpoint before re-firing so we don't double-count on next restart
            try:
                os.remove(checkpoint_path)
            except Exception:
                pass
            # Fall through to fire immediately below by setting a flag
            _fire_now = True
        else:
            logger.warning(f"Task '{item.name}': stale checkpoint found but outside window — removing and skipping to next window.")
            if agent_sched_task_checkpoint_stale_total is not None:
                agent_sched_task_checkpoint_stale_total.inc()
            try:
                os.remove(checkpoint_path)
            except Exception:
                pass
            _fire_now = False
    else:
        # No checkpoint: if currently inside window, run already completed cleanly — skip to next day
        _fire_now = False

    while True:
        if not _fire_now:
            now = _now_in_tz(item.tz)
            next_open = _next_window_open(item, now)
            if next_open is None:
                logger.info(f"Task '{item.name}': no future eligible days — task has expired.")
                return
            delay = (next_open.astimezone(timezone.utc) - datetime.now(timezone.utc)).total_seconds()
            logger.info(f"Task '{item.name}' next window opens in {delay:.0f}s at {next_open.isoformat()}")
            await asyncio.sleep(max(delay, 0))

        _fire_now = False  # only fires once from restart path

        # Skip if already running
        if item.running:
            logger.warning(f"Task '{item.name}' still running from previous window, skipping.")
            if agent_sched_task_skips_total is not None:
                agent_sched_task_skips_total.labels(name=item.name).inc()
            continue

        _semaphore_acquired = False
        if semaphore is not None:
            await semaphore.acquire()
            _semaphore_acquired = True

        item.running = True
        if agent_sched_task_running_items is not None:
            agent_sched_task_running_items.inc()

        # Generate per-day session ID
        today = _now_in_tz(item.tz).date()
        session_id = _day_session_id(item, today)

        # Write checkpoint
        try:
            os.makedirs(CHECKPOINT_DIR, exist_ok=True)
            with open(checkpoint_path, "w") as f:
                json.dump(
                    {
                        "started_at": datetime.now(timezone.utc).isoformat(),
                        "name": item.name,
                        "session_id": session_id,
                    },
                    f,
                )
        except Exception as e:
            if agent_checkpoint_write_errors_total is not None:
                agent_checkpoint_write_errors_total.inc()
            logger.error(f"Task '{item.name}' checkpoint write failed: {e}")

        try:
            while True:
                # Record lag
                scheduled_open = datetime.combine(_now_in_tz(item.tz).date(), item.window_start, tzinfo=item.tz)
                lag = max(0.0, (_now_in_tz(item.tz) - scheduled_open).total_seconds())
                if agent_sched_task_lag_seconds is not None:
                    agent_sched_task_lag_seconds.observe(lag)
                if agent_sched_task_item_last_run_timestamp_seconds is not None:
                    agent_sched_task_item_last_run_timestamp_seconds.labels(name=item.name).set(time.time())

                prompt = f"Task: {item.name}\n\n{item.content}"
                logger.info(f"Task '{item.name}' firing (session={session_id}).")
                _task_start = time.monotonic()

                _send_task: asyncio.Task | None = None
                response = ""
                try:
                    message = Message(
                        prompt=prompt,
                        session_id=session_id,
                        kind=f"task:{item.name}",
                        model=item.model,
                        backend_id=item.backend_id,
                    )
                    _send_task = asyncio.ensure_future(bus.send(message))

                    def _log_send_result(t: asyncio.Task, _name: str = item.name) -> None:
                        exc = t.exception() if not t.cancelled() else None
                        if exc is not None:
                            logger.error(f"Task '{_name}' background bus.send failed: {exc}")

                    _send_task.add_done_callback(_log_send_result)
                    response = await asyncio.shield(_send_task)

                    if agent_sched_task_duration_seconds is not None:
                        agent_sched_task_duration_seconds.labels(name=item.name).observe(time.monotonic() - _task_start)
                    if agent_sched_task_runs_total is not None:
                        agent_sched_task_runs_total.labels(name=item.name, status="success").inc()
                    if agent_sched_task_item_last_success_timestamp_seconds is not None:
                        agent_sched_task_item_last_success_timestamp_seconds.labels(name=item.name).set(time.time())

                except asyncio.CancelledError:
                    if _send_task is not None and not _send_task.done():
                        logger.info(f"Task '{item.name}' cancelled — awaiting in-flight bus.send.")
                        await asyncio.gather(_send_task, return_exceptions=True)
                    raise
                except Exception as e:
                    logger.error(f"Task '{item.name}' error: {e}")
                    if agent_sched_task_runs_total is not None:
                        agent_sched_task_runs_total.labels(name=item.name, status="error").inc()
                    if agent_sched_task_error_duration_seconds is not None:
                        agent_sched_task_error_duration_seconds.labels(name=item.name).observe(time.monotonic() - _task_start)
                    if agent_sched_task_item_last_error_timestamp_seconds is not None:
                        agent_sched_task_item_last_error_timestamp_seconds.labels(name=item.name).set(time.time())
                    break  # stop looping on error; go back to waiting for next window

                # Loop logic
                if not item.loop or item.window_end is None:
                    break  # fire once, then wait for next window

                # done-when check
                if item.done_when and response and item.done_when in response:
                    logger.info(f"Task '{item.name}': done-when signal received — stopping for the day.")
                    break

                # window boundary check
                now_local = _now_in_tz(item.tz)
                if now_local.time() >= item.window_end:
                    logger.info(f"Task '{item.name}': window closed — stopping for the day.")
                    break

                # loop-gap
                if item.loop_gap:
                    logger.info(f"Task '{item.name}': waiting {item.loop_gap:.0f}s before next iteration.")
                    await asyncio.sleep(item.loop_gap)

                # Re-check window after gap (gap may have pushed past window_end)
                now_local = _now_in_tz(item.tz)
                if item.window_end and now_local.time() >= item.window_end:
                    logger.info(f"Task '{item.name}': window closed after loop-gap — stopping for the day.")
                    break

                # Refresh session ID in case day rolled over during a long gap
                new_today = _now_in_tz(item.tz).date()
                if new_today != today:
                    today = new_today
                    session_id = _day_session_id(item, today)

        except asyncio.CancelledError:
            raise
        finally:
            item.running = False
            if agent_sched_task_running_items is not None:
                agent_sched_task_running_items.dec()
            if semaphore is not None and _semaphore_acquired:
                semaphore.release()
            try:
                os.remove(checkpoint_path)
            except FileNotFoundError:
                pass
            except Exception as e:
                if agent_checkpoint_write_errors_total is not None:
                    agent_checkpoint_write_errors_total.inc()
                logger.warning(f"Task '{item.name}' checkpoint delete failed: {e}")


class TaskRunner:
    def __init__(self, bus: MessageBus):
        self._bus = bus
        self._items: dict[str, TaskItem] = {}
        self._semaphore: asyncio.Semaphore | None = (
            asyncio.Semaphore(_TASKS_MAX_CONCURRENT) if _TASKS_MAX_CONCURRENT > 0 else None
        )
        if self._semaphore is not None:
            logger.info(f"Task concurrency limit: {_TASKS_MAX_CONCURRENT} concurrent items")

    async def _register(self, path: str) -> None:
        item = parse_task_file(path)
        if not item:
            return
        cancelled = self._unregister(path)
        if cancelled is not None:
            await asyncio.gather(cancelled, return_exceptions=True)
        task = asyncio.create_task(run_task(item, self._bus, self._semaphore))

        def _task_done_callback(t: asyncio.Task, _name: str = item.name) -> None:
            if not t.cancelled() and t.exception() is not None:
                logger.error(f"Task '{_name}' coroutine crashed: {t.exception()!r}")
                if agent_sched_task_runs_total is not None:
                    agent_sched_task_runs_total.labels(name=_name, status="error").inc()

        task.add_done_callback(_task_done_callback)
        item.task = task
        self._items[path] = item
        if agent_sched_task_items_registered is not None:
            agent_sched_task_items_registered.set(len(self._items))
        if item.window_start is not None:
            logger.info(f"Task '{item.name}' registered. Window: {item.window_start.strftime('%H:%M')}")
        else:
            logger.info(f"Task '{item.name}' registered. Mode: run-once")

    def _unregister(self, path: str) -> asyncio.Task | None:
        existing = self._items.pop(path, None)
        if existing and existing.task:
            if existing.running:
                logger.info(f"Task '{existing.name}' unregistered — cancelling while run is in progress.")
            else:
                logger.info(f"Task '{existing.name}' unregistered.")
            existing.task.cancel()
            if agent_sched_task_items_registered is not None:
                agent_sched_task_items_registered.set(len(self._items))
            return existing.task
        if agent_sched_task_items_registered is not None:
            agent_sched_task_items_registered.set(len(self._items))
        return None

    async def _scan(self) -> None:
        if not os.path.isdir(TASKS_DIR):
            return
        try:
            task_files = os.listdir(TASKS_DIR)
        except OSError:
            return
        for filename in task_files:
            if filename.endswith(".md"):
                await self._register(os.path.join(TASKS_DIR, filename))

    async def run(self) -> None:
        logger.info(f"Task runner watching {TASKS_DIR}")

        while True:
            if not os.path.isdir(TASKS_DIR):
                logger.info("Tasks directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            asyncio.ensure_future(self._scan())
            async for changes in awatch(TASKS_DIR):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="tasks").inc()
                for _, path in changes:
                    if not path.endswith(".md"):
                        continue
                    if os.path.exists(path):
                        logger.info(f"Task file changed: {path}")
                        if agent_sched_task_reloads_total is not None:
                            agent_sched_task_reloads_total.inc()
                        await self._register(path)
                    else:
                        logger.info(f"Task file removed: {path}")
                        if agent_sched_task_reloads_total is not None:
                            agent_sched_task_reloads_total.inc()
                        self._unregister(path)

            logger.warning("Tasks directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="tasks").inc()
            cancelled = [t for path in list(self._items.keys()) if (t := self._unregister(path)) is not None]
            if cancelled:
                await asyncio.gather(*cancelled, return_exceptions=True)
            await asyncio.sleep(10)

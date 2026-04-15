import asyncio
import logging
import os
import time
import uuid
from datetime import datetime, timezone

from bus import Message, MessageBus
from croniter import croniter
from metrics import (
    agent_file_watcher_restarts_total,
    agent_heartbeat_duration_seconds,
    agent_heartbeat_error_duration_seconds,
    agent_heartbeat_lag_seconds,
    agent_heartbeat_last_error_timestamp_seconds,
    agent_heartbeat_last_run_timestamp_seconds,
    agent_heartbeat_last_success_timestamp_seconds,
    agent_heartbeat_load_errors_total,
    agent_heartbeat_reloads_total,
    agent_heartbeat_runs_total,
    agent_heartbeat_skips_total,
    agent_watcher_events_total,
)
from utils import ConsensusEntry, parse_consensus, parse_frontmatter, parse_frontmatter_raw
from watchfiles import awatch

logger = logging.getLogger(__name__)

HEARTBEAT_PATH = os.environ.get("HEARTBEAT_PATH", "/home/agent/.nyx/HEARTBEAT.md")
DEFAULT_SCHEDULE = "*/30 * * * *"
HEARTBEAT_DIR = os.path.dirname(HEARTBEAT_PATH)
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx")
HEARTBEAT_SESSION = str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.heartbeat"))
# Sentinel token: heartbeat prompts should include this string to suppress response logging.
HEARTBEAT_OK = "HEARTBEAT_OK"


def load_heartbeat() -> tuple[str, str, str | None, str | None, bool, int | None] | None:
    if not os.path.exists(HEARTBEAT_PATH):
        return None
    with open(HEARTBEAT_PATH) as f:
        raw = f.read()

    schedule = DEFAULT_SCHEDULE
    enabled = True

    fields, content = parse_frontmatter(raw)
    raw_fields, _ = parse_frontmatter_raw(raw)
    if "schedule" in fields:
        schedule = fields["schedule"]
    if "enabled" in fields:
        enabled = str(fields["enabled"]).lower() not in ("false", "")
    model = fields.get("model") or None
    backend_id = fields.get("agent") or None
    consensus = parse_consensus(raw_fields.get("consensus"))
    max_tokens: int | None = None
    max_tokens_raw = fields.get("max-tokens") or fields.get("max_tokens")
    if max_tokens_raw is not None:
        try:
            max_tokens = max(1, int(max_tokens_raw))
        except (ValueError, TypeError):
            logger.warning(f"HEARTBEAT.md: invalid 'max-tokens' value {max_tokens_raw!r}, ignoring.")

    if not enabled:
        return None

    if not content:
        return None

    if not croniter.is_valid(schedule):
        logger.warning(f"HEARTBEAT.md has invalid cron expression '{schedule}', using default.")
        schedule = DEFAULT_SCHEDULE

    return schedule, content, model, backend_id, consensus, max_tokens


async def _run_loop(
    bus: MessageBus,
    schedule: str,
    content: str,
    stop_event: asyncio.Event,
    model: str | None = None,
    backend_id: str | None = None,
    consensus: list[ConsensusEntry] | None = None,
    max_tokens: int | None = None,
) -> None:
    cron = croniter(schedule, datetime.now(timezone.utc))
    stop_waiter = asyncio.create_task(stop_event.wait())
    try:
        while not stop_event.is_set():
            next_run = cron.get_next(datetime)
            now = datetime.now(timezone.utc)
            delay = (next_run - now).total_seconds()
            logger.info(f"Heartbeat next run in {delay:.0f}s at {next_run.isoformat()}")
            try:
                await asyncio.wait_for(asyncio.shield(stop_waiter), timeout=delay)
                return  # stop_event fired — exit loop so caller can restart with new config
            except asyncio.TimeoutError:
                pass

            if agent_heartbeat_lag_seconds is not None:
                lag = (datetime.now(timezone.utc) - next_run).total_seconds()
                agent_heartbeat_lag_seconds.observe(lag)

            if stop_event.is_set():
                return

            # Reload content (schedule changes are handled by the watcher, but content may change)
            try:
                loaded = load_heartbeat()
            except Exception as e:
                if agent_heartbeat_load_errors_total is not None:
                    agent_heartbeat_load_errors_total.inc()
                logger.warning(f"Heartbeat reload error — skipping this run: {e}")
                continue
            if not loaded:
                logger.info("Heartbeat skipped — HEARTBEAT.md disabled or empty.")
                continue
            _, content, model, backend_id, consensus, max_tokens = loaded

            prompt = f"Heartbeat check. Follow these instructions:\n\n{content}"
            _hb_start = time.monotonic()
            message = Message(prompt=prompt, session_id=HEARTBEAT_SESSION, kind="heartbeat", model=model, backend_id=backend_id, consensus=consensus, max_tokens=max_tokens)
            if not bus.try_send(message):
                logger.info("Heartbeat skipped — previous heartbeat still pending.")
                if agent_heartbeat_skips_total is not None:
                    agent_heartbeat_skips_total.inc()
                continue
            if message.result is not None:
                logger.info("Heartbeat firing.")
                if agent_heartbeat_last_run_timestamp_seconds is not None:
                    agent_heartbeat_last_run_timestamp_seconds.set(time.time())
                try:
                    response = await message.result
                    if agent_heartbeat_duration_seconds is not None:
                        agent_heartbeat_duration_seconds.observe(time.monotonic() - _hb_start)
                    if response and HEARTBEAT_OK not in response:
                        logger.info(f"Heartbeat response: {response}")
                    if agent_heartbeat_runs_total is not None:
                        agent_heartbeat_runs_total.labels(status="success").inc()
                    if agent_heartbeat_last_success_timestamp_seconds is not None:
                        agent_heartbeat_last_success_timestamp_seconds.set(time.time())
                except Exception as e:
                    logger.error(f"Heartbeat executor error: {e}")
                    if agent_heartbeat_runs_total is not None:
                        agent_heartbeat_runs_total.labels(status="error").inc()
                    if agent_heartbeat_error_duration_seconds is not None:
                        agent_heartbeat_error_duration_seconds.observe(time.monotonic() - _hb_start)
                    if agent_heartbeat_last_error_timestamp_seconds is not None:
                        agent_heartbeat_last_error_timestamp_seconds.set(time.time())
    finally:
        stop_waiter.cancel()


def _loop_task_done_callback(t: asyncio.Task) -> None:
    """Log and count unexpected exceptions from a _run_loop task."""
    if not t.cancelled() and t.exception() is not None:
        logger.error(f"Heartbeat loop_task crashed: {t.exception()!r}")
        if agent_heartbeat_runs_total is not None:
            agent_heartbeat_runs_total.labels(status="error").inc()


async def heartbeat_runner(bus: MessageBus) -> None:
    try:
        loaded = load_heartbeat()
    except Exception as e:
        if agent_heartbeat_load_errors_total is not None:
            agent_heartbeat_load_errors_total.inc()
        logger.warning(f"Heartbeat load error at startup — treating as disabled: {e}")
        loaded = None
    if not loaded:
        logger.info("Heartbeat idle — HEARTBEAT.md not found, disabled, or empty.")
    else:
        schedule, content, model, backend_id, consensus, max_tokens = loaded
        logger.info(f"Heartbeat runner started. Schedule: {schedule}")

    stop_event = asyncio.Event()
    loop_task: asyncio.Task | None = None

    if loaded:
        loop_task = asyncio.create_task(_run_loop(bus, schedule, content, stop_event, model=model, backend_id=backend_id, consensus=consensus, max_tokens=max_tokens))
        loop_task.add_done_callback(_loop_task_done_callback)

    while True:
        if not os.path.isdir(HEARTBEAT_DIR):
            logger.info("Heartbeat directory not found — retrying in 10s.")
            await asyncio.sleep(10)
            continue

        async for changes in awatch(HEARTBEAT_DIR):
            if agent_watcher_events_total is not None:
                agent_watcher_events_total.labels(watcher="heartbeat").inc()
            for _, path in changes:
                if not path.endswith("HEARTBEAT.md"):
                    continue

                logger.info("HEARTBEAT.md changed — reloading.")
                if agent_heartbeat_reloads_total is not None:
                    agent_heartbeat_reloads_total.inc()
                if loop_task and not loop_task.done():
                    stop_event.set()
                    await loop_task
                    stop_event.clear()

                try:
                    loaded = load_heartbeat()
                except Exception as e:
                    if agent_heartbeat_load_errors_total is not None:
                        agent_heartbeat_load_errors_total.inc()
                    logger.warning(f"Heartbeat reload error after file change — skipping: {e}")
                    loop_task = None
                    continue
                if not loaded:
                    logger.info("Heartbeat disabled or empty after reload.")
                    loop_task = None
                else:
                    schedule, content, model, backend_id, consensus, max_tokens = loaded
                    logger.info(f"Heartbeat reloaded. Schedule: {schedule}")
                    loop_task = asyncio.create_task(_run_loop(bus, schedule, content, stop_event, model=model, backend_id=backend_id, consensus=consensus, max_tokens=max_tokens))
                    loop_task.add_done_callback(_loop_task_done_callback)

        logger.warning("Heartbeat directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
        if agent_file_watcher_restarts_total is not None:
            agent_file_watcher_restarts_total.labels(watcher="heartbeat").inc()
        if loop_task and not loop_task.done():
            stop_event.set()
            await loop_task
        stop_event.clear()
        try:
            loaded = load_heartbeat()
        except Exception as e:
            if agent_heartbeat_load_errors_total is not None:
                agent_heartbeat_load_errors_total.inc()
            logger.warning(f"Heartbeat reload error after watcher restart — skipping: {e}")
            loaded = None
        if loaded:
            schedule, content, model, backend_id, consensus, max_tokens = loaded
            loop_task = asyncio.create_task(_run_loop(bus, schedule, content, stop_event, model=model, backend_id=backend_id, consensus=consensus, max_tokens=max_tokens))
            loop_task.add_done_callback(_loop_task_done_callback)
        else:
            loop_task = None
            logger.info("Heartbeat disabled or empty after watcher restart.")
        await asyncio.sleep(10)

import asyncio
import fcntl
import json
import logging
import os
import time
import uuid
from collections import OrderedDict
from datetime import datetime, timezone
from logging.handlers import RotatingFileHandler

from a2a.server.agent_execution import AgentExecutor as A2AAgentExecutor
from a2a.server.agent_execution import RequestContext
from a2a.server.events import EventQueue
from a2a.utils import new_agent_text_message
from google import genai
from google.genai import types
from metrics import (
    a2_a2a_last_request_timestamp_seconds,
    a2_a2a_request_duration_seconds,
    a2_a2a_requests_total,
    a2_active_sessions,
    a2_concurrent_queries,
    a2_empty_responses_total,
    a2_log_bytes_total,
    a2_log_entries_total,
    a2_log_write_errors_total,
    a2_lru_cache_utilization_percent,
    a2_model_requests_total,
    a2_prompt_length_bytes,
    a2_response_length_bytes,
    a2_running_tasks,
    a2_sdk_messages_per_query,
    a2_sdk_query_duration_seconds,
    a2_sdk_query_error_duration_seconds,
    a2_sdk_session_duration_seconds,
    a2_sdk_time_to_first_message_seconds,
    a2_sdk_turns_per_query,
    a2_session_age_seconds,
    a2_session_evictions_total,
    a2_session_idle_seconds,
    a2_session_starts_total,
    a2_task_cancellations_total,
    a2_task_duration_seconds,
    a2_task_error_duration_seconds,
    a2_task_last_error_timestamp_seconds,
    a2_task_last_success_timestamp_seconds,
    a2_task_timeout_headroom_seconds,
    a2_tasks_total,
    a2_text_blocks_per_query,
)

logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "a2-gemini")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "gemini")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
AGENT_MD = os.environ.get("AGENT_MD", "/home/agent/agent.md")
SESSION_STORE_DIR = os.environ.get("SESSION_STORE_DIR", "/home/agent/memory/sessions")

MAX_LOG_BYTES = int(os.environ.get("MAX_LOG_BYTES", str(10 * 1024 * 1024)))
MAX_LOG_BACKUP_COUNT = int(os.environ.get("MAX_LOG_BACKUP_COUNT", "1"))
MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))

GEMINI_MODEL = os.environ.get("GEMINI_MODEL") or "gemini-2.5-pro"
GEMINI_API_KEY: str | None = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY") or None

_BACKEND_ID = "gemini"
_LABELS = {"agent": AGENT_OWNER, "agent_id": AGENT_ID, "backend": _BACKEND_ID}


def _load_agent_md() -> str:
    try:
        with open(AGENT_MD) as f:
            return f.read()
    except OSError:
        return ""


def _append_log(path: str, line: str) -> None:
    """Append a single line to a log file using fcntl locking for multi-process safety."""
    log_dir = os.path.dirname(path)
    if log_dir:
        os.makedirs(log_dir, exist_ok=True)
    with open(path, "a", encoding="utf-8") as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        try:
            f.write(line + "\n")
        finally:
            fcntl.flock(f, fcntl.LOCK_UN)


async def log_entry(role: str, text: str, session_id: str, model: str | None = None) -> None:
    try:
        entry = {
            "ts": datetime.now(timezone.utc).isoformat(),
            "agent": AGENT_NAME,
            "session_id": session_id,
            "role": role,
            "model": model,
            "text": text,
        }
        _line = json.dumps(entry)
        await asyncio.to_thread(_append_log, CONVERSATION_LOG, _line)
        if a2_log_entries_total is not None:
            a2_log_entries_total.labels(**_LABELS, logger="conversation").inc()
        if a2_log_bytes_total is not None:
            a2_log_bytes_total.labels(**_LABELS, logger="conversation").inc(len(_line.encode()))
    except Exception as e:
        if a2_log_write_errors_total is not None:
            a2_log_write_errors_total.labels(**_LABELS).inc()
        logger.error(f"log_entry error: {e}")


async def log_trace(text: str) -> None:
    try:
        await asyncio.to_thread(_append_log, TRACE_LOG, text)
        if a2_log_entries_total is not None:
            a2_log_entries_total.labels(**_LABELS, logger="trace").inc()
        if a2_log_bytes_total is not None:
            a2_log_bytes_total.labels(**_LABELS, logger="trace").inc(len(text.encode()))
    except Exception as e:
        if a2_log_write_errors_total is not None:
            a2_log_write_errors_total.labels(**_LABELS).inc()
        logger.error(f"log_trace error: {e}")


def _session_path(session_id: str) -> str:
    os.makedirs(SESSION_STORE_DIR, exist_ok=True)
    return os.path.join(SESSION_STORE_DIR, f"{session_id}.json")


def _load_history(session_id: str) -> list[types.Content]:
    """Load persisted conversation history for a session, or return empty list."""
    path = _session_path(session_id)
    if not os.path.exists(path):
        return []
    try:
        with open(path) as f:
            raw = json.load(f)
        history: list[types.Content] = []
        for entry in raw:
            parts = [types.Part(**p) for p in entry.get("parts", []) if p]
            if parts:
                history.append(types.Content(role=entry["role"], parts=parts))
        return history
    except Exception as e:
        logger.warning(f"Failed to load session history for {session_id!r}: {e}")
        return []


def _save_history(session_id: str, history: list[types.Content]) -> None:
    """Persist conversation history for a session."""
    path = _session_path(session_id)
    try:
        raw = []
        for content in history:
            parts = [p.model_dump(exclude_none=True) for p in (content.parts or []) if p]
            if parts:
                raw.append({"role": content.role, "parts": parts})
        tmp_path = path + ".tmp"
        with open(tmp_path, "w") as f:
            json.dump(raw, f)
        os.replace(tmp_path, path)
    except Exception as e:
        logger.warning(f"Failed to save session history for {session_id!r}: {e}")


_session_locks: dict[str, asyncio.Lock] = {}


def _get_session_lock(session_id: str) -> asyncio.Lock:
    if session_id not in _session_locks:
        _session_locks[session_id] = asyncio.Lock()
    return _session_locks[session_id]


def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    if session_id in sessions:
        sessions.move_to_end(session_id)
        sessions[session_id] = time.monotonic()
    else:
        if len(sessions) >= MAX_SESSIONS:
            _evicted_id, last_used_at = sessions.popitem(last=False)
            _session_locks.pop(_evicted_id, None)
            if a2_session_evictions_total is not None:
                a2_session_evictions_total.labels(**_LABELS).inc()
            if a2_session_age_seconds is not None:
                a2_session_age_seconds.labels(**_LABELS).observe(time.monotonic() - last_used_at)
        sessions[session_id] = time.monotonic()
        # Prune any lock entries for sessions no longer tracked
        stale = [sid for sid in _session_locks if sid not in sessions]
        for sid in stale:
            _session_locks.pop(sid, None)
    if a2_active_sessions is not None:
        a2_active_sessions.labels(**_LABELS).set(len(sessions))
    if a2_lru_cache_utilization_percent is not None:
        a2_lru_cache_utilization_percent.labels(**_LABELS).set(len(sessions) / MAX_SESSIONS * 100)


def _make_client() -> genai.Client:
    if not GEMINI_API_KEY:
        raise RuntimeError("No Gemini API key configured. Set GEMINI_API_KEY or GOOGLE_API_KEY.")
    return genai.Client(api_key=GEMINI_API_KEY)


async def run_query(
    prompt: str,
    session_id: str,
    agent_md_content: str,
    model: str | None = None,
) -> list[str]:
    resolved_model = model or GEMINI_MODEL

    instructions = f"Your name is {AGENT_NAME}. Your session ID is {session_id}."
    if agent_md_content:
        instructions = f"{agent_md_content}\n\nYour session ID is {session_id}."

    lock = _get_session_lock(session_id)
    async with lock:
        history = await asyncio.to_thread(_load_history, session_id)

        client = _make_client()

        # Create chat with persisted history and system instruction
        chat = client.aio.chats.create(
            model=resolved_model,
            config=types.GenerateContentConfig(
                system_instruction=instructions,
            ),
            history=history,
        )

        collected: list[str] = []
        _query_start = time.monotonic()
        _session_start = time.monotonic()
        _first_chunk_at: float | None = None
        _turn_count = 0
        _message_count = 0

        try:
            async for chunk in await chat.send_message_stream(prompt):
                _message_count += 1
                text = getattr(chunk, "text", None)
                if text:
                    if _first_chunk_at is None:
                        _first_chunk_at = time.monotonic()
                        if a2_sdk_time_to_first_message_seconds is not None:
                            a2_sdk_time_to_first_message_seconds.labels(**_LABELS, model=resolved_model).observe(
                                _first_chunk_at - _query_start
                            )
                    collected.append(text)
            _turn_count = 1
        except Exception:
            if a2_sdk_query_error_duration_seconds is not None:
                a2_sdk_query_error_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                    time.monotonic() - _query_start
                )
            if a2_sdk_session_duration_seconds is not None:
                a2_sdk_session_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                    time.monotonic() - _session_start
                )
            raise

        if a2_sdk_session_duration_seconds is not None:
            a2_sdk_session_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                time.monotonic() - _session_start
            )

        full_response = "".join(collected)
        if full_response:
            await log_entry("agent", full_response, session_id, model=resolved_model)

        # Persist updated history
        await asyncio.to_thread(_save_history, session_id, chat.history)

    if a2_sdk_query_duration_seconds is not None:
        a2_sdk_query_duration_seconds.labels(**_LABELS, model=resolved_model).observe(time.monotonic() - _query_start)
    if a2_sdk_messages_per_query is not None:
        a2_sdk_messages_per_query.labels(**_LABELS, model=resolved_model).observe(_message_count)
    if a2_sdk_turns_per_query is not None:
        a2_sdk_turns_per_query.labels(**_LABELS, model=resolved_model).observe(_turn_count)
    if a2_text_blocks_per_query is not None:
        a2_text_blocks_per_query.labels(**_LABELS, model=resolved_model).observe(len(collected))

    try:
        ts = datetime.now(timezone.utc).isoformat()
        entry = {
            "ts": ts,
            "agent": AGENT_NAME, "agent_id": AGENT_ID,
            "session_id": session_id,
            "event_type": "response",
            "model": resolved_model,
            "chunks": len(collected),
        }
        await log_trace(json.dumps(entry))
    except Exception as e:
        logger.error(f"log_trace error: {e}")

    return collected


async def run(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    agent_md_content: str,
    model: str | None = None,
) -> str:
    if a2_concurrent_queries is not None:
        a2_concurrent_queries.labels(**_LABELS).inc()
    try:
        return await _run_inner(prompt, session_id, sessions, agent_md_content, model)
    finally:
        if a2_concurrent_queries is not None:
            a2_concurrent_queries.labels(**_LABELS).dec()


async def _run_inner(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    agent_md_content: str,
    model: str | None = None,
) -> str:
    resolved_model = model or GEMINI_MODEL
    if a2_model_requests_total is not None:
        a2_model_requests_total.labels(**_LABELS, model=resolved_model).inc()

    is_new = session_id not in sessions
    if not is_new and a2_session_idle_seconds is not None:
        _last_used = sessions.get(session_id)
        if _last_used is not None:
            a2_session_idle_seconds.labels(**_LABELS).observe(time.monotonic() - _last_used)
    if a2_session_starts_total is not None:
        a2_session_starts_total.labels(**_LABELS, type="new" if is_new else "resumed").inc()

    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) — prompt: {prompt!r}")
    await log_entry("user", prompt, session_id, model=resolved_model)

    if a2_prompt_length_bytes is not None:
        a2_prompt_length_bytes.labels(**_LABELS).observe(len(prompt.encode()))

    _start = time.monotonic()
    try:
        collected = await asyncio.wait_for(
            run_query(prompt, session_id, agent_md_content, model=model),
            timeout=TASK_TIMEOUT_SECONDS,
        )
        _track_session(sessions, session_id)
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: timed out after {TASK_TIMEOUT_SECONDS}s.")
        if a2_tasks_total is not None:
            a2_tasks_total.labels(**_LABELS, status="timeout").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.labels(**_LABELS).set(time.time())
        raise
    except Exception:
        if a2_tasks_total is not None:
            a2_tasks_total.labels(**_LABELS, status="error").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.labels(**_LABELS).set(time.time())
        raise

    if a2_tasks_total is not None:
        a2_tasks_total.labels(**_LABELS, status="success").inc()
    if a2_task_last_success_timestamp_seconds is not None:
        a2_task_last_success_timestamp_seconds.labels(**_LABELS).set(time.time())
    if a2_task_duration_seconds is not None:
        a2_task_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
    if a2_task_timeout_headroom_seconds is not None:
        a2_task_timeout_headroom_seconds.labels(**_LABELS).observe(TASK_TIMEOUT_SECONDS - (time.monotonic() - _start))

    response = "".join(collected) if collected else ""
    if not response:
        if a2_empty_responses_total is not None:
            a2_empty_responses_total.labels(**_LABELS).inc()
    elif a2_response_length_bytes is not None:
        a2_response_length_bytes.labels(**_LABELS).observe(len(response.encode()))
    return response


class AgentExecutor(A2AAgentExecutor):
    def __init__(self):
        self._sessions: OrderedDict[str, float] = OrderedDict()
        self._running_tasks: dict[str, asyncio.Task] = {}
        self._agent_md_content: str = _load_agent_md()
        self._mcp_watcher_tasks: list[asyncio.Task] = []

    def _mcp_watchers(self):
        """No MCP watchers for gemini backend."""
        return []

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        _exec_start = time.monotonic()
        prompt = context.get_user_input()
        metadata = context.message.metadata or {}
        _raw_sid = "".join(c for c in str(context.context_id or metadata.get("session_id") or "").strip()[:256] if c >= " ")
        if not _raw_sid:
            session_id = str(uuid.uuid4())
        else:
            try:
                uuid.UUID(_raw_sid)
                session_id = _raw_sid
            except ValueError:
                session_id = str(uuid.uuid5(uuid.NAMESPACE_URL, _raw_sid))
        model = metadata.get("model") or None
        task_id = context.task_id

        if task_id:
            current = asyncio.current_task()
            if current:
                self._running_tasks[task_id] = current
                if a2_running_tasks is not None:
                    a2_running_tasks.labels(**_LABELS).inc()
        _response = ""
        _success = False
        _error: str | None = None
        try:
            _response = await run(
                prompt,
                session_id,
                self._sessions,
                self._agent_md_content,
                model=model,
            )
            _success = True
            if _response:
                await event_queue.enqueue_event(new_agent_text_message(_response))
            if a2_a2a_requests_total is not None:
                a2_a2a_requests_total.labels(**_LABELS, status="success").inc()
        except Exception as _exc:
            _error = repr(_exc)
            if a2_a2a_requests_total is not None:
                a2_a2a_requests_total.labels(**_LABELS, status="error").inc()
            raise
        finally:
            if a2_a2a_request_duration_seconds is not None:
                a2_a2a_request_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _exec_start)
            if a2_a2a_last_request_timestamp_seconds is not None:
                a2_a2a_last_request_timestamp_seconds.labels(**_LABELS).set(time.time())
            if task_id and task_id in self._running_tasks:
                self._running_tasks.pop(task_id)
                if a2_running_tasks is not None:
                    a2_running_tasks.labels(**_LABELS).dec()

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        if a2_task_cancellations_total is not None:
            a2_task_cancellations_total.labels(**_LABELS).inc()
        task_id = context.task_id
        task = self._running_tasks.get(task_id) if task_id else None
        if task:
            task.cancel()
            logger.info(f"Task {task_id!r} cancellation requested.")
        else:
            logger.info(f"Task {task_id!r} cancellation requested but no running task found.")

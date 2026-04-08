import asyncio
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
from agents import Agent, Runner, RunConfig, SQLiteSession
from agents.models.multi_provider import MultiProvider
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

AGENT_NAME = os.environ.get("AGENT_NAME", "a2-codex")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.log")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
AGENT_MD = os.environ.get("AGENT_MD", "/home/agent/agent.md")
CODEX_SESSION_DB = os.environ.get("CODEX_SESSION_DB", "/home/agent/logs/codex_sessions.db")

_DEFAULT_TOOLS = "Read,Write,Edit,Bash,Glob,Grep,WebSearch,WebFetch"
ALLOWED_TOOLS: list[str] = [t.strip() for t in os.environ.get("ALLOWED_TOOLS", _DEFAULT_TOOLS).split(",") if t.strip()]

MAX_LOG_BYTES = int(os.environ.get("MAX_LOG_BYTES", str(10 * 1024 * 1024)))
MAX_LOG_BACKUP_COUNT = int(os.environ.get("MAX_LOG_BACKUP_COUNT", "1"))
MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))

CODEX_MODEL = os.environ.get("CODEX_MODEL") or "codex-mini-latest"
OPENAI_API_KEY: str | None = os.environ.get("OPENAI_API_KEY") or None

_BACKEND_ID = "codex"


def _load_agent_md() -> str:
    try:
        with open(AGENT_MD) as f:
            return f.read()
    except OSError:
        return ""


def get_conversation_logger() -> logging.Logger:
    conv_logger = logging.getLogger("conversation")
    if not conv_logger.handlers:
        log_dir = os.path.dirname(CONVERSATION_LOG)
        if log_dir:
            os.makedirs(log_dir, exist_ok=True)
        handler = RotatingFileHandler(CONVERSATION_LOG, maxBytes=MAX_LOG_BYTES, backupCount=MAX_LOG_BACKUP_COUNT)
        handler.setFormatter(logging.Formatter("%(message)s"))
        conv_logger.addHandler(handler)
        conv_logger.setLevel(logging.INFO)
        conv_logger.propagate = False
    return conv_logger


def get_trace_logger() -> logging.Logger:
    trace_logger = logging.getLogger("trace")
    if not trace_logger.handlers:
        trace_dir = os.path.dirname(TRACE_LOG)
        if trace_dir:
            os.makedirs(trace_dir, exist_ok=True)
        handler = RotatingFileHandler(TRACE_LOG, maxBytes=MAX_LOG_BYTES, backupCount=MAX_LOG_BACKUP_COUNT)
        handler.setFormatter(logging.Formatter("%(message)s"))
        trace_logger.addHandler(handler)
        trace_logger.setLevel(logging.INFO)
        trace_logger.propagate = False
    return trace_logger


def log_entry(role: str, text: str, session_id: str, suffix: str = "") -> None:
    try:
        ts = datetime.now(timezone.utc).isoformat()
        conv = get_conversation_logger()
        _formatted = f"[{ts}] [{session_id}] [{role.upper()}]{suffix}\n{text}\n{'-' * 80}"
        conv.info(_formatted)
        if a2_log_entries_total is not None:
            a2_log_entries_total.labels(logger="conversation").inc()
        if a2_log_bytes_total is not None:
            a2_log_bytes_total.labels(logger="conversation").inc(len(_formatted.encode()))
    except Exception as e:
        if a2_log_write_errors_total is not None:
            a2_log_write_errors_total.inc()
        logger.error(f"log_entry error: {e}")


def log_trace(text: str) -> None:
    try:
        trace = get_trace_logger()
        trace.info(text)
        if a2_log_entries_total is not None:
            a2_log_entries_total.labels(logger="trace").inc()
        if a2_log_bytes_total is not None:
            a2_log_bytes_total.labels(logger="trace").inc(len(text.encode()))
    except Exception as e:
        if a2_log_write_errors_total is not None:
            a2_log_write_errors_total.inc()
        logger.error(f"log_trace error: {e}")


def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    if session_id in sessions:
        sessions.move_to_end(session_id)
        sessions[session_id] = time.monotonic()
        return
    if len(sessions) >= MAX_SESSIONS:
        _evicted_id, last_used_at = sessions.popitem(last=False)
        if a2_session_evictions_total is not None:
            a2_session_evictions_total.inc()
        if a2_session_age_seconds is not None:
            a2_session_age_seconds.observe(time.monotonic() - last_used_at)
    sessions[session_id] = time.monotonic()
    if a2_active_sessions is not None:
        a2_active_sessions.set(len(sessions))
    if a2_lru_cache_utilization_percent is not None:
        a2_lru_cache_utilization_percent.set(len(sessions) / MAX_SESSIONS * 100)


async def run_query(
    prompt: str,
    session_id: str,
    agent_md_content: str,
    model: str | None = None,
) -> list[str]:
    resolved_model = model or CODEX_MODEL
    log_dir = os.path.dirname(CODEX_SESSION_DB)
    if log_dir:
        os.makedirs(log_dir, exist_ok=True)

    instructions = f"Your name is {AGENT_NAME}. Your session ID is {session_id}."
    if agent_md_content:
        instructions = f"{agent_md_content}\n\nYour session ID is {session_id}."

    codex_agent = Agent(
        name=AGENT_NAME,
        instructions=instructions,
        model=resolved_model,
    )

    session = SQLiteSession(session_id, CODEX_SESSION_DB)

    run_config = RunConfig(model_provider=MultiProvider(openai_api_key=OPENAI_API_KEY)) if OPENAI_API_KEY else None

    collected: list[str] = []
    _query_start = time.monotonic()
    _session_start = time.monotonic()
    _first_chunk_at: float | None = None
    _turn_count = 0
    _message_count = 0

    try:
        result = Runner.run_streamed(codex_agent, prompt, session=session, run_config=run_config)
        async for event in result.stream_events():
            _message_count += 1
            if event.type == "raw_response_event":
                delta = getattr(getattr(event, "data", None), "delta", None)
                if delta and hasattr(delta, "text") and delta.text:
                    if _first_chunk_at is None:
                        _first_chunk_at = time.monotonic()
                        if a2_sdk_time_to_first_message_seconds is not None:
                            a2_sdk_time_to_first_message_seconds.labels(backend=_BACKEND_ID).observe(
                                _first_chunk_at - _query_start
                            )
                    collected.append(delta.text)
            elif event.type == "agent_updated_stream_event":
                _turn_count += 1
    except Exception:
        if a2_sdk_query_error_duration_seconds is not None:
            a2_sdk_query_error_duration_seconds.labels(backend=_BACKEND_ID).observe(
                time.monotonic() - _query_start
            )
        if a2_sdk_session_duration_seconds is not None:
            a2_sdk_session_duration_seconds.labels(backend=_BACKEND_ID).observe(
                time.monotonic() - _session_start
            )
        raise

    if a2_sdk_session_duration_seconds is not None:
        a2_sdk_session_duration_seconds.labels(backend=_BACKEND_ID).observe(
            time.monotonic() - _session_start
        )

    # Flush any final output not captured via streaming deltas
    final = getattr(result, "final_output", None)
    if final and isinstance(final, str) and not collected:
        collected.append(final)

    full_response = "".join(collected)
    if full_response:
        log_entry("agent", full_response, session_id)

    if a2_sdk_query_duration_seconds is not None:
        a2_sdk_query_duration_seconds.labels(backend=_BACKEND_ID).observe(time.monotonic() - _query_start)
    if a2_sdk_messages_per_query is not None:
        a2_sdk_messages_per_query.labels(backend=_BACKEND_ID).observe(_message_count)
    if a2_sdk_turns_per_query is not None:
        a2_sdk_turns_per_query.labels(backend=_BACKEND_ID).observe(_turn_count)
    if a2_text_blocks_per_query is not None:
        a2_text_blocks_per_query.observe(len(collected))

    # Log a trace entry for the completed turn
    try:
        ts = datetime.now(timezone.utc).isoformat()
        entry = {
            "ts": ts,
            "session_id": session_id,
            "event_type": "response",
            "model": resolved_model,
            "chunks": len(collected),
        }
        log_trace(json.dumps(entry))
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
        a2_concurrent_queries.inc()
    try:
        return await _run_inner(prompt, session_id, sessions, agent_md_content, model)
    finally:
        if a2_concurrent_queries is not None:
            a2_concurrent_queries.dec()


async def _run_inner(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    agent_md_content: str,
    model: str | None = None,
) -> str:
    resolved_model = model or CODEX_MODEL
    if a2_model_requests_total is not None:
        a2_model_requests_total.labels(model=resolved_model).inc()

    is_new = session_id not in sessions
    if not is_new and a2_session_idle_seconds is not None:
        a2_session_idle_seconds.observe(time.monotonic() - sessions[session_id])
    if a2_session_starts_total is not None:
        a2_session_starts_total.labels(type="new" if is_new else "resumed").inc()

    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) — prompt: {prompt!r}")
    log_entry("user", prompt, session_id)

    if a2_prompt_length_bytes is not None:
        a2_prompt_length_bytes.observe(len(prompt.encode()))

    _start = time.monotonic()
    try:
        collected = await asyncio.wait_for(
            run_query(prompt, session_id, agent_md_content, model=model),
            timeout=TASK_TIMEOUT_SECONDS,
        )
        _track_session(sessions, session_id)
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: timed out after {TASK_TIMEOUT_SECONDS}s.")
        _track_session(sessions, session_id)
        if a2_tasks_total is not None:
            a2_tasks_total.labels(status="timeout").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.set(time.time())
        raise
    except Exception:
        _track_session(sessions, session_id)
        if a2_tasks_total is not None:
            a2_tasks_total.labels(status="error").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.set(time.time())
        raise

    if a2_tasks_total is not None:
        a2_tasks_total.labels(status="success").inc()
    if a2_task_last_success_timestamp_seconds is not None:
        a2_task_last_success_timestamp_seconds.set(time.time())
    if a2_task_duration_seconds is not None:
        a2_task_duration_seconds.observe(time.monotonic() - _start)
    if a2_task_timeout_headroom_seconds is not None:
        a2_task_timeout_headroom_seconds.observe(TASK_TIMEOUT_SECONDS - (time.monotonic() - _start))

    response = "".join(collected) if collected else ""
    if not response:
        if a2_empty_responses_total is not None:
            a2_empty_responses_total.inc()
    elif a2_response_length_bytes is not None:
        a2_response_length_bytes.observe(len(response.encode()))
    return response


class AgentExecutor(A2AAgentExecutor):
    def __init__(self):
        self._sessions: OrderedDict[str, float] = OrderedDict()
        self._running_tasks: dict[str, asyncio.Task] = {}
        self._agent_md_content: str = _load_agent_md()
        self._mcp_watcher_tasks: list[asyncio.Task] = []

    def _mcp_watchers(self):
        """No MCP watchers for codex backend."""
        return []

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        _exec_start = time.monotonic()
        prompt = context.get_user_input()
        metadata = context.message.metadata or {}
        _raw_sid = "".join(c for c in str(metadata.get("session_id") or "").strip()[:256] if c >= " ")
        session_id = _raw_sid or str(uuid.uuid4())
        model = metadata.get("model") or None
        task_id = context.task_id

        if task_id:
            current = asyncio.current_task()
            if current:
                self._running_tasks[task_id] = current
                if a2_running_tasks is not None:
                    a2_running_tasks.inc()
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
                a2_a2a_requests_total.labels(status="success").inc()
        except Exception as _exc:
            _error = repr(_exc)
            if a2_a2a_requests_total is not None:
                a2_a2a_requests_total.labels(status="error").inc()
            raise
        finally:
            if a2_a2a_request_duration_seconds is not None:
                a2_a2a_request_duration_seconds.observe(time.monotonic() - _exec_start)
            if a2_a2a_last_request_timestamp_seconds is not None:
                a2_a2a_last_request_timestamp_seconds.set(time.time())
            if task_id and task_id in self._running_tasks:
                self._running_tasks.pop(task_id)
                if a2_running_tasks is not None:
                    a2_running_tasks.dec()

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        if a2_task_cancellations_total is not None:
            a2_task_cancellations_total.inc()
        task_id = context.task_id
        task = self._running_tasks.get(task_id) if task_id else None
        if task:
            task.cancel()
            logger.info(f"Task {task_id!r} cancellation requested.")
        else:
            logger.info(f"Task {task_id!r} cancellation requested but no running task found.")

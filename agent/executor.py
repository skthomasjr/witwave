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
from bus import Message
from claude_agent_sdk import ClaudeAgentOptions, ClaudeSDKClient
from claude_agent_sdk.types import AssistantMessage, ResultMessage, TextBlock, ToolResultBlock, ToolUseBlock
from metrics import (
    agent_a2a_last_request_timestamp_seconds,
    agent_a2a_request_duration_seconds,
    agent_a2a_requests_total,
    agent_active_sessions,
    agent_concurrent_queries,
    agent_context_exhaustion_total,
    agent_context_tokens,
    agent_context_usage_percent,
    agent_context_warnings_total,
    agent_empty_responses_total,
    agent_file_watcher_restarts_total,
    agent_log_entries_total,
    agent_log_write_errors_total,
    agent_lru_cache_utilization_percent,
    agent_mcp_config_errors_total,
    agent_mcp_config_reloads_total,
    agent_mcp_servers_active,
    agent_model_requests_total,
    agent_prompt_length_bytes,
    agent_response_length_bytes,
    agent_running_tasks,
    agent_sdk_client_errors_total,
    agent_sdk_context_fetch_errors_total,
    agent_sdk_errors_total,
    agent_sdk_messages_per_query,
    agent_sdk_query_duration_seconds,
    agent_sdk_result_errors_total,
    agent_sdk_session_duration_seconds,
    agent_sdk_tokens_per_query,
    agent_sdk_tool_calls_per_query,
    agent_sdk_tool_calls_total,
    agent_sdk_tool_errors_total,
    agent_session_age_seconds,
    agent_session_evictions_total,
    agent_session_starts_total,
    agent_stderr_lines_per_task,
    agent_task_cancellations_total,
    agent_task_duration_seconds,
    agent_task_error_duration_seconds,
    agent_task_last_error_timestamp_seconds,
    agent_task_last_success_timestamp_seconds,
    agent_task_retries_total,
    agent_task_timeout_headroom_seconds,
    agent_tasks_total,
    agent_tasks_with_stderr_total,
    agent_text_blocks_per_query,
    agent_watcher_events_total,
)
from watchfiles import awatch

logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "claude-agent")
CLAUDE_MODEL: str | None = os.environ.get("CLAUDE_MODEL") or None
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.log")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
_DEFAULT_TOOLS = "Read,Write,Edit,Bash,Glob,Grep,WebSearch,WebFetch"
ALLOWED_TOOLS: list[str] = [t.strip() for t in os.environ.get("ALLOWED_TOOLS", _DEFAULT_TOOLS).split(",") if t.strip()]


MAX_LOG_BYTES = 10 * 1024 * 1024  # 10 MB per file, 1 backup kept
MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
CONTEXT_USAGE_WARN_THRESHOLD = float(os.environ.get("CONTEXT_USAGE_WARN_THRESHOLD", "0.9"))

MCP_CONFIG_PATH = os.environ.get("MCP_CONFIG_PATH", "/home/agent/.claude/mcp.json")
_mcp_servers: dict = {}


def _load_mcp_config() -> dict:
    if not os.path.exists(MCP_CONFIG_PATH):
        return {}
    try:
        with open(MCP_CONFIG_PATH) as f:
            return json.load(f)
    except Exception as e:
        if agent_mcp_config_errors_total is not None:
            agent_mcp_config_errors_total.inc()
        logger.warning(f"Failed to load MCP config from {MCP_CONFIG_PATH}: {e}")
        return {}


async def mcp_config_watcher() -> None:
    global _mcp_servers
    _mcp_servers = _load_mcp_config()
    if agent_mcp_servers_active is not None:
        agent_mcp_servers_active.set(len(_mcp_servers))
    if _mcp_servers:
        logger.info(f"MCP config loaded: {list(_mcp_servers.keys())}")

    watch_dir = os.path.dirname(MCP_CONFIG_PATH)

    while True:
        if not os.path.isdir(watch_dir):
            logger.info("MCP config directory not found — retrying in 10s.")
            await asyncio.sleep(10)
            continue

        async for changes in awatch(watch_dir):
            if agent_watcher_events_total is not None:
                agent_watcher_events_total.labels(watcher="mcp").inc()
            for _, path in changes:
                if path == MCP_CONFIG_PATH:
                    _mcp_servers = _load_mcp_config()
                    if agent_mcp_servers_active is not None:
                        agent_mcp_servers_active.set(len(_mcp_servers))
                    logger.info(f"MCP config reloaded: {list(_mcp_servers.keys())}")
                    if agent_mcp_config_reloads_total is not None:
                        agent_mcp_config_reloads_total.inc()
                    break

        logger.warning("MCP config directory watcher exited — directory deleted or unavailable. Retrying in 10s.")
        if agent_file_watcher_restarts_total is not None:
            agent_file_watcher_restarts_total.labels(watcher="mcp").inc()
        await asyncio.sleep(10)


def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    """Record session_id access, evicting the least-recently-used entry if the cap is reached."""
    if session_id in sessions:
        sessions.move_to_end(session_id)
        return
    if len(sessions) >= MAX_SESSIONS:
        _evicted_id, created_at = sessions.popitem(last=False)  # evict oldest (least-recently-used)
        if agent_session_evictions_total is not None:
            agent_session_evictions_total.inc()
        if agent_session_age_seconds is not None:
            agent_session_age_seconds.observe(time.monotonic() - created_at)
    sessions[session_id] = time.monotonic()
    if agent_active_sessions is not None:
        agent_active_sessions.set(len(sessions))
    if agent_lru_cache_utilization_percent is not None:
        agent_lru_cache_utilization_percent.set(len(sessions) / MAX_SESSIONS * 100)


def get_conversation_logger() -> logging.Logger:
    conv_logger = logging.getLogger("conversation")
    if not conv_logger.handlers:
        log_dir = os.path.dirname(CONVERSATION_LOG)
        if log_dir:
            os.makedirs(log_dir, exist_ok=True)
        handler = RotatingFileHandler(CONVERSATION_LOG, maxBytes=MAX_LOG_BYTES, backupCount=1)
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
        handler = RotatingFileHandler(TRACE_LOG, maxBytes=MAX_LOG_BYTES, backupCount=1)
        handler.setFormatter(logging.Formatter("%(message)s"))
        trace_logger.addHandler(handler)
        trace_logger.setLevel(logging.INFO)
        trace_logger.propagate = False
    return trace_logger


def log_entry(role: str, text: str, session_id: str, suffix: str = "") -> None:
    try:
        ts = datetime.now(timezone.utc).isoformat()
        conv = get_conversation_logger()
        conv.info(f"[{ts}] [{session_id}] [{role.upper()}]{suffix}\n{text}\n{'-' * 80}")
        if agent_log_entries_total is not None:
            agent_log_entries_total.labels(logger="conversation").inc()
    except Exception as e:
        if agent_log_write_errors_total is not None:
            agent_log_write_errors_total.inc()
        logger.error(f"log_entry error: {e}")


def log_tool_event(event_type: str, block, session_id: str, model: str | None = None) -> None:
    try:
        ts = datetime.now(timezone.utc).isoformat()
        if event_type == "tool_use":
            entry = {
                "ts": ts,
                "session_id": session_id,
                "event_type": event_type,
                "model": model,
                "id": block.id,
                "name": block.name,
                "input": block.input,
            }
        else:  # tool_result
            entry = {
                "ts": ts,
                "session_id": session_id,
                "event_type": event_type,
                "model": model,
                "tool_use_id": block.tool_use_id,
                "content": block.content,
                "is_error": block.is_error,
            }
        get_trace_logger().info(json.dumps(entry))
        if agent_log_entries_total is not None:
            agent_log_entries_total.labels(logger="trace").inc()
    except Exception as e:
        if agent_log_write_errors_total is not None:
            agent_log_write_errors_total.inc()
        logger.error(f"log_tool_event error: {e}")


def log_context_usage(usage: dict, session_id: str) -> None:
    try:
        ts = datetime.now(timezone.utc).isoformat()
        entry = {
            "ts": ts,
            "session_id": session_id,
            "event_type": "context_usage",
            "percentage": usage.get("percentage"),
            "total_tokens": usage.get("totalTokens"),
            "max_tokens": usage.get("maxTokens"),
            "categories": usage.get("categories", []),
        }
        get_trace_logger().info(json.dumps(entry))
        if agent_log_entries_total is not None:
            agent_log_entries_total.labels(logger="trace").inc()
    except Exception as e:
        if agent_log_write_errors_total is not None:
            agent_log_write_errors_total.inc()
        logger.error(f"log_context_usage error: {e}")


async def run_query(
    prompt: str, options: ClaudeAgentOptions, session_id: str = "", model: str | None = None
) -> list[str]:
    collected: list[str] = []
    _query_start = time.monotonic()
    _message_count = 0
    _tool_names: dict[str, str] = {}  # tool_use_id → tool name
    _last_total_tokens = 0
    _session_start = time.monotonic()
    try:
        async with ClaudeSDKClient(options=options) as client:
            await client.query(prompt)
            async for message in client.receive_response():
                _message_count += 1
                if isinstance(message, AssistantMessage):
                    for block in message.content:
                        if isinstance(block, TextBlock):
                            collected.append(block.text)
                            log_entry("agent", block.text, session_id)
                        elif isinstance(block, ToolUseBlock):
                            _tool_names[block.id] = block.name
                            if agent_sdk_tool_calls_total is not None:
                                agent_sdk_tool_calls_total.labels(tool=block.name).inc()
                            log_tool_event("tool_use", block, session_id, model=model)
                        elif isinstance(block, ToolResultBlock):
                            if block.is_error and agent_sdk_tool_errors_total is not None:
                                tool_name = _tool_names.get(block.tool_use_id, "unknown")
                                agent_sdk_tool_errors_total.labels(tool=tool_name).inc()
                            log_tool_event("tool_result", block, session_id, model=model)
                    try:
                        usage = await client.get_context_usage()
                        pct = usage.get("percentage", 0.0)
                        _last_total_tokens = usage.get("totalTokens", 0)
                        if agent_context_tokens is not None:
                            agent_context_tokens.observe(_last_total_tokens)
                        if agent_context_usage_percent is not None:
                            agent_context_usage_percent.observe(pct)
                        if pct >= 100 and agent_context_exhaustion_total is not None:
                            agent_context_exhaustion_total.inc()
                        if pct >= CONTEXT_USAGE_WARN_THRESHOLD * 100:
                            if agent_context_warnings_total is not None:
                                agent_context_warnings_total.inc()
                            logger.warning(
                                f"Session {session_id!r}: context usage {usage['percentage']:.1f}% "
                                f"exceeds threshold {CONTEXT_USAGE_WARN_THRESHOLD * 100:.0f}%"
                            )
                            log_context_usage(usage, session_id)
                    except Exception as e:
                        if agent_sdk_context_fetch_errors_total is not None:
                            agent_sdk_context_fetch_errors_total.inc()
                        logger.warning(f"Session {session_id!r}: get_context_usage failed: {e}")
                elif isinstance(message, ResultMessage) and message.is_error:
                    if agent_sdk_result_errors_total is not None:
                        agent_sdk_result_errors_total.inc()
                    raise RuntimeError("\n".join(message.errors or []))
    except (OSError, ConnectionError):
        if agent_sdk_client_errors_total is not None:
            agent_sdk_client_errors_total.inc()
        raise
    if agent_sdk_session_duration_seconds is not None:
        agent_sdk_session_duration_seconds.observe(time.monotonic() - _session_start)
    if agent_sdk_query_duration_seconds is not None:
        agent_sdk_query_duration_seconds.observe(time.monotonic() - _query_start)
    if agent_sdk_messages_per_query is not None:
        agent_sdk_messages_per_query.observe(_message_count)
    if agent_sdk_tokens_per_query is not None:
        agent_sdk_tokens_per_query.observe(_last_total_tokens)
    if agent_sdk_tool_calls_per_query is not None:
        agent_sdk_tool_calls_per_query.observe(len(_tool_names))
    if agent_text_blocks_per_query is not None:
        agent_text_blocks_per_query.observe(len(collected))
    return collected


async def run(prompt: str, session_id: str, sessions: OrderedDict[str, float], model: str | None = None) -> str:
    if agent_concurrent_queries is not None:
        agent_concurrent_queries.inc()
    try:
        return await _run_inner(prompt, session_id, sessions, model=model)
    finally:
        if agent_concurrent_queries is not None:
            agent_concurrent_queries.dec()


async def _run_inner(prompt: str, session_id: str, sessions: OrderedDict[str, float], model: str | None = None) -> str:
    resolved_model = model or CLAUDE_MODEL
    if agent_model_requests_total is not None:
        agent_model_requests_total.labels(model=resolved_model or "default").inc()
    is_new = session_id not in sessions
    if agent_session_starts_total is not None:
        agent_session_starts_total.labels(type="new" if is_new else "resumed").inc()

    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) — received prompt: {prompt!r}")
    model_tag = f" [model: {resolved_model}]" if resolved_model else ""
    log_entry("user", prompt, session_id, suffix=model_tag)

    stderr_lines: list[str] = []

    def capture_stderr(line: str) -> None:
        stderr_lines.append(line)
        if agent_sdk_errors_total is not None:
            agent_sdk_errors_total.inc()
        logger.error(f"[claude stderr] {line}")

    def make_options(resume: bool) -> ClaudeAgentOptions:
        return ClaudeAgentOptions(
            allowed_tools=ALLOWED_TOOLS,
            system_prompt=f"Your name is {AGENT_NAME}. Your session ID is {session_id}.",
            resume=session_id if resume else None,
            session_id=None if resume else session_id,
            stderr=capture_stderr,
            mcp_servers=_mcp_servers,
            model=resolved_model,
        )

    if agent_prompt_length_bytes is not None:
        agent_prompt_length_bytes.observe(len(prompt.encode()))

    _start = time.monotonic()
    try:
        collected = await asyncio.wait_for(
            run_query(prompt, make_options(resume=not is_new), session_id=session_id, model=resolved_model),
            timeout=TASK_TIMEOUT_SECONDS,
        )
        _track_session(sessions, session_id)
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: run_query timed out after {TASK_TIMEOUT_SECONDS}s.")
        if agent_tasks_total is not None:
            agent_tasks_total.labels(status="timeout").inc()
        if agent_task_error_duration_seconds is not None:
            agent_task_error_duration_seconds.observe(time.monotonic() - _start)
        if agent_task_last_error_timestamp_seconds is not None:
            agent_task_last_error_timestamp_seconds.set(time.time())
        raise
    except Exception:
        if is_new and any("already in use" in line.lower() for line in stderr_lines):
            if agent_task_retries_total is not None:
                agent_task_retries_total.inc()
            try:
                collected = await asyncio.wait_for(
                    run_query(prompt, make_options(resume=True), session_id=session_id, model=resolved_model),
                    timeout=TASK_TIMEOUT_SECONDS,
                )
            except asyncio.TimeoutError:
                if agent_tasks_total is not None:
                    agent_tasks_total.labels(status="timeout").inc()
                if agent_task_error_duration_seconds is not None:
                    agent_task_error_duration_seconds.observe(time.monotonic() - _start)
                if agent_task_last_error_timestamp_seconds is not None:
                    agent_task_last_error_timestamp_seconds.set(time.time())
                raise
            except Exception:
                if agent_tasks_total is not None:
                    agent_tasks_total.labels(status="error").inc()
                if agent_task_error_duration_seconds is not None:
                    agent_task_error_duration_seconds.observe(time.monotonic() - _start)
                if agent_task_last_error_timestamp_seconds is not None:
                    agent_task_last_error_timestamp_seconds.set(time.time())
                raise
            _track_session(sessions, session_id)
        else:
            if agent_tasks_total is not None:
                agent_tasks_total.labels(status="error").inc()
            if agent_task_error_duration_seconds is not None:
                agent_task_error_duration_seconds.observe(time.monotonic() - _start)
            if agent_task_last_error_timestamp_seconds is not None:
                agent_task_last_error_timestamp_seconds.set(time.time())
            raise

    if agent_tasks_total is not None:
        agent_tasks_total.labels(status="success").inc()
    if agent_task_last_success_timestamp_seconds is not None:
        agent_task_last_success_timestamp_seconds.set(time.time())
    if stderr_lines and agent_tasks_with_stderr_total is not None:
        agent_tasks_with_stderr_total.inc()
    if agent_stderr_lines_per_task is not None:
        agent_stderr_lines_per_task.observe(len(stderr_lines))
    if agent_task_duration_seconds is not None:
        agent_task_duration_seconds.observe(time.monotonic() - _start)
    if agent_task_timeout_headroom_seconds is not None:
        agent_task_timeout_headroom_seconds.observe(TASK_TIMEOUT_SECONDS - (time.monotonic() - _start))
    response = "\n\n".join(collected) if collected else ""
    if not response:
        if agent_empty_responses_total is not None:
            agent_empty_responses_total.inc()
    elif agent_response_length_bytes is not None:
        agent_response_length_bytes.observe(len(response.encode()))
    return response


class AgentExecutor(A2AAgentExecutor):
    def __init__(self):
        self._sessions: OrderedDict[str, float] = OrderedDict()
        self._running_tasks: dict[str, asyncio.Task] = {}

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        _exec_start = time.monotonic()
        prompt = context.get_user_input()
        metadata = context.message.metadata or {}
        session_id = metadata.get("session_id") or str(uuid.uuid4())
        task_id = context.task_id

        if task_id:
            current = asyncio.current_task()
            if current:
                self._running_tasks[task_id] = current
                if agent_running_tasks is not None:
                    agent_running_tasks.inc()
        try:
            response = await run(prompt, session_id, self._sessions)
            if response:
                await event_queue.enqueue_event(new_agent_text_message(response))
            if agent_a2a_requests_total is not None:
                agent_a2a_requests_total.labels(status="success").inc()
        except Exception:
            if agent_a2a_requests_total is not None:
                agent_a2a_requests_total.labels(status="error").inc()
            raise
        finally:
            if agent_a2a_request_duration_seconds is not None:
                agent_a2a_request_duration_seconds.observe(time.monotonic() - _exec_start)
            if agent_a2a_last_request_timestamp_seconds is not None:
                agent_a2a_last_request_timestamp_seconds.set(time.time())
            if task_id and task_id in self._running_tasks:
                self._running_tasks.pop(task_id)
                if agent_running_tasks is not None:
                    agent_running_tasks.dec()

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        if agent_task_cancellations_total is not None:
            agent_task_cancellations_total.inc()
        task_id = context.task_id
        task = self._running_tasks.get(task_id) if task_id else None
        if task:
            task.cancel()
            logger.info(f"Task {task_id!r} cancellation requested — in-progress run_query interrupted.")
        else:
            logger.info(f"Task {task_id!r} cancellation requested but no running task found.")

    async def process_bus(self, message: Message) -> None:
        try:
            response = await run(
                message.prompt, message.session_id or str(uuid.uuid4()), self._sessions, model=message.model
            )
            if message.result is not None:
                message.result.set_result(response)
        except Exception as e:
            logger.exception(f"process_bus error for session {message.session_id!r}: {e}")
            if message.result is not None:
                message.result.set_exception(e)

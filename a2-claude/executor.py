import asyncio
import json
import logging
import os
import time
import uuid
from collections import OrderedDict
from datetime import datetime, timezone

from a2a.server.agent_execution import AgentExecutor as A2AAgentExecutor
from a2a.server.agent_execution import RequestContext
from a2a.server.events import EventQueue
from a2a.utils import new_agent_text_message
from claude_agent_sdk import ClaudeAgentOptions, ClaudeSDKClient
from claude_agent_sdk.types import AssistantMessage, ResultMessage, TextBlock, ToolResultBlock, ToolUseBlock
from metrics import (
    a2_a2a_last_request_timestamp_seconds,
    a2_a2a_request_duration_seconds,
    a2_a2a_requests_total,
    a2_active_sessions,
    a2_budget_exceeded_total,
    a2_concurrent_queries,
    a2_context_exhaustion_total,
    a2_context_tokens,
    a2_context_tokens_remaining,
    a2_context_usage_percent,
    a2_context_warnings_total,
    a2_empty_responses_total,
    a2_file_watcher_restarts_total,
    a2_log_bytes_total,
    a2_log_entries_total,
    a2_log_write_errors_total,
    a2_lru_cache_utilization_percent,
    a2_mcp_config_errors_total,
    a2_mcp_config_reloads_total,
    a2_mcp_servers_active,
    a2_model_requests_total,
    a2_prompt_length_bytes,
    a2_response_length_bytes,
    a2_running_tasks,
    a2_sdk_client_errors_total,
    a2_sdk_context_fetch_errors_total,
    a2_sdk_errors_total,
    a2_sdk_messages_per_query,
    a2_sdk_query_duration_seconds,
    a2_sdk_query_error_duration_seconds,
    a2_sdk_result_errors_total,
    a2_sdk_session_duration_seconds,
    a2_sdk_subprocess_spawn_duration_seconds,
    a2_sdk_time_to_first_message_seconds,
    a2_sdk_tokens_per_query,
    a2_sdk_tool_call_input_size_bytes,
    a2_sdk_tool_calls_per_query,
    a2_sdk_tool_calls_total,
    a2_sdk_tool_duration_seconds,
    a2_sdk_tool_errors_total,
    a2_sdk_tool_result_size_bytes,
    a2_sdk_turns_per_query,
    a2_session_age_seconds,
    a2_session_evictions_total,
    a2_session_history_save_errors_total,
    a2_session_idle_seconds,
    a2_session_starts_total,
    a2_stderr_lines_per_task,
    a2_task_cancellations_total,
    a2_task_duration_seconds,
    a2_task_error_duration_seconds,
    a2_task_last_error_timestamp_seconds,
    a2_task_last_success_timestamp_seconds,
    a2_task_retries_total,
    a2_task_timeout_headroom_seconds,
    a2_tasks_total,
    a2_tasks_with_stderr_total,
    a2_text_blocks_per_query,
    a2_watcher_events_total,
)
from watchfiles import awatch
from log_utils import _append_log
from exceptions import BudgetExceededError

logger = logging.getLogger(__name__)


def _session_file_path(session_id: str) -> "pathlib.Path | None":
    """Return the on-disk path for a Claude session file, or None on error.

    Derives the session file path from documented conventions only — no
    private SDK internals.  The Claude Agent SDK stores session files at:

        <config_home>/projects/<sanitized_cwd>/<session_id>.jsonl

    where config_home is $CLAUDE_CONFIG_DIR or ~/.claude, and sanitized_cwd
    is the working directory with all non-alphanumeric characters replaced by
    hyphens (truncated to 200 characters with a hash suffix if longer).
    """
    import pathlib
    import re
    import unicodedata

    _SANITIZE_RE = re.compile(r"[^a-zA-Z0-9]")
    _MAX_LEN = 200

    def _simple_hash(s: str) -> str:
        """32-bit JS-compatible hash to base36 — matches Claude CLI directory naming."""
        h = 0
        for ch in s:
            h = (h << 5) - h + ord(ch)
            h = h & 0xFFFFFFFF
            if h >= 0x80000000:
                h -= 0x100000000
        h = abs(h)
        if h == 0:
            return "0"
        digits = "0123456789abcdefghijklmnopqrstuvwxyz"
        out: list[str] = []
        n = h
        while n > 0:
            out.append(digits[n % 36])
            n //= 36
        return "".join(reversed(out))

    def _sanitize(name: str) -> str:
        sanitized = _SANITIZE_RE.sub("-", name)
        if len(sanitized) <= _MAX_LEN:
            return sanitized
        return f"{sanitized[:_MAX_LEN]}-{_simple_hash(name)}"

    def _config_home() -> pathlib.Path:
        config_dir = os.environ.get("CLAUDE_CONFIG_DIR")
        if config_dir:
            return pathlib.Path(unicodedata.normalize("NFC", config_dir))
        return pathlib.Path(unicodedata.normalize("NFC", str(pathlib.Path.home() / ".claude")))

    try:
        cwd = os.getcwd()
        sessions_dir = _config_home() / "projects" / _sanitize(cwd)
        return sessions_dir / f"{session_id}.jsonl"
    except Exception:
        return None


def _session_file_exists(session_id: str) -> bool:
    """Check whether a Claude session file exists on disk for this session_id.

    Used to detect resumed sessions after a process restart, when the in-memory
    LRU cache is empty but history exists on disk.  Always returns False if any
    error occurs so it never prevents a prompt from being processed.
    """
    try:
        path = _session_file_path(session_id)
        return path is not None and path.exists()
    except Exception:
        if a2_session_history_save_errors_total is not None:
            a2_session_history_save_errors_total.labels(**_LABELS).inc()
        return False

AGENT_NAME = os.environ.get("AGENT_NAME", "a2-claude")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "claude")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
MCP_CONFIG_PATH = os.environ.get("MCP_CONFIG_PATH", "/home/agent/.claude/mcp.json")
AGENT_MD = os.environ.get("AGENT_MD", "/home/agent/.claude/CLAUDE.md")

_DEFAULT_TOOLS = "Read,Write,Edit,Bash,Glob,Grep,WebSearch,WebFetch"
ALLOWED_TOOLS: list[str] = [t.strip() for t in os.environ.get("ALLOWED_TOOLS", _DEFAULT_TOOLS).split(",") if t.strip()]

CONTEXT_USAGE_WARN_THRESHOLD = float(os.environ.get("CONTEXT_USAGE_WARN_THRESHOLD", "0.9"))
MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Maximum number of bytes of prompt text included in INFO-level log messages.
# Set to 0 to suppress prompt text from logs entirely; set higher for more context.
LOG_PROMPT_MAX_BYTES = int(os.environ.get("LOG_PROMPT_MAX_BYTES", "200"))

_BACKEND_ID = "claude"
_LABELS = {"agent": AGENT_OWNER, "agent_id": AGENT_ID, "backend": _BACKEND_ID}

CLAUDE_MODEL = os.environ.get("CLAUDE_MODEL") or None
CLAUDE_CREDENTIAL = (
    os.environ.get("CLAUDE_CODE_OAUTH_TOKEN")
    or os.environ.get("ANTHROPIC_API_KEY")
    or None
)
CLAUDE_AUTH_ENV = (
    "CLAUDE_CODE_OAUTH_TOKEN" if os.environ.get("CLAUDE_CODE_OAUTH_TOKEN")
    else "ANTHROPIC_API_KEY" if os.environ.get("ANTHROPIC_API_KEY")
    else None
)


def _load_agent_md() -> str:
    try:
        with open(AGENT_MD) as f:
            return f.read()
    except OSError:
        return ""


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


async def _log_tool_event(event_type: str, block, session_id: str, model: str | None = None) -> None:
    try:
        ts = datetime.now(timezone.utc).isoformat()
        if event_type == "tool_use":
            entry = {
                "ts": ts, "agent": AGENT_NAME, "agent_id": AGENT_ID,
                "session_id": session_id, "event_type": event_type,
                "model": model, "id": block.id, "name": block.name, "input": block.input,
            }
        else:
            entry = {
                "ts": ts, "agent": AGENT_NAME, "agent_id": AGENT_ID,
                "session_id": session_id, "event_type": event_type,
                "model": model, "tool_use_id": block.tool_use_id,
                "content": block.content, "is_error": block.is_error,
            }
        await log_trace(json.dumps(entry))
    except Exception as e:
        logger.error(f"_log_tool_event error: {e}")


def _load_mcp_config() -> dict:
    if not os.path.exists(MCP_CONFIG_PATH):
        return {}
    try:
        with open(MCP_CONFIG_PATH) as f:
            return json.load(f)
    except Exception as e:
        if a2_mcp_config_errors_total is not None:
            a2_mcp_config_errors_total.labels(**_LABELS).inc()
        logger.warning(f"Failed to load MCP config from {MCP_CONFIG_PATH}: {e}")
        return {}


def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    if session_id in sessions:
        sessions.move_to_end(session_id)
        sessions[session_id] = time.monotonic()
    else:
        if len(sessions) >= MAX_SESSIONS:
            _evicted_id, last_used_at = sessions.popitem(last=False)
            if a2_session_evictions_total is not None:
                a2_session_evictions_total.labels(**_LABELS).inc()
            if a2_session_age_seconds is not None:
                a2_session_age_seconds.labels(**_LABELS).observe(time.monotonic() - last_used_at)
            # Remove the on-disk session file for the evicted session so that
            # disk space is reclaimed and a future request for the same session
            # ID starts with a clean slate rather than stale history (#368).
            _evicted_path = _session_file_path(_evicted_id)
            if _evicted_path is not None:
                try:
                    _evicted_path.unlink(missing_ok=True)
                except OSError as _e:
                    logger.warning("Failed to remove evicted session file %r: %s", _evicted_path, _e)
        sessions[session_id] = time.monotonic()
    if a2_active_sessions is not None:
        a2_active_sessions.labels(**_LABELS).set(len(sessions))
    if a2_lru_cache_utilization_percent is not None:
        a2_lru_cache_utilization_percent.labels(**_LABELS).set(len(sessions) / MAX_SESSIONS * 100)


def _make_options(
    session_id: str,
    resume: bool,
    stderr_fn,
    mcp_servers: dict,
    model: str | None = None,
    agent_md_content: str = "",
) -> ClaudeAgentOptions:
    env: dict | None = None
    if CLAUDE_CREDENTIAL and CLAUDE_AUTH_ENV:
        env = {CLAUDE_AUTH_ENV: CLAUDE_CREDENTIAL}

    system_prompt = f"Your name is {AGENT_NAME}. Your session ID is {session_id}."
    if agent_md_content:
        system_prompt = f"{agent_md_content}\n\nYour session ID is {session_id}."

    return ClaudeAgentOptions(
        allowed_tools=ALLOWED_TOOLS,
        system_prompt=system_prompt,
        resume=session_id if resume else None,
        session_id=session_id if not resume else None,
        stderr=stderr_fn,
        mcp_servers=mcp_servers,
        model=model or CLAUDE_MODEL,
        **({"env": env} if env else {}),
    )


async def _run_query_inner(
    prompt: str,
    options: ClaudeAgentOptions,
    session_id: str,
    effective_model: str | None = None,
    max_tokens: int | None = None,
) -> list[str]:
    _sdk_labels = {**_LABELS, "model": effective_model or ""}
    collected: list[str] = []
    _query_start = time.monotonic()
    _message_count = 0
    _tool_names: dict[str, str] = {}
    _tool_start_times: dict[str, float] = {}
    _last_total_tokens = 0
    _session_start = time.monotonic()
    _assistant_turn_count = 0

    try:
        _spawn_start = time.monotonic()
        async with ClaudeSDKClient(options=options) as client:
            if a2_sdk_subprocess_spawn_duration_seconds is not None:
                a2_sdk_subprocess_spawn_duration_seconds.labels(**_sdk_labels).observe(time.monotonic() - _spawn_start)
            await client.query(prompt)
            _query_sent_at = time.monotonic()
            async for message in client.receive_response():
                _message_count += 1
                if isinstance(message, AssistantMessage):
                    if _assistant_turn_count == 0:
                        if a2_sdk_time_to_first_message_seconds is not None:
                            a2_sdk_time_to_first_message_seconds.labels(**_sdk_labels).observe(time.monotonic() - _query_sent_at)
                    _assistant_turn_count += 1
                    for block in message.content:
                        if isinstance(block, TextBlock):
                            collected.append(block.text)
                            await log_entry("agent", block.text, session_id, model=effective_model)
                        elif isinstance(block, ToolUseBlock):
                            _tool_names[block.id] = block.name
                            _tool_start_times[block.id] = time.monotonic()
                            if a2_sdk_tool_calls_total is not None:
                                a2_sdk_tool_calls_total.labels(**_LABELS, tool=block.name).inc()
                            if a2_sdk_tool_call_input_size_bytes is not None:
                                a2_sdk_tool_call_input_size_bytes.labels(**_LABELS, tool=block.name).observe(
                                    len(json.dumps(block.input).encode())
                                )
                            await _log_tool_event("tool_use", block, session_id, model=effective_model)
                        elif isinstance(block, ToolResultBlock):
                            tool_name = _tool_names.get(block.tool_use_id, "unknown")
                            if block.is_error and a2_sdk_tool_errors_total is not None:
                                a2_sdk_tool_errors_total.labels(**_LABELS, tool=tool_name).inc()
                            _t_start = _tool_start_times.pop(block.tool_use_id, None)
                            if _t_start is not None and a2_sdk_tool_duration_seconds is not None:
                                a2_sdk_tool_duration_seconds.labels(**_LABELS, tool=tool_name).observe(
                                    time.monotonic() - _t_start
                                )
                            if a2_sdk_tool_result_size_bytes is not None:
                                a2_sdk_tool_result_size_bytes.labels(**_LABELS, tool=tool_name).observe(
                                    len(str(block.content).encode())
                                )
                            await _log_tool_event("tool_result", block, session_id, model=effective_model)
                    try:
                        usage = await client.get_context_usage()
                        pct = usage.get("percentage", 0.0)
                        _last_total_tokens = usage.get("totalTokens", 0)
                        if a2_context_tokens is not None:
                            a2_context_tokens.labels(**_LABELS).observe(_last_total_tokens)
                        if a2_context_tokens_remaining is not None:
                            a2_context_tokens_remaining.labels(**_LABELS).observe(
                                usage.get("maxTokens", 0) - _last_total_tokens
                            )
                        if a2_context_usage_percent is not None:
                            a2_context_usage_percent.labels(**_LABELS).observe(pct)
                        if pct >= 100 and a2_context_exhaustion_total is not None:
                            a2_context_exhaustion_total.labels(**_LABELS).inc()
                        if pct >= CONTEXT_USAGE_WARN_THRESHOLD * 100:
                            if a2_context_warnings_total is not None:
                                a2_context_warnings_total.labels(**_LABELS).inc()
                            logger.warning(
                                f"Session {session_id!r}: context usage {pct:.1f}% "
                                f"exceeds threshold {CONTEXT_USAGE_WARN_THRESHOLD * 100:.0f}%"
                            )
                        if max_tokens is not None and _last_total_tokens >= max_tokens:
                            if a2_budget_exceeded_total is not None:
                                a2_budget_exceeded_total.labels(**_LABELS).inc()
                            raise BudgetExceededError(_last_total_tokens, max_tokens, list(collected))
                    except BudgetExceededError:
                        raise
                    except Exception as e:
                        if a2_sdk_context_fetch_errors_total is not None:
                            a2_sdk_context_fetch_errors_total.labels(**_sdk_labels).inc()
                        logger.warning(f"Session {session_id!r}: get_context_usage failed: {e}")
                elif isinstance(message, ResultMessage) and message.is_error:
                    if a2_sdk_result_errors_total is not None:
                        a2_sdk_result_errors_total.labels(**_sdk_labels).inc()
                    if a2_sdk_query_error_duration_seconds is not None:
                        a2_sdk_query_error_duration_seconds.labels(**_sdk_labels).observe(time.monotonic() - _query_start)
                    error_parts = list(message.errors or [])
                    if not error_parts and message.result:
                        error_parts = [message.result]
                    raise RuntimeError("\n".join(error_parts) if error_parts else "Claude SDK returned an error result with no details")
    except (OSError, ConnectionError):
        if a2_sdk_client_errors_total is not None:
            a2_sdk_client_errors_total.labels(**_sdk_labels).inc()
        if a2_sdk_query_error_duration_seconds is not None:
            a2_sdk_query_error_duration_seconds.labels(**_sdk_labels).observe(time.monotonic() - _query_start)
        raise
    finally:
        if a2_sdk_session_duration_seconds is not None:
            a2_sdk_session_duration_seconds.labels(**_sdk_labels).observe(time.monotonic() - _session_start)

    if a2_sdk_query_duration_seconds is not None:
        a2_sdk_query_duration_seconds.labels(**_sdk_labels).observe(time.monotonic() - _query_start)
    if a2_sdk_messages_per_query is not None:
        a2_sdk_messages_per_query.labels(**_sdk_labels).observe(_message_count)
    if a2_sdk_tokens_per_query is not None:
        a2_sdk_tokens_per_query.labels(**_sdk_labels).observe(_last_total_tokens)
    if a2_sdk_tool_calls_per_query is not None:
        a2_sdk_tool_calls_per_query.labels(**_sdk_labels).observe(len(_tool_names))
    if a2_sdk_turns_per_query is not None:
        a2_sdk_turns_per_query.labels(**_sdk_labels).observe(_assistant_turn_count)
    if a2_text_blocks_per_query is not None:
        a2_text_blocks_per_query.labels(**_sdk_labels).observe(len(collected))
    return collected


async def run_query(
    prompt: str,
    session_id: str,
    is_new: bool,
    mcp_servers: dict,
    agent_md_content: str,
    model: str | None = None,
    max_tokens: int | None = None,
) -> list[str]:
    stderr_lines: list[str] = []
    _query_start = time.monotonic()

    def capture_stderr(line: str) -> None:
        stderr_lines.append(line)
        if a2_sdk_errors_total is not None:
            a2_sdk_errors_total.labels(**_LABELS).inc()
        logger.error(f"[claude stderr] {line}")

    effective_model = model or CLAUDE_MODEL
    try:
        return await _run_query_inner(
            prompt,
            _make_options(session_id, resume=not is_new, stderr_fn=capture_stderr, mcp_servers=mcp_servers, model=model, agent_md_content=agent_md_content),
            session_id,
            effective_model=effective_model,
            max_tokens=max_tokens,
        )
    except BudgetExceededError:
        raise
    except Exception:
        _collision_lines = [
            line for line in stderr_lines
            if "session" in line.lower() and "already in use" in line.lower()
        ]
        if is_new and _collision_lines:
            logger.warning(
                f"Session {session_id!r}: session-ID collision detected on new session "
                f"(stderr: {_collision_lines[0]!r}) — retrying as resume."
            )
            if a2_task_retries_total is not None:
                a2_task_retries_total.labels(**_LABELS).inc()
            if a2_sdk_query_error_duration_seconds is not None:
                a2_sdk_query_error_duration_seconds.labels(**_LABELS, model=effective_model or "").observe(time.monotonic() - _query_start)
            return await _run_query_inner(
                prompt,
                _make_options(session_id, resume=True, stderr_fn=capture_stderr, mcp_servers=mcp_servers, model=model, agent_md_content=agent_md_content),
                session_id,
                effective_model=effective_model,
                max_tokens=max_tokens,
            )
        raise
    finally:
        if a2_stderr_lines_per_task is not None:
            a2_stderr_lines_per_task.labels(**_LABELS).observe(len(stderr_lines))
        if stderr_lines and a2_tasks_with_stderr_total is not None:
            a2_tasks_with_stderr_total.labels(**_LABELS).inc()


async def run(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    mcp_servers: dict,
    agent_md_content: str,
    model: str | None = None,
    max_tokens: int | None = None,
) -> str:
    if a2_concurrent_queries is not None:
        a2_concurrent_queries.labels(**_LABELS).inc()
    try:
        return await _run_inner(prompt, session_id, sessions, mcp_servers, agent_md_content, model, max_tokens)
    finally:
        if a2_concurrent_queries is not None:
            a2_concurrent_queries.labels(**_LABELS).dec()


async def _run_inner(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    mcp_servers: dict,
    agent_md_content: str,
    model: str | None = None,
    max_tokens: int | None = None,
) -> str:
    resolved_model = model or CLAUDE_MODEL or "default"
    if a2_model_requests_total is not None:
        a2_model_requests_total.labels(**_LABELS, model=resolved_model).inc()

    is_new = session_id not in sessions and not await asyncio.to_thread(_session_file_exists, session_id)
    if not is_new and a2_session_idle_seconds is not None:
        _last_used = sessions.get(session_id)
        if _last_used is not None:
            a2_session_idle_seconds.labels(**_LABELS).observe(time.monotonic() - _last_used)
    if a2_session_starts_total is not None:
        a2_session_starts_total.labels(**_LABELS, type="new" if is_new else "resumed").inc()

    _prompt_preview = prompt[:LOG_PROMPT_MAX_BYTES] + ("[truncated]" if len(prompt) > LOG_PROMPT_MAX_BYTES else "") if LOG_PROMPT_MAX_BYTES > 0 else "[redacted]"
    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) — prompt: {_prompt_preview!r}")
    await log_entry("user", prompt, session_id, model=model)

    if a2_prompt_length_bytes is not None:
        a2_prompt_length_bytes.labels(**_LABELS).observe(len(prompt.encode()))

    _start = time.monotonic()
    _budget_exceeded = False
    try:
        collected = await asyncio.wait_for(
            run_query(prompt, session_id, is_new, mcp_servers, agent_md_content, model=model, max_tokens=max_tokens),
            timeout=TASK_TIMEOUT_SECONDS,
        )
        _track_session(sessions, session_id)
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: timed out after {TASK_TIMEOUT_SECONDS}s.")
        # Remove the session from the LRU cache on timeout. The SDK context
        # manager is cancelled mid-stream, so the session state may be
        # inconsistent. Evicting it ensures the next call starts a fresh
        # session rather than trying to resume a potentially broken one.
        sessions.pop(session_id, None)
        # Also remove the on-disk session file so the next request for this
        # session_id starts with empty history rather than reloading the
        # potentially stale or mid-stream snapshot written before the timeout.
        _timeout_path = _session_file_path(session_id)
        if _timeout_path is not None:
            try:
                _timeout_path.unlink(missing_ok=True)
                logger.info("Removed stale session file for timed-out session %r", session_id)
            except OSError as _e:
                logger.warning("Could not remove session file for timed-out session %r: %s", session_id, _e)
        if a2_tasks_total is not None:
            a2_tasks_total.labels(**_LABELS, status="timeout").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.labels(**_LABELS).set(time.time())
        raise
    except BudgetExceededError as _bexc:
        _budget_exceeded = True
        logger.warning(f"Session {session_id!r}: {_bexc} — returning partial response.")
        await log_entry(
            "system",
            f"Budget exceeded: {_bexc.total} tokens used of {_bexc.limit} limit.",
            session_id,
            model=model,
        )
        collected = _bexc.collected
        _track_session(sessions, session_id)
    except Exception:
        if a2_tasks_total is not None:
            a2_tasks_total.labels(**_LABELS, status="error").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.labels(**_LABELS).set(time.time())
        raise

    if a2_tasks_total is not None:
        a2_tasks_total.labels(**_LABELS, status="budget_exceeded" if _budget_exceeded else "success").inc()
    if a2_task_last_success_timestamp_seconds is not None:
        a2_task_last_success_timestamp_seconds.labels(**_LABELS).set(time.time())
    if a2_task_duration_seconds is not None:
        a2_task_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
    if a2_task_timeout_headroom_seconds is not None:
        a2_task_timeout_headroom_seconds.labels(**_LABELS).observe(TASK_TIMEOUT_SECONDS - (time.monotonic() - _start))

    response = "\n\n".join(collected) if collected else ""
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
        self._mcp_servers: dict = {}
        self._agent_md_content: str = _load_agent_md()
        self._mcp_watcher_tasks: list[asyncio.Task] = []

    def _mcp_watchers(self):
        """Return callables for MCP config and agent.md watching."""
        return [self.mcp_config_watcher, self.agent_md_watcher]

    async def close(self) -> None:
        """Cancel and drain all MCP watcher tasks."""
        for task in self._mcp_watcher_tasks:
            task.cancel()
        if self._mcp_watcher_tasks:
            await asyncio.gather(*self._mcp_watcher_tasks, return_exceptions=True)
        self._mcp_watcher_tasks.clear()

    async def mcp_config_watcher(self) -> None:
        self._mcp_servers = await asyncio.to_thread(_load_mcp_config)
        if a2_mcp_servers_active is not None:
            a2_mcp_servers_active.labels(**_LABELS).set(len(self._mcp_servers))
        if self._mcp_servers:
            logger.info(f"MCP config loaded: {list(self._mcp_servers.keys())}")

        watch_dir = os.path.dirname(MCP_CONFIG_PATH)
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("MCP config directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in awatch(watch_dir):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="mcp").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(MCP_CONFIG_PATH):
                        self._mcp_servers = await asyncio.to_thread(_load_mcp_config)
                        if a2_mcp_servers_active is not None:
                            a2_mcp_servers_active.labels(**_LABELS).set(len(self._mcp_servers))
                        logger.info(f"MCP config reloaded: {list(self._mcp_servers.keys())}")
                        if a2_mcp_config_reloads_total is not None:
                            a2_mcp_config_reloads_total.labels(**_LABELS).inc()
                        break
            logger.warning("MCP config directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="mcp").inc()
            await asyncio.sleep(10)

    async def agent_md_watcher(self) -> None:
        """Watch AGENT_MD for changes and hot-reload agent identity / behavioral instructions (#371).

        This ensures that updating agent.md does not require a container restart,
        consistent with all other file-based configuration in the platform.
        """
        # Perform an initial load so the watcher starts with current content.
        self._agent_md_content = _load_agent_md()
        logger.info("agent.md loaded from %s", AGENT_MD)

        watch_dir = os.path.dirname(os.path.abspath(AGENT_MD))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("agent.md directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in awatch(watch_dir):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="agent_md").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(AGENT_MD):
                        self._agent_md_content = _load_agent_md()
                        logger.info("agent.md reloaded from %s", AGENT_MD)
                        break
            logger.warning("agent.md directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="agent_md").inc()
            await asyncio.sleep(10)

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
        _max_tokens_raw = metadata.get("max_tokens")
        max_tokens: int | None = None
        if _max_tokens_raw is not None:
            try:
                max_tokens = int(_max_tokens_raw)
            except (ValueError, TypeError):
                logger.warning(f"Session {session_id!r}: invalid max_tokens in metadata {_max_tokens_raw!r}, ignoring.")
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
                self._mcp_servers,
                self._agent_md_content,
                model=model,
                max_tokens=max_tokens,
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

import asyncio
import json
import logging
import os
import subprocess
import time
import uuid
from collections import OrderedDict
from datetime import datetime, timezone

from a2a.server.agent_execution import AgentExecutor as A2AAgentExecutor
from a2a.server.agent_execution import RequestContext
from a2a.server.events import EventQueue
from a2a.utils import new_agent_text_message
from agents import Agent, ComputerTool, LocalShellCommandRequest, LocalShellTool, Runner, RunConfig, SQLiteSession, WebSearchTool
from agents.items import ToolCallItem, ToolCallOutputItem
from computer import PlaywrightComputer
from agents.models.multi_provider import MultiProvider
from metrics import (
    a2_a2a_last_request_timestamp_seconds,
    a2_a2a_request_duration_seconds,
    a2_a2a_requests_total,
    a2_active_sessions,
    a2_budget_exceeded_total,
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
    a2_session_history_save_errors_total,
    a2_tasks_total,
    a2_text_blocks_per_query,
    a2_watcher_events_total,
    a2_file_watcher_restarts_total,
)

from log_utils import _append_log
from exceptions import BudgetExceededError

logger = logging.getLogger(__name__)


AGENT_NAME = os.environ.get("AGENT_NAME", "a2-codex")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "codex")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
AGENT_MD = os.environ.get("AGENT_MD", "/home/agent/.codex/AGENTS.md")
CODEX_SESSION_DB = os.environ.get("CODEX_SESSION_DB", "/home/agent/logs/codex_sessions.db")

CODEX_CONFIG_TOML = os.environ.get("CODEX_CONFIG_TOML", "/home/agent/.codex/config.toml")

MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Maximum number of bytes of prompt text included in INFO-level log messages.
# Set to 0 to suppress prompt text from logs entirely; set higher for more context.
LOG_PROMPT_MAX_BYTES = int(os.environ.get("LOG_PROMPT_MAX_BYTES", "200"))

CODEX_MODEL = os.environ.get("CODEX_MODEL") or "gpt-5.1-codex"
OPENAI_API_KEY: str | None = os.environ.get("OPENAI_API_KEY") or None

_BACKEND_ID = "codex"
_LABELS = {"agent": AGENT_OWNER, "agent_id": AGENT_ID, "backend": _BACKEND_ID}


# Env var keys that must not be overridden by caller-supplied values because
# they influence binary resolution or dynamic-linker / interpreter behavior
# and could be used for privilege escalation or code injection.
_SHELL_ENV_DENYLIST: frozenset[str] = frozenset({
    "PATH",
    "LD_PRELOAD",
    "LD_LIBRARY_PATH",
    "LD_AUDIT",
    "LD_DEBUG",
    "PYTHONPATH",
    "PYTHONSTARTUP",
    "PYTHONINSPECT",
    "RUBYLIB",
    "RUBYOPT",
    "PERL5LIB",
    "PERL5OPT",
    "NODE_PATH",
    "DYLD_INSERT_LIBRARIES",
    "DYLD_LIBRARY_PATH",
    "DYLD_FRAMEWORK_PATH",
})


async def _shell_executor(req: LocalShellCommandRequest) -> str:
    cmd = req.data.action.command
    cwd = req.data.action.working_directory or None
    env_extra = req.data.action.env or {}
    # Strip keys that could be used to hijack binary resolution or loader
    # behavior before merging caller-supplied values into the subprocess env.
    sanitized_extra = {k: v for k, v in env_extra.items() if k not in _SHELL_ENV_DENYLIST}
    rejected = set(env_extra) - set(sanitized_extra)
    if rejected:
        logger.warning("_shell_executor: stripped dangerous env vars from caller-supplied env: %s", sorted(rejected))
    _base_env = {k: os.environ[k] for k in ("PATH", "HOME", "USER", "TMPDIR", "LANG", "LC_ALL") if k in os.environ}
    env = {**_base_env, **sanitized_extra}
    timeout_ms = req.data.action.timeout_ms
    timeout_s = (timeout_ms / 1000.0) if timeout_ms else 30.0
    try:
        result = await asyncio.to_thread(
            subprocess.run,
            cmd,
            capture_output=True,
            text=True,
            timeout=timeout_s,
            cwd=cwd,
            env=env,
        )
        out = result.stdout
        if result.returncode != 0 and result.stderr:
            out += result.stderr
        return out
    except subprocess.TimeoutExpired:
        return f"Command timed out after {timeout_s}s"
    except Exception as exc:
        return f"Shell error: {exc}"


def _load_tool_config() -> dict:
    """Read [tools] table from config.toml. Returns empty dict if file absent or unparseable."""
    try:
        import tomllib
    except ImportError:
        try:
            import tomli as tomllib  # type: ignore
        except ImportError:
            return {}
    try:
        with open(CODEX_CONFIG_TOML, "rb") as f:
            data = tomllib.load(f)
        return data.get("tools", {})
    except Exception as exc:
        logger.warning("Could not read tool config from %s: %s", CODEX_CONFIG_TOML, exc)
        return {}


_computer: PlaywrightComputer | None = None
# _computer_lock is initialized lazily on first use inside _build_tools() so that
# it is always created within a running event loop.  A module-level asyncio.Lock()
# created before asyncio.run() is entered causes a DeprecationWarning in Python
# 3.10+ and may attach to the wrong event loop in Python 3.12+ (#378).
_computer_lock: asyncio.Lock | None = None

# Models known to support computer_use_preview
_COMPUTER_SUPPORTED_MODELS = {"computer-use-preview"}


async def _build_tools(model: str) -> list:
    global _computer, _computer_lock
    cfg = _load_tool_config()
    tools = []
    if cfg.get("shell", False):
        tools.append(LocalShellTool(executor=_shell_executor))
    if cfg.get("web_search", False):
        tools.append(WebSearchTool())
    if cfg.get("computer", False) and model in _COMPUTER_SUPPORTED_MODELS:
        if _computer_lock is None:
            _computer_lock = asyncio.Lock()
        async with _computer_lock:
            if _computer is None:
                _computer = PlaywrightComputer()
        tools.append(ComputerTool(computer=_computer))
    return tools


def _sqlite_session_exists(session_id: str) -> bool:
    """Check whether a session already has history in CODEX_SESSION_DB.

    Uses a direct sqlite3 query against the agent_sessions table so that
    after a process restart we correctly identify sessions that exist on disk
    even though the in-memory LRU cache is empty.  Returns False if the
    database file does not exist yet or if any error occurs.
    """
    import sqlite3
    db_path = CODEX_SESSION_DB
    if db_path == ":memory:" or not db_path:
        return False
    import os as _os
    if not _os.path.exists(db_path):
        return False
    try:
        conn = sqlite3.connect(db_path, check_same_thread=False)
        try:
            cursor = conn.execute(
                "SELECT 1 FROM agent_sessions WHERE session_id = ? LIMIT 1",
                (session_id,),
            )
            return cursor.fetchone() is not None
        finally:
            conn.close()
    except Exception:
        return False


def _delete_sqlite_session(session_id: str, db_path: str) -> None:
    """Delete a session row from the SQLite session database (blocking I/O).

    Intended to be called via asyncio.to_thread() so the event loop is not
    stalled by SQLite I/O during timeout cleanup (#361).
    """
    import sqlite3 as _sqlite3
    conn = _sqlite3.connect(db_path, check_same_thread=False)
    try:
        conn.execute("DELETE FROM agent_sessions WHERE session_id = ?", (session_id,))
        conn.commit()
    finally:
        conn.close()


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
        sessions[session_id] = time.monotonic()
    if a2_active_sessions is not None:
        a2_active_sessions.labels(**_LABELS).set(len(sessions))
    if a2_lru_cache_utilization_percent is not None:
        a2_lru_cache_utilization_percent.labels(**_LABELS).set(len(sessions) / MAX_SESSIONS * 100)


async def run_query(
    prompt: str,
    session_id: str,
    agent_md_content: str,
    model: str | None = None,
    max_tokens: int | None = None,
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
        tools=await _build_tools(resolved_model),
    )

    try:
        session = SQLiteSession(session_id, CODEX_SESSION_DB)
    except Exception as _sess_exc:
        logger.error("Failed to initialise SQLiteSession for %r: %s", session_id, _sess_exc)
        if a2_session_history_save_errors_total is not None:
            a2_session_history_save_errors_total.labels(**_LABELS).inc()
        session = None

    run_config = RunConfig(model_provider=MultiProvider(openai_api_key=OPENAI_API_KEY)) if OPENAI_API_KEY else None

    collected: list[str] = []
    _query_start = time.monotonic()
    _session_start = time.monotonic()
    _first_chunk_at: float | None = None
    _turn_count = 0
    _message_count = 0
    _tool_call_names: dict[str, str] = {}  # call_id -> tool name
    _total_tokens = 0

    try:
        result = Runner.run_streamed(codex_agent, prompt, session=session, run_config=run_config)
        async for event in result.stream_events():
            _message_count += 1
            if event.type == "raw_response_event":
                data = getattr(event, "data", None)
                delta = getattr(data, "delta", None)
                if delta and hasattr(delta, "text") and delta.text:
                    if _first_chunk_at is None:
                        _first_chunk_at = time.monotonic()
                        if a2_sdk_time_to_first_message_seconds is not None:
                            a2_sdk_time_to_first_message_seconds.labels(**_LABELS, model=resolved_model).observe(
                                _first_chunk_at - _query_start
                            )
                    collected.append(delta.text)
                # Check usage on response events (final chunk carries usage object)
                _usage = getattr(data, "usage", None)
                if _usage is not None:
                    _candidate = getattr(_usage, "total_tokens", None)
                    if _candidate is not None:
                        _total_tokens = max(_total_tokens, int(_candidate))
                        if max_tokens is not None and _total_tokens >= max_tokens:
                            if a2_budget_exceeded_total is not None:
                                a2_budget_exceeded_total.labels(**_LABELS).inc()
                            raise BudgetExceededError(_total_tokens, max_tokens, list(collected))
            elif event.type == "agent_updated_stream_event":
                _turn_count += 1
            elif event.type == "run_item_stream_event":
                item = event.item
                if isinstance(item, ToolCallItem):
                    raw = item.raw_item
                    call_id = getattr(raw, "call_id", None) or getattr(raw, "id", None) or ""
                    name = getattr(raw, "name", None) or getattr(raw, "type", "unknown")
                    # For local_shell, extract command as input
                    if hasattr(raw, "action") and hasattr(raw.action, "command"):
                        tool_input = {"command": raw.action.command}
                    else:
                        args_str = getattr(raw, "arguments", None)
                        if args_str:
                            try:
                                tool_input = json.loads(args_str)
                            except Exception:
                                tool_input = {"arguments": args_str}
                        else:
                            tool_input = {}
                    _tool_call_names[call_id] = name
                    try:
                        ts = datetime.now(timezone.utc).isoformat()
                        entry = {
                            "ts": ts,
                            "agent": AGENT_NAME, "agent_id": AGENT_ID,
                            "session_id": session_id,
                            "event_type": "tool_use",
                            "model": resolved_model,
                            "id": call_id,
                            "name": name,
                            "input": tool_input,
                        }
                        await log_trace(json.dumps(entry))
                    except Exception as e:
                        logger.error(f"log_trace tool_use error: {e}")
                elif isinstance(item, ToolCallOutputItem):
                    raw = item.raw_item
                    call_id = raw.get("call_id", "") if isinstance(raw, dict) else getattr(raw, "call_id", "")
                    tool_name = _tool_call_names.get(call_id, "unknown")
                    output = item.output
                    content = str(output)
                    is_error = bool(
                        getattr(item, "is_error", None)
                        or (isinstance(raw, dict) and raw.get("is_error"))
                    )
                    try:
                        ts = datetime.now(timezone.utc).isoformat()
                        entry = {
                            "ts": ts,
                            "agent": AGENT_NAME, "agent_id": AGENT_ID,
                            "session_id": session_id,
                            "event_type": "tool_result",
                            "model": resolved_model,
                            "tool_use_id": call_id,
                            "name": tool_name,
                            "content": content,
                            "is_error": is_error,
                        }
                        await log_trace(json.dumps(entry))
                    except Exception as e:
                        logger.error(f"log_trace tool_result error: {e}")
    except BudgetExceededError as exc:
        if a2_sdk_session_duration_seconds is not None:
            a2_sdk_session_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                time.monotonic() - _session_start
            )
        partial_response = "".join(exc.collected)
        if partial_response:
            await log_entry("agent", partial_response, session_id, model=resolved_model)
        raise
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

    # Prefer final_output as the SDK's authoritative answer when it is available.
    # Streaming deltas may represent intermediate or partial content during tool-call
    # turns; final_output is always the completed response the SDK intends to return.
    # Fall back to streamed collected content only when final_output is absent (#381).
    final = getattr(result, "final_output", None)
    if final and isinstance(final, str):
        if not collected:
            collected.append(final)
        else:
            streamed = "".join(collected)
            if final != streamed:
                logger.debug(
                    "final_output differs from streamed deltas — using streamed content "
                    "(len streamed=%d, len final=%d)",
                    len(streamed),
                    len(final),
                )

    full_response = "".join(collected)
    if full_response:
        await log_entry("agent", full_response, session_id, model=resolved_model)

    if a2_sdk_query_duration_seconds is not None:
        a2_sdk_query_duration_seconds.labels(**_LABELS, model=resolved_model).observe(time.monotonic() - _query_start)
    if a2_sdk_messages_per_query is not None:
        a2_sdk_messages_per_query.labels(**_LABELS, model=resolved_model).observe(_message_count)
    if a2_sdk_turns_per_query is not None:
        a2_sdk_turns_per_query.labels(**_LABELS, model=resolved_model).observe(_turn_count)
    if a2_text_blocks_per_query is not None:
        a2_text_blocks_per_query.labels(**_LABELS, model=resolved_model).observe(len(collected))

    # Log a trace entry for the completed turn
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
    max_tokens: int | None = None,
) -> str:
    if a2_concurrent_queries is not None:
        a2_concurrent_queries.labels(**_LABELS).inc()
    try:
        return await _run_inner(prompt, session_id, sessions, agent_md_content, model, max_tokens)
    finally:
        if a2_concurrent_queries is not None:
            a2_concurrent_queries.labels(**_LABELS).dec()


async def _run_inner(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    agent_md_content: str,
    model: str | None = None,
    max_tokens: int | None = None,
) -> str:
    resolved_model = model or CODEX_MODEL
    if a2_model_requests_total is not None:
        a2_model_requests_total.labels(**_LABELS, model=resolved_model).inc()

    is_new = session_id not in sessions and not await asyncio.to_thread(_sqlite_session_exists, session_id)
    if not is_new and a2_session_idle_seconds is not None:
        _last_used = sessions.get(session_id)
        if _last_used is not None:
            a2_session_idle_seconds.labels(**_LABELS).observe(time.monotonic() - _last_used)
    if a2_session_starts_total is not None:
        a2_session_starts_total.labels(**_LABELS, type="new" if is_new else "resumed").inc()

    _prompt_preview = prompt[:LOG_PROMPT_MAX_BYTES] + ("[truncated]" if len(prompt) > LOG_PROMPT_MAX_BYTES else "") if LOG_PROMPT_MAX_BYTES > 0 else "[redacted]"
    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) — prompt: {_prompt_preview!r}")
    await log_entry("user", prompt, session_id, model=resolved_model)

    if a2_prompt_length_bytes is not None:
        a2_prompt_length_bytes.labels(**_LABELS).observe(len(prompt.encode()))

    _start = time.monotonic()
    _budget_exceeded = False
    try:
        collected = await asyncio.wait_for(
            run_query(prompt, session_id, agent_md_content, model=model, max_tokens=max_tokens),
            timeout=TASK_TIMEOUT_SECONDS,
        )
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: timed out after {TASK_TIMEOUT_SECONDS}s.")
        # Evict the session from the LRU cache on timeout. The underlying
        # SQLiteSession may be in an inconsistent state after a mid-stream
        # cancellation; removing it ensures the next call for this session_id
        # starts fresh rather than attempting to resume a broken session.
        sessions.pop(session_id, None)
        # Also remove the SQLite history row so the next request for this
        # session_id starts with empty history rather than reloading the
        # potentially stale snapshot stored before the timeout.
        # Run in a thread to avoid blocking the event loop with SQLite I/O (#361).
        _db_path = CODEX_SESSION_DB
        if _db_path and _db_path != ":memory:":
            try:
                await asyncio.to_thread(_delete_sqlite_session, session_id, _db_path)
                logger.info("Removed stale SQLite session for timed-out session %r", session_id)
            except Exception as _e:
                logger.warning("Could not remove SQLite session for timed-out session %r: %s", session_id, _e)
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
            model=resolved_model,
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

    if not _budget_exceeded:
        _track_session(sessions, session_id)
    if a2_tasks_total is not None:
        a2_tasks_total.labels(**_LABELS, status="budget_exceeded" if _budget_exceeded else "success").inc()
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
        """Return callables for agent.md watching (#371)."""
        return [self.agent_md_watcher]

    async def agent_md_watcher(self) -> None:
        """Watch AGENT_MD for changes and hot-reload agent identity / behavioral instructions (#371).

        This ensures that updating agent.md does not require a container restart,
        consistent with all other file-based configuration in the platform.
        """
        from watchfiles import awatch as _awatch

        # Perform an initial load so the watcher starts with current content.
        self._agent_md_content = _load_agent_md()
        logger.info("agent.md loaded from %s", AGENT_MD)

        watch_dir = os.path.dirname(os.path.abspath(AGENT_MD))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("agent.md directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in _awatch(watch_dir):
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

    async def close(self) -> None:
        """Cancel and drain all MCP watcher tasks."""
        for task in self._mcp_watcher_tasks:
            task.cancel()
        if self._mcp_watcher_tasks:
            await asyncio.gather(*self._mcp_watcher_tasks, return_exceptions=True)
        self._mcp_watcher_tasks.clear()

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

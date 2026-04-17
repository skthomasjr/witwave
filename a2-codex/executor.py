import asyncio
import json
import logging
import os
import re
import subprocess
import time
import uuid
from collections import OrderedDict
from contextlib import AsyncExitStack
from datetime import datetime, timezone
from typing import Awaitable, Callable

from a2a.server.agent_execution import AgentExecutor as A2AAgentExecutor
from a2a.server.agent_execution import RequestContext
from a2a.server.events import EventQueue
from a2a.utils import new_agent_text_message
from agents import Agent, ComputerTool, LocalShellCommandRequest, LocalShellTool, Runner, RunConfig, SQLiteSession, WebSearchTool
from agents.items import ToolCallItem, ToolCallOutputItem
from computer import BrowserPool, PlaywrightComputer
from agents.models.multi_provider import MultiProvider
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
    a2_log_bytes_total,
    a2_log_entries_total,
    a2_log_write_errors_total,
    a2_lru_cache_utilization_percent,
    a2_model_requests_total,
    a2_prompt_length_bytes,
    a2_response_length_bytes,
    a2_running_tasks,
    a2_sdk_messages_per_query,
    a2_sdk_client_errors_total,
    a2_sdk_errors_total,
    a2_sdk_query_duration_seconds,
    a2_sdk_query_error_duration_seconds,
    a2_sdk_result_errors_total,
    a2_sdk_session_duration_seconds,
    a2_sdk_time_to_first_message_seconds,
    a2_sdk_tool_call_input_size_bytes,
    a2_sdk_tool_calls_per_query,
    a2_sdk_tool_calls_total,
    a2_sdk_tool_duration_seconds,
    a2_sdk_tool_errors_total,
    a2_sdk_tool_result_size_bytes,
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
    a2_mcp_config_errors_total,
    a2_mcp_config_reloads_total,
    a2_mcp_servers_active,
    a2_streaming_events_emitted_total,
    a2_sdk_tokens_per_query,
    a2_text_blocks_per_query,
    a2_watcher_events_total,
    a2_file_watcher_restarts_total,
    a2_codex_hooks_denials_total,
    a2_tool_audit_entries_total,
)

from log_utils import _append_log
from exceptions import BudgetExceededError
from validation import parse_max_tokens
from otel import start_span, set_span_error

logger = logging.getLogger(__name__)


AGENT_NAME = os.environ.get("AGENT_NAME", "a2-codex")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "codex")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
AGENT_MD = "/home/agent/.codex/AGENTS.md"
CODEX_SESSION_DB = os.environ.get("CODEX_SESSION_DB", "/home/agent/logs/codex_sessions.db")

CODEX_CONFIG_TOML = os.environ.get("CODEX_CONFIG_TOML", "/home/agent/.codex/config.toml")
# MCP server config — same wire format as a2-claude's mcp.json so users can
# share the file shape between backends. Codex mounts the .codex/ tree by
# default, so the path differs (#432).
MCP_CONFIG_PATH = os.environ.get("MCP_CONFIG_PATH", "/home/agent/.codex/mcp.json")

MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Per-chunk timeout for the streaming on_chunk callback. Bounds the SDK event
# loop's wait on a slow A2A consumer so token-budget enforcement and SDK
# iteration are never stalled by backpressure on a single delivery. On timeout
# the chunk is logged and dropped; iteration continues (#539).
STREAM_CHUNK_TIMEOUT_SECONDS = float(os.environ.get("STREAM_CHUNK_TIMEOUT_SECONDS", "5"))
# Percent of max_tokens at which a context-window warning metric is incremented.
# Tunable via env so operators can dial sensitivity without patching the
# binary. Matches the a2-claude knob (#459).
CONTEXT_USAGE_WARN_THRESHOLD = float(os.environ.get("CONTEXT_USAGE_WARN_THRESHOLD", "0.8"))
# Maximum number of bytes of prompt text included in INFO-level log messages.
# Set to 0 to suppress prompt text from logs entirely; set higher for more context.
LOG_PROMPT_MAX_BYTES = int(os.environ.get("LOG_PROMPT_MAX_BYTES", "200"))

CODEX_MODEL = os.environ.get("CODEX_MODEL") or "gpt-5.1-codex"
OPENAI_API_KEY: str | None = os.environ.get("OPENAI_API_KEY") or None


def _resolve_model_label(model: str | None) -> str:
    """Resolve a non-empty model label for observability (metrics + spans).

    Falls back to the module-load ``CODEX_MODEL`` default, then to the sentinel
    ``"unknown"`` if both are empty. Using ``"unknown"`` (not ``""``) keeps
    Prometheus series and OTel span attributes filterable in dashboards and
    avoids phantom empty-string label values (#570).
    """
    return model or CODEX_MODEL or "unknown"

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


TOOL_AUDIT_LOG = os.environ.get(
    "TOOL_AUDIT_LOG", "/home/agent/logs/tool-audit.jsonl"
)


# PreToolUse deny baseline for LocalShellTool (#586, shell-only scope).
# Mirrors a2-claude's shell baseline. `pattern` is compiled once at module
# load; `rule` name matches a2-claude's a2_hooks_denials_total{rule=...}
# label convention for cross-backend dashboard parity.
_SHELL_DENY_RULES: tuple[tuple[str, "re.Pattern[str]", str], ...] = (
    (
        "baseline-rm-rf-root",
        re.compile(r"\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)\s+(/|/\*|~|\$HOME|/\$)", re.IGNORECASE),
        "rm -rf of /, ~, $HOME, or / glob",
    ),
    (
        "baseline-git-force-push-main",
        re.compile(r"\bgit\s+push\b.*\b--force\b.*\b(main|master)\b|\bgit\s+push\b.*\b(main|master)\b.*--force\b", re.IGNORECASE),
        "git force-push to main/master",
    ),
    (
        "baseline-curl-pipe-shell",
        re.compile(r"\bcurl\b[^|]*\|\s*(sh|bash|zsh|python3?)\b", re.IGNORECASE),
        "curl | sh / bash / python pipeline",
    ),
    (
        "baseline-chmod-777",
        re.compile(r"\bchmod\b\s+(-R\s+)?[0-7]*777\b"),
        "chmod 777",
    ),
    (
        "baseline-dd-device",
        re.compile(r"\bdd\b.*\bof=/dev/(sd|nvme|hd|xvd)", re.IGNORECASE),
        "dd of=/dev/<block-device>",
    ),
)


def _evaluate_shell_baseline(cmd_parts: list[str]) -> tuple[str, str] | None:
    """Return (rule, reason) for the first matching baseline rule, else None.

    ``cmd_parts`` is the argv list as supplied by the SDK. Joined on single
    spaces before matching so the regex authors don't need to guess quoting.
    """
    joined = " ".join(cmd_parts)
    for rule, pattern, reason in _SHELL_DENY_RULES:
        if pattern.search(joined):
            return rule, reason
    return None


def _append_tool_audit(entry: dict) -> None:
    """Append a JSON line to tool-audit.jsonl; swallow errors.

    Mirrors a2-claude's audit writer shape: one JSON object per line with
    a monotonic timestamp, tool name, decision (allow|deny), command, and
    reason when denied. Best-effort — a full disk or missing parent dir
    must not block the tool call.
    """
    try:
        os.makedirs(os.path.dirname(TOOL_AUDIT_LOG), exist_ok=True)
        with open(TOOL_AUDIT_LOG, "a") as f:
            f.write(json.dumps(entry) + "\n")
    except Exception:
        pass
    tool = str(entry.get("tool") or "")
    if a2_tool_audit_entries_total is not None:
        try:
            a2_tool_audit_entries_total.labels(**_LABELS, tool=tool).inc()
        except Exception:
            pass


async def _shell_executor(req: LocalShellCommandRequest) -> str:
    # tool.call child span (#630) — the LocalShellTool invocation path. Kept
    # around the full executor body so baseline-deny short-circuits and
    # subprocess.run are both inside the span, not just the allowed branch.
    with start_span(
        "tool.call",
        kind="internal",
        attributes={"tool.name": "LocalShell"},
    ) as _tool_span:
        try:
            return await _shell_executor_inner(req)
        except BaseException as _exc:
            set_span_error(_tool_span, _exc)
            raise


async def _shell_executor_inner(req: LocalShellCommandRequest) -> str:
    cmd = req.data.action.command
    cwd = req.data.action.working_directory or None
    env_extra = req.data.action.env or {}

    # PreToolUse deny baseline (#586 shell-only).
    _denied = _evaluate_shell_baseline(cmd)
    if _denied is not None:
        rule, reason = _denied
        if a2_codex_hooks_denials_total is not None:
            try:
                a2_codex_hooks_denials_total.labels(**_LABELS, rule=rule).inc()
            except Exception:
                pass
        _append_tool_audit({
            "ts": time.time(),
            "tool": "LocalShell",
            "decision": "deny",
            "rule": rule,
            "reason": reason,
            "command": cmd,
        })
        logger.warning("_shell_executor: baseline deny rule=%s cmd=%r", rule, cmd)
        return f"Command blocked by shell baseline rule '{rule}': {reason}"

    # Audit allowed commands too so the log is a complete forensic trail.
    _append_tool_audit({
        "ts": time.time(),
        "tool": "LocalShell",
        "decision": "allow",
        "command": cmd,
    })

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


def _load_mcp_config() -> dict:
    """Load and normalise the MCP server config from MCP_CONFIG_PATH (#432).

    Accepts both the Claude-native shape (`{"mcpServers": {...}}`) and a flat
    `{server_name: {...}}` dict, returning the inner dict in both cases.
    Missing file is treated as "no MCP servers" (returns {}). Parse / I/O
    errors return {} AND increment a2_mcp_config_errors_total.
    """
    if not os.path.exists(MCP_CONFIG_PATH):
        return {}
    try:
        with open(MCP_CONFIG_PATH) as f:
            data = json.load(f)
        if isinstance(data, dict) and "mcpServers" in data and isinstance(data["mcpServers"], dict):
            return data["mcpServers"]
        if isinstance(data, dict):
            return data
        logger.warning("MCP config at %s is not a dict; ignoring.", MCP_CONFIG_PATH)
        return {}
    except Exception as e:
        if a2_mcp_config_errors_total is not None:
            a2_mcp_config_errors_total.labels(**_LABELS).inc()
        logger.warning("Failed to load MCP config from %s: %s", MCP_CONFIG_PATH, e)
        return {}


def _build_mcp_servers(mcp_config: dict) -> list:
    """Convert an MCP config dict into OpenAI Agents SDK MCPServer instances (#432).

    Each entry's transport is detected from its shape:
    - has 'command' key  → MCPServerStdio (subprocess transport)
    - has 'url' key      → MCPServerStreamableHttp (preferred HTTP transport)

    Returned servers are NOT yet entered as context managers — the caller is
    responsible for entering them via AsyncExitStack before passing to
    Agent(mcp_servers=[...]). Each entry that fails to instantiate is logged
    and skipped so a single bad entry does not break unrelated MCP servers.
    """
    if not mcp_config:
        return []
    try:
        from agents.mcp import MCPServerStdio, MCPServerStreamableHttp
    except Exception as _imp_exc:
        logger.warning(
            "openai-agents SDK does not expose agents.mcp servers (%s); "
            "MCP support disabled — install a newer openai-agents.",
            _imp_exc,
        )
        return []

    servers = []
    for name, cfg in mcp_config.items():
        if not isinstance(cfg, dict):
            logger.warning("MCP server %r: config must be a dict; got %r — skipping.", name, type(cfg).__name__)
            continue
        try:
            if "command" in cfg:
                params = {"command": cfg["command"]}
                if "args" in cfg:
                    params["args"] = list(cfg["args"])
                if "env" in cfg:
                    # Apply the same loader/interpreter env denylist used by
                    # _shell_executor (#248) to MCP stdio env — MCPServerStdio
                    # spawns a subprocess with identical code-injection risk
                    # profile. Strip keys that could be used to hijack binary
                    # resolution or dynamic-linker / interpreter behavior
                    # before passing env to the SDK (#519).
                    raw_env = dict(cfg["env"])
                    sanitized_env = {k: v for k, v in raw_env.items() if k not in _SHELL_ENV_DENYLIST}
                    rejected = set(raw_env) - set(sanitized_env)
                    if rejected:
                        logger.warning(
                            "MCP server %r: stripped dangerous env vars from config env: %s",
                            name,
                            sorted(rejected),
                        )
                    params["env"] = sanitized_env
                if "cwd" in cfg:
                    params["cwd"] = cfg["cwd"]
                servers.append(MCPServerStdio(name=name, params=params))
            elif "url" in cfg:
                params = {"url": cfg["url"]}
                if "headers" in cfg:
                    params["headers"] = dict(cfg["headers"])
                servers.append(MCPServerStreamableHttp(name=name, params=params))
            else:
                logger.warning(
                    "MCP server %r: missing both 'command' and 'url'; cannot determine transport — skipping.",
                    name,
                )
        except Exception as _e:
            logger.warning("MCP server %r: failed to instantiate (%s); skipping.", name, _e)
    return servers


_browser_pool: BrowserPool | None = None
# _computer_lock is initialized in main() inside asyncio.run() so that it is
# always created within the running event loop.  A module-level asyncio.Lock()
# causes a DeprecationWarning in Python 3.10+ and wrong-loop attachment in
# Python 3.12+ (#378).  Do not set this at import time.
_computer_lock: asyncio.Lock | None = None

# Serialises the LRU evict/insert block in _track_session (#506). Without
# this, a concurrent caller can observe the shared OrderedDict in a
# transient under-capacity state between popitem(last=False) and the
# post-await sessions[session_id] = ... insertion — leading to
# over-eviction, mis-counted metrics, and redundant SQLite deletes. Lazily
# initialized on first use so it binds to the running event loop (same
# rationale as _computer_lock above; #378).
_sessions_lock: asyncio.Lock | None = None

# Models known to support computer_use_preview
_COMPUTER_SUPPORTED_MODELS = {"computer-use-preview"}


async def _build_tools(model: str, session_id: str, tool_config: dict | None = None) -> list:
    """Build the SDK tool list for one run.

    The ComputerTool is bound to a per-``session_id`` PlaywrightComputer
    acquired from the shared BrowserPool so cookies, localStorage,
    service workers, cache, and page state stay isolated between A2A
    sessions (#522).

    ``tool_config`` is the cached [tools] table from CODEX_CONFIG_TOML,
    maintained by ``AgentExecutor.tool_config_watcher`` (#561). If None
    (e.g. invoked outside a running AgentExecutor, such as in tests),
    falls back to a direct disk read for backwards compatibility.
    """
    global _browser_pool
    cfg = tool_config if tool_config is not None else _load_tool_config()
    tools = []
    if cfg.get("shell", False):
        tools.append(LocalShellTool(executor=_shell_executor))
    if cfg.get("web_search", False):
        tools.append(WebSearchTool())
    if cfg.get("computer", False) and model in _COMPUTER_SUPPORTED_MODELS:
        async with _computer_lock:
            if _browser_pool is None:
                _browser_pool = BrowserPool()
            pool = _browser_pool
        computer = await pool.acquire(session_id)
        tools.append(ComputerTool(computer=computer))
    return tools


async def _release_computer(session_id: str) -> None:
    """Release the per-session PlaywrightComputer, if any.

    Called when a session is evicted from the LRU cache, times out, or
    the executor shuts down. Closes the session's browser context
    (isolating its cookies/storage/service workers from any future
    session) while leaving the shared browser process running.
    """
    pool = _browser_pool
    if pool is None:
        return
    try:
        await pool.release(session_id)
    except Exception as _e:
        logger.warning(
            "Failed to release PlaywrightComputer for session %r: %s",
            session_id, _e,
        )


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
    except Exception as exc:
        logger.warning("_sqlite_session_exists(%r) failed: %s", session_id, exc)
        if a2_session_history_save_errors_total is not None:
            a2_session_history_save_errors_total.labels(**_LABELS).inc()
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


def _current_trace_id_hex() -> str | None:
    """Return the active OTel span's trace_id as hex, or None when no active span.

    Used to stamp ``trace_id`` on conversation.jsonl rows so external
    log-correlation tools can join the backend log with harness / downstream
    spans (#636). Returns None when OTel is disabled (invalid span context
    or zero trace_id) so old rows stay backward-compatible.
    """
    try:
        from opentelemetry import trace as _otel_trace

        span = _otel_trace.get_current_span()
        ctx = span.get_span_context()
        if not ctx or not ctx.is_valid or ctx.trace_id == 0:
            return None
        return _otel_trace.format_trace_id(ctx.trace_id)
    except Exception:
        return None


async def log_entry(role: str, text: str, session_id: str, model: str | None = None, tokens: int | None = None) -> None:
    try:
        entry = {
            "ts": datetime.now(timezone.utc).isoformat(),
            "agent": AGENT_NAME,
            "session_id": session_id,
            "role": role,
            "model": model,
            "tokens": tokens,
            "text": text,
        }
        # Stamp trace_id from the active OTel span so conversation rows can be
        # joined with backend/harness traces (#636). Absent when OTel is off.
        _tid = _current_trace_id_hex()
        if _tid is not None:
            entry["trace_id"] = _tid
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


async def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    # Serialise evict/insert on the shared OrderedDict so concurrent callers
    # (A2A execute() and the /mcp tools/call path both share
    # AgentExecutor._sessions) cannot interleave popitem(last=False) with
    # the post-await reinsertion (#506). The SQLite delete and browser
    # release run inside the lock so the invariant
    # `len(sessions) <= MAX_SESSIONS` and eviction-metric accuracy are
    # restored before any other coroutine observes the dict — consistent
    # with the #522 per-session computer-tool pool and the #526
    # lifespan-scoped MCP server lock (both of which also serialise
    # await-crossing critical sections over shared state).
    global _sessions_lock
    if _sessions_lock is None:
        _sessions_lock = asyncio.Lock()
    async with _sessions_lock:
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
                # Clean up the evicted session's SQLite record so the database does not
                # grow unboundedly as sessions cycle through the LRU cache (#415).
                # Run the delete in a thread pool so the event loop is not blocked
                # on slow/remote filesystems — same pattern the timeout-cleanup
                # path at line 766 uses, and consistent with a2-claude #426 (#450).
                _db = CODEX_SESSION_DB
                if _db and _db != ":memory:":
                    try:
                        await asyncio.to_thread(_delete_sqlite_session, _evicted_id, _db)
                    except Exception as _del_exc:
                        logger.warning("Could not delete evicted session %r from DB: %s", _evicted_id, _del_exc)
                # Release the per-session Playwright browser context, if any, so
                # cookies/localStorage/service workers from the evicted session do
                # not linger in memory (#522). Safe no-op when the session never
                # used the computer tool.
                await _release_computer(_evicted_id)
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
    on_chunk: Callable[[str], Awaitable[None]] | None = None,
    live_mcp_servers: list | None = None,
    tool_config: dict | None = None,
) -> list[str]:
    resolved_model = model or CODEX_MODEL
    log_dir = os.path.dirname(CODEX_SESSION_DB)
    if log_dir:
        os.makedirs(log_dir, exist_ok=True)

    instructions = f"Your name is {AGENT_NAME}. Your session ID is {session_id}."
    if agent_md_content:
        instructions = f"{agent_md_content}\n\nYour session ID is {session_id}."

    # MCP servers are entered once at backend lifespan start by
    # AgentExecutor._apply_mcp_config() and passed in live here (#526). Previous
    # behaviour spawned a fresh stdio subprocess on every request; servers now
    # persist across requests for performance and to allow stateful MCP servers
    # (e.g. kubeconfig / HTTP pool) to retain state between calls.
    _live_mcp_servers: list = list(live_mcp_servers or [])

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
    _tool_start_times: dict[str, float] = {}  # call_id -> monotonic start time
    _tool_call_count = 0
    _total_tokens = 0
    # Initialized before the try so the llm.request span's finally-close
    # handler always has a sentinel to test against even if Agent construction
    # fails (#630).
    _llm_ctx = None

    try:
        # MCP servers are owned by AgentExecutor's lifespan-scoped AsyncExitStack
        # (#526). We receive them already-entered; we do not enter or exit them
        # per request. The snapshot above is the caller's live list.
        codex_agent = Agent(
            name=AGENT_NAME,
            instructions=instructions,
            model=resolved_model,
            tools=await _build_tools(resolved_model, session_id, tool_config=tool_config),
            mcp_servers=_live_mcp_servers,
        )

        # llm.request child span (#630) — one per Runner event-loop entry.
        # Managed manually with a finally so we do not need to re-indent the
        # long stream loop below. The span stays open for the full streaming
        # iteration so nested tool.call / mcp.call spans parent under it.
        _llm_ctx = start_span(
            "llm.request",
            kind="client",
            attributes={"model": _resolve_model_label(resolved_model)},
        )
        _llm_ctx.__enter__()
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
                    # Stream the chunk to the A2A event_queue (#430). Set by
                    # execute(); None when MCP /mcp endpoint or non-streaming
                    # caller. Awaited directly so chunk events stay ordered
                    # on the wire and exceptions surface here. Errors are
                    # logged and swallowed so SDK iteration is never aborted.
                    # Wrapped in asyncio.wait_for so a slow/stuck A2A consumer
                    # cannot stall the SDK event loop, token-budget
                    # enforcement, or tool-call processing (#539). On timeout
                    # the chunk is dropped with a warning and iteration
                    # continues; the overall TASK_TIMEOUT_SECONDS still bounds
                    # total request time.
                    if on_chunk is not None:
                        try:
                            await asyncio.wait_for(
                                on_chunk(delta.text),
                                timeout=STREAM_CHUNK_TIMEOUT_SECONDS,
                            )
                        except asyncio.TimeoutError:
                            logger.warning(
                                "Session %r: on_chunk callback timed out after %.3fs; dropping chunk and continuing stream",
                                session_id,
                                STREAM_CHUNK_TIMEOUT_SECONDS,
                            )
                        except Exception as _e:
                            logger.warning("Session %r: on_chunk callback raised: %s", session_id, _e)
                # Check usage on response events — response.completed carries usage
                # in event.data.response (ResponseCompletedEvent.response = Response)
                _usage = getattr(data, "usage", None) or getattr(getattr(data, "response", None), "usage", None)
                if _usage is not None:
                    _candidate = getattr(_usage, "total_tokens", None) or getattr(_usage, "output_tokens", None)
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
                    _tool_start_times[call_id] = time.monotonic()
                    _tool_call_count += 1
                    if a2_sdk_tool_calls_total is not None:
                        a2_sdk_tool_calls_total.labels(**_LABELS, tool=name).inc()
                    if a2_sdk_tool_call_input_size_bytes is not None:
                        try:
                            _input_bytes = len(json.dumps(tool_input).encode())
                            a2_sdk_tool_call_input_size_bytes.labels(**_LABELS, tool=name).observe(_input_bytes)
                        except Exception:
                            pass
                    # tool.call / mcp.call span (#630). Emitted on the dispatch
                    # bookkeeping side so the call is visible in trace UIs as a
                    # child of llm.request. MCP tools surface via Agents SDK
                    # with a separate raw_item.type (e.g. "mcp_call"); fall
                    # back to name-prefix detection for the Claude-compatible
                    # "mcp__server__tool" convention.
                    _raw_type = getattr(raw, "type", "") or ""
                    _is_mcp = "mcp" in _raw_type.lower() or (isinstance(name, str) and name.startswith("mcp__"))
                    if _is_mcp:
                        _mcp_server = ""
                        _mcp_tool = name
                        if isinstance(name, str) and name.startswith("mcp__"):
                            _parts = name.split("__", 2)
                            _mcp_server = _parts[1] if len(_parts) > 1 else ""
                            _mcp_tool = _parts[2] if len(_parts) > 2 else name
                        _tool_ctx = start_span(
                            "mcp.call",
                            kind="client",
                            attributes={
                                "mcp.server": _mcp_server,
                                "mcp.tool": _mcp_tool,
                                "tool.name": name,
                            },
                        )
                    else:
                        _tool_ctx = start_span(
                            "tool.call",
                            kind="internal",
                            attributes={"tool.name": name},
                        )
                    _tool_ctx.__enter__()
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
                    finally:
                        try:
                            _tool_ctx.__exit__(None, None, None)
                        except Exception:
                            pass
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
                    _tool_elapsed = time.monotonic() - _tool_start_times.pop(call_id, time.monotonic())
                    if a2_sdk_tool_duration_seconds is not None:
                        a2_sdk_tool_duration_seconds.labels(**_LABELS, tool=tool_name).observe(_tool_elapsed)
                    if is_error and a2_sdk_tool_errors_total is not None:
                        a2_sdk_tool_errors_total.labels(**_LABELS, tool=tool_name).inc()
                    if a2_sdk_tool_result_size_bytes is not None:
                        a2_sdk_tool_result_size_bytes.labels(**_LABELS, tool=tool_name).observe(len(content.encode()))
    except BudgetExceededError as exc:
        if a2_sdk_session_duration_seconds is not None:
            a2_sdk_session_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                time.monotonic() - _session_start
            )
        partial_response = "".join(exc.collected)
        if partial_response:
            await log_entry("agent", partial_response, session_id, model=resolved_model, tokens=_total_tokens or None)
        raise
    except Exception as _run_exc:
        if a2_sdk_query_error_duration_seconds is not None:
            a2_sdk_query_error_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                time.monotonic() - _query_start
            )
        if a2_sdk_session_duration_seconds is not None:
            a2_sdk_session_duration_seconds.labels(**_LABELS, model=resolved_model).observe(
                time.monotonic() - _session_start
            )
        # Mark the llm.request span as errored so traces reflect the failure
        # even though we re-raise immediately (#630).
        try:
            _otel_cur = getattr(_llm_ctx, "_active_span", None)
            set_span_error(_otel_cur, _run_exc)
        except Exception:
            pass
        # Classify by exception type to match a2-claude's error metric surface
        # (#431). Best-effort — unknown exception types fall through to the
        # generic a2_sdk_errors_total counter.
        try:
            import openai as _openai
            if isinstance(_run_exc, _openai.APIConnectionError):
                if a2_sdk_client_errors_total is not None:
                    a2_sdk_client_errors_total.labels(**_LABELS, model=resolved_model).inc()
            elif isinstance(_run_exc, _openai.APIError):
                if a2_sdk_result_errors_total is not None:
                    a2_sdk_result_errors_total.labels(**_LABELS, model=resolved_model).inc()
            else:
                if a2_sdk_errors_total is not None:
                    a2_sdk_errors_total.labels(**_LABELS, model=resolved_model).inc()
        except Exception:
            if a2_sdk_errors_total is not None:
                a2_sdk_errors_total.labels(**_LABELS, model=resolved_model).inc()
        raise
    finally:
        # Close the llm.request span opened above (#630). Best-effort — if the
        # context manager was never entered (e.g. exception before its __enter__
        # call), swallow any error.
        if _llm_ctx is not None:
            try:
                _llm_ctx.__exit__(None, None, None)
            except Exception:
                pass

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
        await log_entry("agent", full_response, session_id, model=resolved_model, tokens=_total_tokens or None)

    if a2_sdk_query_duration_seconds is not None:
        a2_sdk_query_duration_seconds.labels(**_LABELS, model=resolved_model).observe(time.monotonic() - _query_start)
    if a2_sdk_messages_per_query is not None:
        a2_sdk_messages_per_query.labels(**_LABELS, model=resolved_model).observe(_message_count)
    if a2_sdk_turns_per_query is not None:
        a2_sdk_turns_per_query.labels(**_LABELS, model=resolved_model).observe(_turn_count)
    if a2_text_blocks_per_query is not None:
        a2_text_blocks_per_query.labels(**_LABELS, model=resolved_model).observe(len(collected))
    if a2_sdk_tokens_per_query is not None:
        a2_sdk_tokens_per_query.labels(**_LABELS, model=resolved_model).observe(_total_tokens)
    if a2_sdk_tool_calls_per_query is not None:
        a2_sdk_tool_calls_per_query.labels(**_LABELS).observe(_tool_call_count)
    if _total_tokens:
        if a2_context_tokens is not None:
            a2_context_tokens.labels(**_LABELS).observe(_total_tokens)
        if max_tokens:
            if a2_context_tokens_remaining is not None:
                a2_context_tokens_remaining.labels(**_LABELS).observe(max(0, max_tokens - _total_tokens))
            _pct = _total_tokens / max_tokens * 100
            if a2_context_usage_percent is not None:
                a2_context_usage_percent.labels(**_LABELS).observe(_pct)
            if _pct >= 100 and a2_context_exhaustion_total is not None:
                a2_context_exhaustion_total.labels(**_LABELS).inc()
            elif _pct >= CONTEXT_USAGE_WARN_THRESHOLD * 100 and a2_context_warnings_total is not None:
                a2_context_warnings_total.labels(**_LABELS).inc()

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
    on_chunk: Callable[[str], Awaitable[None]] | None = None,
    live_mcp_servers: list | None = None,
    tool_config: dict | None = None,
) -> str:
    if a2_concurrent_queries is not None:
        a2_concurrent_queries.labels(**_LABELS).inc()
    try:
        return await _run_inner(prompt, session_id, sessions, agent_md_content, model, max_tokens, on_chunk=on_chunk, live_mcp_servers=live_mcp_servers, tool_config=tool_config)
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
    on_chunk: Callable[[str], Awaitable[None]] | None = None,
    live_mcp_servers: list | None = None,
    tool_config: dict | None = None,
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
            run_query(prompt, session_id, agent_md_content, model=model, max_tokens=max_tokens, on_chunk=on_chunk, live_mcp_servers=live_mcp_servers, tool_config=tool_config),
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
        # Drop the per-session Playwright context so a later request reusing
        # this session_id does not inherit mid-stream browser state (#522).
        await _release_computer(session_id)
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
        await _track_session(sessions, session_id)
    except Exception:
        if a2_tasks_total is not None:
            a2_tasks_total.labels(**_LABELS, status="error").inc()
        if a2_task_error_duration_seconds is not None:
            a2_task_error_duration_seconds.labels(**_LABELS).observe(time.monotonic() - _start)
        if a2_task_last_error_timestamp_seconds is not None:
            a2_task_last_error_timestamp_seconds.labels(**_LABELS).set(time.time())
        raise

    if not _budget_exceeded:
        await _track_session(sessions, session_id)
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
        # MCP config dict loaded from MCP_CONFIG_PATH; populated and refreshed
        # by mcp_config_watcher() (#432).
        self._mcp_config: dict = {}
        # [tools] table from CODEX_CONFIG_TOML; populated and refreshed by
        # tool_config_watcher() (#561). Eliminates per-request TOML re-parse
        # in _build_tools; consistent with the mcp.json / AGENTS.md cache
        # pattern elsewhere in this module.
        self._tool_config: dict = {}
        self._mcp_watcher_tasks: list[asyncio.Task] = []
        # Lifespan-scoped MCP server stack (#526). MCP servers are entered
        # once at startup (or on hot-reload) and reused across requests,
        # eliminating per-request stdio subprocess spawn overhead. The lock
        # serialises reload-vs-request access to _live_mcp_servers so an
        # in-flight Agent(...) call never sees a half-torn-down server.
        self._mcp_stack: AsyncExitStack | None = None
        self._live_mcp_servers: list = []
        self._mcp_servers_lock: asyncio.Lock | None = None
        # Public idempotency flag — set to True after close() completes so
        # callers (e.g. main.py's lifespan) can safely avoid double-close of
        # shared resources like the module-level _browser_pool (#555).
        self.closed: bool = False

    def _mcp_watchers(self):
        """Return callables for AGENTS.md, mcp.json, and config.toml watching (#371, #432, #561)."""
        return [self.agent_md_watcher, self.mcp_config_watcher, self.tool_config_watcher]

    async def _apply_mcp_config(self, mcp_config: dict) -> None:
        """Enter the given MCP config into a fresh lifespan-scoped stack (#526).

        Tears down any previously-entered stack first, then enters each server
        as an async context manager. Failures on individual servers are logged
        and skipped so one broken entry does not prevent others from starting.
        The a2_mcp_servers_active gauge reflects the actually-running count,
        not the config-loaded count.
        """
        if self._mcp_servers_lock is None:
            self._mcp_servers_lock = asyncio.Lock()
        async with self._mcp_servers_lock:
            # Tear down the previous stack (if any) before entering the new one.
            if self._mcp_stack is not None:
                try:
                    await self._mcp_stack.aclose()
                except Exception as _close_exc:
                    logger.warning("Previous MCP stack aclose error: %s", _close_exc)
                self._mcp_stack = None
                self._live_mcp_servers = []

            new_stack = AsyncExitStack()
            await new_stack.__aenter__()
            new_live: list = []
            try:
                for _srv in _build_mcp_servers(mcp_config or {}):
                    # mcp.call child span (#630) — wraps the stdio transport
                    # bring-up so the trace shows which MCP server the stack
                    # is spinning up (or failing to). kind=client reflects
                    # that the backend is dialling an external server.
                    with start_span(
                        "mcp.call",
                        kind="client",
                        attributes={
                            "mcp.server": getattr(_srv, "name", "?") or "?",
                            "mcp.tool": "__start__",
                        },
                    ) as _mcp_span:
                        try:
                            _live = await new_stack.enter_async_context(_srv)
                            new_live.append(_live)
                        except Exception as _mcp_exc:
                            set_span_error(_mcp_span, _mcp_exc)
                            logger.warning(
                                "MCP server %r failed to start (%s); proceeding without it.",
                                getattr(_srv, "name", "?"), _mcp_exc,
                            )
            except Exception:
                # If the per-server loop itself raises something unexpected,
                # unwind the partial stack so we do not leak subprocesses.
                try:
                    await new_stack.aclose()
                except Exception:
                    pass
                raise

            self._mcp_stack = new_stack
            self._live_mcp_servers = new_live
            if a2_mcp_servers_active is not None:
                a2_mcp_servers_active.labels(**_LABELS).set(len(new_live))

    async def _snapshot_live_mcp_servers(self) -> list:
        """Return a defensive copy of the currently-live MCP server list (#526).

        Taken under the lock so a concurrent hot-reload cannot swap the list
        out from under the caller mid-read.
        """
        if self._mcp_servers_lock is None:
            self._mcp_servers_lock = asyncio.Lock()
        async with self._mcp_servers_lock:
            return list(self._live_mcp_servers)

    async def mcp_config_watcher(self) -> None:
        """Watch MCP_CONFIG_PATH for changes and hot-reload the MCP server config (#432, #526).

        Mirrors the a2-claude pattern: load on startup, then watch the parent
        directory for any changes to the config file. Each reload restarts the
        lifespan-scoped MCP server stack so stdio subprocesses are respawned
        cleanly under the new config and existing request traffic sees a
        consistent snapshot.
        """
        from watchfiles import awatch as _awatch

        # Initial load + first stack entry.
        self._mcp_config = await asyncio.to_thread(_load_mcp_config)
        if self._mcp_config:
            logger.info("MCP config loaded: %s", list(self._mcp_config.keys()))
        try:
            await self._apply_mcp_config(self._mcp_config)
        except Exception as _apply_exc:
            logger.warning("Initial MCP stack start failed: %s", _apply_exc)

        watch_dir = os.path.dirname(os.path.abspath(MCP_CONFIG_PATH))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("MCP config directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in _awatch(watch_dir, recursive=False):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="mcp").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(MCP_CONFIG_PATH):
                        self._mcp_config = await asyncio.to_thread(_load_mcp_config)
                        logger.info("MCP config reloaded: %s", list(self._mcp_config.keys()))
                        try:
                            await self._apply_mcp_config(self._mcp_config)
                        except Exception as _apply_exc:
                            logger.warning("MCP stack reload failed: %s", _apply_exc)
                        if a2_mcp_config_reloads_total is not None:
                            a2_mcp_config_reloads_total.labels(**_LABELS).inc()
                        break
            logger.warning("MCP config directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="mcp").inc()
            await asyncio.sleep(10)

    async def agent_md_watcher(self) -> None:
        """Watch AGENT_MD for changes and hot-reload agent identity / behavioral instructions (#371).

        This ensures that updating AGENTS.md does not require a container restart,
        consistent with all other file-based configuration in the platform.
        """
        from watchfiles import awatch as _awatch

        # Perform an initial load so the watcher starts with current content.
        self._agent_md_content = _load_agent_md()
        logger.info("AGENTS.md loaded from %s", AGENT_MD)

        watch_dir = os.path.dirname(os.path.abspath(AGENT_MD))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("AGENTS.md directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in _awatch(watch_dir):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="agent_md").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(AGENT_MD):
                        self._agent_md_content = _load_agent_md()
                        logger.info("AGENTS.md reloaded from %s", AGENT_MD)
                        break
            logger.warning("AGENTS.md directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="agent_md").inc()
            await asyncio.sleep(10)

    async def tool_config_watcher(self) -> None:
        """Watch CODEX_CONFIG_TOML for changes and hot-reload the [tools] table (#561).

        Mirrors the mcp_config_watcher / agent_md_watcher pattern: load once on
        startup into ``self._tool_config`` (used by ``_build_tools`` via
        ``run_query(tool_config=...)``), then watch the parent directory for any
        changes to the config file. Eliminates the per-request
        ``open + tomllib.load`` that previously ran on the hot path of every
        Agent construction.
        """
        from watchfiles import awatch as _awatch

        # Initial load so the cache is populated before the first request
        # arrives. Run in a thread so slow/remote filesystems do not stall the
        # event loop at startup (same rationale as mcp_config_watcher).
        self._tool_config = await asyncio.to_thread(_load_tool_config)
        logger.info("tool config loaded from %s: %s", CODEX_CONFIG_TOML, dict(self._tool_config))

        watch_dir = os.path.dirname(os.path.abspath(CODEX_CONFIG_TOML))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("tool config directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in _awatch(watch_dir, recursive=False):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="tool_config").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(CODEX_CONFIG_TOML):
                        self._tool_config = await asyncio.to_thread(_load_tool_config)
                        logger.info("tool config reloaded from %s: %s", CODEX_CONFIG_TOML, dict(self._tool_config))
                        break
            logger.warning("tool config directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="tool_config").inc()
            await asyncio.sleep(10)

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        _exec_start = time.monotonic()
        prompt = context.get_user_input()
        metadata = context.message.metadata or {}
        # OTel server span continuation (#469).
        from otel import extract_otel_context as _extract_ctx
        _tp = metadata.get("traceparent") if isinstance(metadata.get("traceparent"), str) else None
        _otel_parent = _extract_ctx({"traceparent": _tp}) if _tp else None
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
        # Shared parser lives in shared/validation.py (#537, #428, #555).
        max_tokens = parse_max_tokens(
            metadata.get("max_tokens"),
            logger=logger,
            source="A2A metadata",
            session_id=session_id,
        )
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
        # Streaming bridge (#430): forward each text delta to the A2A
        # event_queue as it arrives. Tracks emission count so the
        # post-completion aggregated enqueue can be skipped when chunks were
        # already delivered.
        _chunks_emitted = 0
        _streaming_label_model = _resolve_model_label(model)

        async def _emit_chunk(text: str) -> None:
            nonlocal _chunks_emitted
            _chunks_emitted += 1
            if a2_streaming_events_emitted_total is not None:
                a2_streaming_events_emitted_total.labels(**_LABELS, model=_streaming_label_model).inc()
            # Await directly — see a2-claude/executor.py _emit_chunk for the
            # rationale (event ordering + exception surfacing).
            await event_queue.enqueue_event(new_agent_text_message(text))

        from otel import start_span as _start_span, set_span_error as _set_span_error
        _otel_span = None
        try:
            with _start_span(
                "a2-codex.execute",
                kind="server",
                parent_context=_otel_parent,
                attributes={
                    "a2.session_id": session_id,
                    "a2.model": _resolve_model_label(model),
                    "a2.agent": AGENT_NAME,
                    "a2.agent_id": AGENT_ID,
                },
            ) as _otel_span:
                _response = await run(
                    prompt,
                    session_id,
                    self._sessions,
                    self._agent_md_content,
                    model=model,
                    max_tokens=max_tokens,
                    on_chunk=_emit_chunk,
                    live_mcp_servers=await self._snapshot_live_mcp_servers(),
                    tool_config=self._tool_config,
                )
                _success = True
                # Skip the final aggregated event when chunks were streamed —
                # they already delivered the content. Keep it as a fallback for
                # tool-only or non-streamed runs.
                if _response and _chunks_emitted == 0:
                    await event_queue.enqueue_event(new_agent_text_message(_response))
                if a2_a2a_requests_total is not None:
                    a2_a2a_requests_total.labels(**_LABELS, status="success").inc()
        except Exception as _exc:
            _error = repr(_exc)
            _set_span_error(_otel_span, _exc)
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
        """Cancel and drain all MCP watcher tasks, tear down the MCP server
        stack (#526), and close the Playwright computer.

        Idempotent: the public ``self.closed`` flag guards a second invocation
        so the shutdown path cannot double-close shared resources such as
        ``_browser_pool`` (#555).
        """
        if self.closed:
            return
        for task in self._mcp_watcher_tasks:
            task.cancel()
        if self._mcp_watcher_tasks:
            await asyncio.gather(*self._mcp_watcher_tasks, return_exceptions=True)
        self._mcp_watcher_tasks.clear()
        # Tear down the lifespan-scoped MCP stack so all stdio subprocesses and
        # HTTP sessions are released cleanly on shutdown. Done after the
        # watcher task is drained to avoid racing a concurrent reload.
        if self._mcp_stack is not None:
            try:
                await self._mcp_stack.aclose()
            except Exception as _close_exc:
                logger.warning("MCP stack aclose on shutdown: %s", _close_exc)
            self._mcp_stack = None
            self._live_mcp_servers = []
            if a2_mcp_servers_active is not None:
                a2_mcp_servers_active.labels(**_LABELS).set(0)
        global _browser_pool
        if _browser_pool is not None:
            try:
                await _browser_pool.close()
            except Exception as _e:
                logger.warning("Failed to close BrowserPool on shutdown: %s", _e)
            _browser_pool = None
        self.closed = True

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

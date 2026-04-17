import asyncio
import json
import logging
import os
import time
import uuid
from collections import OrderedDict
from contextlib import AsyncExitStack
from datetime import datetime, timezone
from typing import Any, Awaitable, Callable

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
    a2_budget_exceeded_total,
    a2_concurrent_queries,
    a2_context_exhaustion_total,
    a2_context_tokens,
    a2_context_tokens_remaining,
    a2_context_usage_percent,
    a2_context_warnings_total,
    a2_empty_responses_total,
    a2_hooks_config_errors_total,
    a2_hooks_config_reloads_total,
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
    a2_sdk_messages_per_query,
    a2_sdk_query_duration_seconds,
    a2_sdk_client_errors_total,
    a2_sdk_errors_total,
    a2_sdk_query_error_duration_seconds,
    a2_sdk_result_errors_total,
    a2_sdk_session_duration_seconds,
    a2_sdk_time_to_first_message_seconds,
    a2_sdk_tool_calls_total,
    a2_sdk_tool_duration_seconds,
    a2_sdk_turns_per_query,
    a2_session_age_seconds,
    a2_session_evictions_total,
    a2_session_history_save_errors_total,
    a2_session_idle_seconds,
    a2_session_starts_total,
    a2_task_cancellations_total,
    a2_task_duration_seconds,
    a2_task_error_duration_seconds,
    a2_task_last_error_timestamp_seconds,
    a2_task_last_success_timestamp_seconds,
    a2_task_timeout_headroom_seconds,
    a2_tasks_total,
    a2_streaming_events_emitted_total,
    a2_text_blocks_per_query,
    a2_watcher_events_total,
    a2_file_watcher_restarts_total,
)

# Hooks engine facade (#631). Imported even though the gemini tool-call path
# is not wired yet (blocked on #640) — this lands the infrastructure so the
# executor can register the hooks_config_watcher alongside existing watchers
# and #640 can drop `evaluate_pre_tool_use` in without further plumbing.
from hooks import (
    BASELINE_RULES,
    HOOKS_BASELINE_ENABLED,
    HOOKS_CONFIG_PATH,
    HookState,
    load_hooks_config_sync,
)

from log_utils import _append_log
from exceptions import BudgetExceededError
from validation import parse_max_tokens, sanitize_model_label
from otel import start_span, set_span_error

logger = logging.getLogger(__name__)


AGENT_NAME = os.environ.get("AGENT_NAME", "a2-gemini")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "gemini")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
AGENT_MD = "/home/agent/.gemini/GEMINI.md"
SESSION_STORE_DIR = os.environ.get("SESSION_STORE_DIR", "/home/agent/memory/sessions")

# Ensure the sessions directory exists once at module load time rather than
# on every _session_path() call.  This eliminates a redundant os.makedirs
# syscall on the hot path for every prompt (see #320).
try:
    os.makedirs(SESSION_STORE_DIR, exist_ok=True)
except OSError:
    pass  # read-only or not yet mounted — will fail naturally on first write

MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Maximum number of bytes of prompt text included in INFO-level log messages.
# Set to 0 to suppress prompt text from logs entirely; set higher for more context.
LOG_PROMPT_MAX_BYTES = int(os.environ.get("LOG_PROMPT_MAX_BYTES", "200"))

GEMINI_MODEL = os.environ.get("GEMINI_MODEL") or "gemini-2.5-pro"
GEMINI_API_KEY: str | None = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY") or None

MCP_CONFIG_PATH = os.environ.get("MCP_CONFIG_PATH", "/home/agent/.gemini/mcp.json")

# Env var keys that must not be overridden by caller-supplied MCP server env
# entries. Mirrors a2-codex (#519): MCP stdio entries spawn a subprocess with
# identical code-injection risk so keep the denylist symmetric. a2-gemini has
# no LocalShell path; this list is only used by ``_build_mcp_stdio_params``.
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

_BACKEND_ID = "gemini"
_LABELS = {"agent": AGENT_OWNER, "agent_id": AGENT_ID, "backend": _BACKEND_ID}

# Bounded allow-pattern for the Prometheus `model` label (#487, hoisted to
# ``shared/validation.py`` for reuse across backends in #601). User-supplied
# metadata.model flows through resolved_model into 12+ metric call sites; an
# unbounded string would let a caller blow up metric cardinality by sending a
# fresh UUID per request. The shared ``sanitize_model_label`` helper enforces
# the allow-pattern (alnum / dot / dash / underscore, length <= 64) and falls
# back to the literal "unknown". Keep the private ``_sanitize_model_label``
# alias so existing call sites stay on-pattern with other local helpers.
_sanitize_model_label = sanitize_model_label


def _load_agent_md() -> str:
    try:
        with open(AGENT_MD) as f:
            return f.read()
    except OSError:
        return ""


def _load_mcp_config() -> dict:
    """Load and normalise the MCP server config from MCP_CONFIG_PATH (#640).

    Accepts both the Claude-native shape (``{"mcpServers": {...}}``) and a
    flat ``{server_name: {...}}`` dict, returning the inner dict in both
    cases. Missing file is treated as "no MCP servers" (returns ``{}``).
    Parse / I/O errors return ``{}`` AND increment
    ``a2_mcp_config_errors_total``. Mirrors a2-codex._load_mcp_config for
    parity across backends.
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


def _build_mcp_stdio_params(name: str, cfg: dict) -> Any | None:
    """Construct an ``mcp.StdioServerParameters`` from a single config entry (#640).

    Applies the shared env denylist (#519) before passing ``env`` through to
    the subprocess so a malicious config cannot hijack dynamic-linker /
    interpreter resolution of the spawned MCP server. Returns ``None`` on
    malformed entries (logged and skipped by the caller).
    """
    try:
        from mcp import StdioServerParameters  # type: ignore
    except Exception as _imp_exc:
        logger.warning(
            "mcp package not available (%s); MCP support disabled.",
            _imp_exc,
        )
        return None
    if "command" not in cfg:
        logger.warning(
            "MCP server %r: missing 'command' (a2-gemini only supports stdio transport "
            "via google-genai AFC); skipping.",
            name,
        )
        return None
    params_kwargs: dict = {"command": cfg["command"]}
    if "args" in cfg:
        params_kwargs["args"] = list(cfg["args"])
    if "env" in cfg:
        raw_env = dict(cfg["env"])
        sanitized_env = {k: v for k, v in raw_env.items() if k not in _SHELL_ENV_DENYLIST}
        rejected = set(raw_env) - set(sanitized_env)
        if rejected:
            logger.warning(
                "MCP server %r: stripped dangerous env vars from config env: %s",
                name, sorted(rejected),
            )
        params_kwargs["env"] = sanitized_env
    if "cwd" in cfg:
        params_kwargs["cwd"] = cfg["cwd"]
    try:
        return StdioServerParameters(**params_kwargs)
    except Exception as _e:
        logger.warning("MCP server %r: failed to build stdio params (%s); skipping.", name, _e)
        return None


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


def _to_jsonable(value: Any) -> Any:
    """Best-effort coercion of SDK objects into json-serialisable structures.

    Used when we extract ``function_call.args`` and ``function_response.response``
    off the google-genai AFC history — the SDK returns pydantic models or
    their raw proto mirrors. Falls back to ``repr`` so logging never crashes
    on an unexpected shape (#640).
    """
    try:
        if hasattr(value, "model_dump"):
            return value.model_dump(exclude_none=True)
    except Exception:
        pass
    if isinstance(value, dict):
        return {k: _to_jsonable(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [_to_jsonable(v) for v in value]
    if isinstance(value, (str, int, float, bool)) or value is None:
        return value
    try:
        return repr(value)
    except Exception:
        return "<unrepr-able>"


async def _emit_afc_history(
    history: list,
    *,
    session_id: str,
    model: str | None,
) -> None:
    """Extract ``function_call`` / ``function_response`` parts from an AFC history
    and emit ``tool_use`` / ``tool_result`` trace rows + metrics (#640).

    google-genai's AFC appends both the user/assistant turns and the
    synthesised function_call / function_response turns to
    ``chat.history`` (and to ``response.automatic_function_calling_history``
    on non-streaming calls). This helper walks that flat list, pairs each
    ``function_call`` with the matching ``function_response`` by tool name,
    and writes one row per side into ``trace.jsonl`` using the same shape
    a2-claude uses so the dashboard TraceView (#592) and OTel trace viewer
    (#632) can render them uniformly.

    Errors here are logged and swallowed — observability must never break
    the response path.
    """
    if not history:
        return
    pending_calls: dict[str, dict] = {}
    call_counter = 0
    for content in history:
        parts = getattr(content, "parts", None) or []
        for part in parts:
            fc = getattr(part, "function_call", None)
            fr = getattr(part, "function_response", None)
            if fc is not None:
                call_counter += 1
                name = getattr(fc, "name", None) or "<unknown>"
                # Gemini function_call objects don't carry a stable id on
                # older SDK releases; synthesise one so the matching
                # tool_result row can reference it.
                call_id = getattr(fc, "id", None) or f"fc-{session_id[:8]}-{call_counter}"
                args = getattr(fc, "args", None)
                ts = datetime.now(timezone.utc).isoformat()
                entry = {
                    "ts": ts,
                    "event_type": "tool_use",
                    "id": call_id,
                    "name": name,
                    "input": _to_jsonable(args) if args is not None else {},
                    "session_id": session_id,
                    "agent": AGENT_NAME,
                    "model": model,
                }
                _tid = _current_trace_id_hex()
                if _tid is not None:
                    entry["trace_id"] = _tid
                try:
                    await log_trace(json.dumps(entry))
                except Exception as _e:
                    logger.debug("AFC tool_use log failed: %s", _e)
                pending_calls.setdefault(name, []).append({"id": call_id, "ts": ts})
            elif fr is not None:
                name = getattr(fr, "name", None) or "<unknown>"
                response = getattr(fr, "response", None)
                is_error = False
                # Best-effort error detection: google-genai surfaces tool
                # errors as a dict with an ``error`` key on the response.
                response_j = _to_jsonable(response) if response is not None else None
                if isinstance(response_j, dict) and "error" in response_j:
                    is_error = True
                # Pair with the most recent unmatched call of the same name.
                queue = pending_calls.get(name) or []
                matched = queue.pop(0) if queue else None
                tool_use_id = matched["id"] if matched else None
                ts = datetime.now(timezone.utc).isoformat()
                entry = {
                    "ts": ts,
                    "event_type": "tool_result",
                    "id": f"{tool_use_id}-resp" if tool_use_id else f"fr-{session_id[:8]}-{call_counter}",
                    "tool_use_id": tool_use_id,
                    "content": response_j,
                    "is_error": is_error,
                    "session_id": session_id,
                    "agent": AGENT_NAME,
                    "model": model,
                }
                _tid = _current_trace_id_hex()
                if _tid is not None:
                    entry["trace_id"] = _tid
                try:
                    await log_trace(json.dumps(entry))
                except Exception as _e:
                    logger.debug("AFC tool_result log failed: %s", _e)
                # Metrics: one sample per observed function_call (keyed on
                # the response side so status="error" / "success" is known).
                status = "error" if is_error else "success"
                if a2_sdk_tool_calls_total is not None:
                    a2_sdk_tool_calls_total.labels(**_LABELS, tool=name, status=status).inc()
                # Duration: the SDK does not expose per-call timings through
                # AFC history. Fall back to wall-clock delta between the
                # matched function_call row and this function_response row
                # — an approximation that still catches runaway tool calls.
                if matched is not None and a2_sdk_tool_duration_seconds is not None:
                    try:
                        _start = datetime.fromisoformat(matched["ts"])
                        _end = datetime.fromisoformat(ts)
                        _dur = max(0.0, (_end - _start).total_seconds())
                        a2_sdk_tool_duration_seconds.labels(**_LABELS, tool=name).observe(_dur)
                    except Exception:
                        pass


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
    return os.path.join(SESSION_STORE_DIR, f"{session_id}.json")


def _session_file_exists(session_id: str) -> bool:
    """Return True if a persisted session history file exists on disk for session_id.

    Used to detect resumed sessions after a process restart, when the in-memory
    LRU cache is empty but history exists on disk.  Always returns False if any
    error occurs so it never prevents a prompt from being processed.
    """
    try:
        return os.path.exists(_session_path(session_id))
    except Exception:
        return False


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


_SAVE_HISTORY_MAX_RETRIES = int(os.environ.get("GEMINI_SAVE_HISTORY_MAX_RETRIES", "3"))
_SAVE_HISTORY_BACKOFF_BASE = float(os.environ.get("GEMINI_SAVE_HISTORY_BACKOFF", "0.5"))
# Maximum number of turns to persist per session. Older turns are dropped so that
# per-turn save cost and file size stay bounded even for very long sessions (#349).
# Set to 0 to disable truncation (keep full history).
_SAVE_HISTORY_MAX_TURNS = int(os.environ.get("GEMINI_MAX_HISTORY_TURNS", "100"))


def _write_history_to_disk(tmp_path: str, path: str, raw: list) -> None:
    """Write serialized history to disk atomically (blocking I/O, run in a thread)."""
    with open(tmp_path, "w") as f:
        json.dump(raw, f)
    os.replace(tmp_path, path)


async def _save_history(session_id: str, history: list[types.Content]) -> None:
    """Persist conversation history for a session.

    Retries up to _SAVE_HISTORY_MAX_RETRIES times with exponential backoff on
    failure.  After all retries are exhausted, raises the exception so the
    caller can log it at ERROR level rather than silently discarding it.
    """
    path = _session_path(session_id)
    raw = []
    for content in history:
        parts = [p.model_dump(exclude_none=True) for p in (content.parts or []) if p]
        if parts:
            raw.append({"role": content.role, "parts": parts})
    if _SAVE_HISTORY_MAX_TURNS > 0 and len(raw) > _SAVE_HISTORY_MAX_TURNS:
        raw = raw[-_SAVE_HISTORY_MAX_TURNS:]
    tmp_path = path + ".tmp"
    last_exc: Exception | None = None
    for attempt in range(_SAVE_HISTORY_MAX_RETRIES):
        try:
            await asyncio.to_thread(_write_history_to_disk, tmp_path, path, raw)
            return
        except Exception as e:
            last_exc = e
            logger.warning(
                f"Failed to save session history for {session_id!r} "
                f"(attempt {attempt + 1}/{_SAVE_HISTORY_MAX_RETRIES}): {e}"
            )
            if attempt < _SAVE_HISTORY_MAX_RETRIES - 1:
                await asyncio.sleep(_SAVE_HISTORY_BACKOFF_BASE * (2 ** attempt))
    # All retries exhausted — raise so the caller can log at ERROR level.
    raise RuntimeError(
        f"Permanently failed to save session history for {session_id!r} "
        f"after {_SAVE_HISTORY_MAX_RETRIES} attempts"
    ) from last_exc


class _RefCountedLock:
    """An asyncio.Lock bundled with a waiter refcount (#483).

    The refcount is incremented by `_acquire_session_lock` BEFORE `async with
    lock` and decremented by `_release_session_lock` AFTER the lock is released.
    Eviction from ``session_locks`` is only permitted when the refcount reaches
    zero, which guarantees:

    - A task that has already looked up (or is about to acquire) a lock entry
      cannot have that entry silently replaced by a fresh ``asyncio.Lock``
      while it still holds — or is queued on — the original lock instance.
    - The #401 hygiene goal is preserved: once the last waiter is done, the
      entry is removed from the dict so idle session locks do not accumulate.
    """

    __slots__ = ("lock", "refcount")

    def __init__(self) -> None:
        self.lock: asyncio.Lock = asyncio.Lock()
        self.refcount: int = 0


def _acquire_session_lock(
    session_id: str, session_locks: dict[str, "_RefCountedLock"]
) -> "_RefCountedLock":
    """Return (and register a waiter on) the refcounted lock for ``session_id``.

    Must be paired with a ``_release_session_lock`` call in a ``finally`` so
    that eviction invariants are not violated on cancellation or error. This is
    safe to call without holding any async-level lock because ``session_locks``
    is mutated only from the single asyncio event loop thread; the refcount
    bump and dict insertion happen synchronously in one step.
    """
    entry = session_locks.get(session_id)
    if entry is None:
        entry = _RefCountedLock()
        session_locks[session_id] = entry
    entry.refcount += 1
    return entry


def _release_session_lock(
    session_id: str, session_locks: dict[str, "_RefCountedLock"]
) -> None:
    """Drop this task's reference; evict the dict entry when no waiters remain."""
    entry = session_locks.get(session_id)
    if entry is None:
        return
    entry.refcount -= 1
    if entry.refcount <= 0 and session_locks.get(session_id) is entry:
        session_locks.pop(session_id, None)


def _track_session(
    sessions: OrderedDict[str, float],
    session_id: str,
    session_locks: dict[str, "_RefCountedLock"],
    history_save_failed: set[str] | None = None,
) -> None:
    if session_id in sessions:
        sessions.move_to_end(session_id)
        sessions[session_id] = time.monotonic()
    else:
        if len(sessions) >= MAX_SESSIONS:
            _evicted_id, last_used_at = sessions.popitem(last=False)
            # Only evict the lock entry when no one holds or waits on it.
            # Otherwise the current holder's release path (_release_session_lock)
            # will remove it once refcount reaches zero. This preserves the
            # mutual-exclusion invariant under MAX_SESSIONS pressure (#483).
            _evicted_entry = session_locks.get(_evicted_id)
            if _evicted_entry is not None and _evicted_entry.refcount <= 0:
                session_locks.pop(_evicted_id, None)
            # Prune the evicted session from history_save_failed so the set
            # does not grow unbounded under sustained save failure (#485).
            # Mirrors the pop symmetry maintained for sessions/session_locks.
            if history_save_failed is not None:
                history_save_failed.discard(_evicted_id)
            if a2_session_evictions_total is not None:
                a2_session_evictions_total.labels(**_LABELS).inc()
            if a2_session_age_seconds is not None:
                a2_session_age_seconds.labels(**_LABELS).observe(time.monotonic() - last_used_at)
            _evicted_path = os.path.join(SESSION_STORE_DIR, f"{_evicted_id}.json")
            try:
                os.remove(_evicted_path)
            except FileNotFoundError:
                pass
            except OSError as e:
                logger.warning("Could not remove evicted session file %s: %s", _evicted_path, e)
        sessions[session_id] = time.monotonic()
    if a2_active_sessions is not None:
        a2_active_sessions.labels(**_LABELS).set(len(sessions))
    if a2_lru_cache_utilization_percent is not None:
        a2_lru_cache_utilization_percent.labels(**_LABELS).set(len(sessions) / MAX_SESSIONS * 100)


_genai_client: genai.Client | None = None


def _get_client() -> genai.Client:
    """Return the module-level genai.Client singleton, creating it on first call.

    The API key is read from the environment on each construction so that
    setting _genai_client = None and calling _get_client() again (e.g., in
    a future refresh path) will pick up the current key value rather than
    the value captured at module import time.

    Note: in standard deployments, API key changes require a process restart
    since the container environment is not updated in-place. Setting
    _genai_client = None alone is not sufficient unless the process environment
    is also updated (e.g., via a secrets-manager sidecar that mutates os.environ).
    """
    global _genai_client
    if _genai_client is None:
        key = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY") or None
        if not key:
            raise RuntimeError("No Gemini API key configured. Set GEMINI_API_KEY or GOOGLE_API_KEY.")
        _genai_client = genai.Client(api_key=key)
    return _genai_client


async def _close_client() -> None:
    """Dispose the module-level genai.Client singleton, if any.

    google-genai (>=1.20) does not expose a public close API on ``genai.Client``;
    the SDK's underlying ``BaseApiClient`` owns ``_httpx_client`` (sync) and
    ``_async_httpx_client`` (async) connection pools that otherwise linger
    until the process exits. Best-effort teardown: close whichever pools were
    actually instantiated and swallow any errors so shutdown is never blocked
    by a transport quirk. Resets the singleton so a later ``_get_client()``
    call will construct a fresh instance.
    """
    global _genai_client
    client = _genai_client
    if client is None:
        return
    try:
        api_client = getattr(client, "_api_client", None)
        if api_client is not None:
            async_httpx = getattr(api_client, "_async_httpx_client", None)
            if async_httpx is not None:
                aclose = getattr(async_httpx, "aclose", None)
                if aclose is not None:
                    try:
                        await aclose()
                    except Exception as e:  # pragma: no cover - defensive
                        logger.debug("genai async httpx client aclose failed: %s", e)
            sync_httpx = getattr(api_client, "_httpx_client", None)
            if sync_httpx is not None:
                close = getattr(sync_httpx, "close", None)
                if close is not None:
                    try:
                        close()
                    except Exception as e:  # pragma: no cover - defensive
                        logger.debug("genai sync httpx client close failed: %s", e)
    finally:
        _genai_client = None


async def run_query(
    prompt: str,
    session_id: str,
    agent_md_content: str,
    session_locks: dict[str, "_RefCountedLock"],
    history_save_failed: set[str] | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
    on_chunk: Callable[[str], Awaitable[None]] | None = None,
    live_mcp_servers: list | None = None,
) -> list[str]:
    resolved_model = model or GEMINI_MODEL
    # Note: resolved_model carries the raw caller-supplied string (so we pass
    # it faithfully to the SDK and log it verbatim). Wherever it lands in a
    # Prometheus label, pass it through _sanitize_model_label() so a hostile
    # caller cannot blow up metric cardinality (#487).

    instructions = f"Your name is {AGENT_NAME}. Your session ID is {session_id}."
    if agent_md_content:
        instructions = f"{agent_md_content}\n\nYour session ID is {session_id}."

    # Refcounted lock lookup (#483). The waiter is registered before we
    # block on the lock so the dict entry cannot be evicted out from under
    # us while we are queued — eviction is gated on refcount == 0.
    entry = _acquire_session_lock(session_id, session_locks)
    try:
        async with entry.lock:
            history = await asyncio.to_thread(_load_history, session_id)

            client = _get_client()

            # NOTE(#640): AFC-internal — hook enforcement requires disabling AFC;
            # see issue body option 2. The google-genai SDK's Automatic Function
            # Calling (AFC) runs the tool ping-pong inside ``generate_content``,
            # so a ``PreToolUse``-style ``evaluate_pre_tool_use`` call site here
            # cannot intercept MCP tool invocations without disabling AFC and
            # hand-rolling the loop. ``self._hook_state`` is still kept in sync
            # by ``hooks_config_watcher`` (#631) so that a future AFC-off path
            # can wire the engine in without further plumbing.

            # Build the GenerateContentConfig. When live MCP sessions are
            # attached (#640), pass them into ``tools=[...]`` — google-genai's
            # experimental MCP-as-tool support accepts raw ``ClientSession``
            # objects and handles the full function_call / function_response
            # ping-pong via AFC. See ``googleapis.github.io/python-genai``
            # for the current surface.
            _config_kwargs: dict = {"system_instruction": instructions}
            _live = list(live_mcp_servers or [])
            if _live:
                _config_kwargs["tools"] = list(_live)
            # Create chat with persisted history and system instruction
            chat = client.aio.chats.create(
                model=resolved_model,
                config=types.GenerateContentConfig(**_config_kwargs),
                history=history,
            )

            collected: list[str] = []
            _query_start = time.monotonic()
            _session_start = time.monotonic()
            _first_chunk_at: float | None = None
            _turn_count = 0
            _message_count = 0
            _total_tokens = 0

            # llm.request child span (#630) — one per generate_content /
            # send_message_stream round-trip. Managed via manual enter/exit so
            # the streaming loop body below does not need to be re-indented.
            # When ``live_mcp_servers`` is non-empty (#640), AFC may dispatch
            # an arbitrary number of MCP tool calls inside this single SDK
            # invocation without surfacing per-call hooks to the caller.
            # Emitting a child span per MCP tool call would require disabling
            # AFC; instead we stamp the ``mcp.sessions.count`` / ``tools.count``
            # attributes on this aggregate span so traces still record that an
            # AFC roundtrip happened and how many sessions were in scope. A
            # future AFC-off path can split this into per-call child spans.
            _llm_attrs: dict = {"model": _sanitize_model_label(resolved_model)}
            if _live:
                _llm_attrs["mcp.sessions.count"] = len(_live)
                _llm_attrs["tools.count"] = len(_live)
            _llm_ctx = start_span(
                "llm.request",
                kind="client",
                attributes=_llm_attrs,
            )
            _llm_ctx.__enter__()
            _llm_closed = False
            try:
                async for chunk in await chat.send_message_stream(prompt):
                    _message_count += 1
                    text = getattr(chunk, "text", None)
                    if text:
                        if _first_chunk_at is None:
                            _first_chunk_at = time.monotonic()
                            if a2_sdk_time_to_first_message_seconds is not None:
                                a2_sdk_time_to_first_message_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(
                                    _first_chunk_at - _query_start
                                )
                        collected.append(text)
                        # Stream the chunk to the A2A event_queue (#430). Set by
                        # execute(); None for non-streaming callers (e.g. /mcp).
                        # Awaited directly so events stay ordered and exceptions
                        # surface here. Errors swallowed so SDK iteration is never
                        # aborted.
                        if on_chunk is not None:
                            try:
                                await on_chunk(text)
                            except Exception as _e:
                                logger.warning("Session %r: on_chunk callback raised: %s", session_id, _e)
                    # Track token count and check budget on each chunk
                    _usage_meta = getattr(chunk, "usage_metadata", None)
                    _token_count = getattr(_usage_meta, "total_token_count", None)
                    if _token_count is not None:
                        _total_tokens = int(_token_count)
                    if max_tokens is not None and _token_count is not None and _total_tokens >= max_tokens:
                        if a2_budget_exceeded_total is not None:
                            a2_budget_exceeded_total.labels(**_LABELS).inc()
                        raise BudgetExceededError(_total_tokens, max_tokens, list(collected))
                _turn_count = 1
            except BudgetExceededError as exc:
                if a2_sdk_session_duration_seconds is not None:
                    a2_sdk_session_duration_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(
                        time.monotonic() - _session_start
                    )
                partial_response = "".join(exc.collected)
                if partial_response:
                    await log_entry("agent", partial_response, session_id, model=resolved_model, tokens=_total_tokens or None)
                # Do not persist chat.history here (#493). At this point the
                # history contains the user turn that triggered the aborted
                # call and, at best, a partial/implementation-defined model
                # turn appended by the google-genai SDK. Saving that would
                # leave the session in a state that either violates Gemini's
                # alternating user/model contract or resumes on incomplete
                # content on the next request. Instead, mark the session in
                # history_save_failed so the next request starts fresh —
                # same invariant the success-path handler maintains
                # (#437, #409). The prior on-disk history remains authoritative.
                if history_save_failed is not None:
                    history_save_failed.add(session_id)
                raise
            except Exception as _run_exc:
                if a2_sdk_query_error_duration_seconds is not None:
                    a2_sdk_query_error_duration_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(
                        time.monotonic() - _query_start
                    )
                if a2_sdk_session_duration_seconds is not None:
                    a2_sdk_session_duration_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(
                        time.monotonic() - _session_start
                    )
                # Classify by exception type so the new SDK error counters track
                # connection vs result vs catch-all failures (#445). Best-effort —
                # if the google.api_core import is unavailable, fall through to the
                # generic catch-all counter.
                try:
                    from google.api_core import exceptions as _g_exc
                    if isinstance(_run_exc, _g_exc.ClientError):
                        if a2_sdk_client_errors_total is not None:
                            a2_sdk_client_errors_total.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).inc()
                    elif isinstance(_run_exc, _g_exc.GoogleAPIError):
                        if a2_sdk_result_errors_total is not None:
                            a2_sdk_result_errors_total.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).inc()
                    else:
                        if a2_sdk_errors_total is not None:
                            a2_sdk_errors_total.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).inc()
                except Exception:
                    if a2_sdk_errors_total is not None:
                        a2_sdk_errors_total.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).inc()
                # Do not persist chat.history here (#499). The SDK may have
                # partially advanced chat.history to include the failed user
                # turn with no (or an incomplete) assistant response. Saving
                # that would leave the session violating Gemini's alternating
                # user/model contract or resuming on incomplete content on the
                # next request. Mirror the BudgetExceededError policy (#493):
                # skip the save and mark the session in history_save_failed so
                # the next request starts fresh. The prior on-disk history (if
                # any) remains authoritative; _run_inner treats save-failed
                # sessions as new (#409, #437).
                if history_save_failed is not None:
                    history_save_failed.add(session_id)
                raise
            finally:
                # Close the llm.request span (#630). Safe to call once in the
                # finally — the context manager swallows double-close and on
                # error paths the propagating exception is already recorded in
                # _sdk_errors metrics above.
                if not _llm_closed:
                    _llm_closed = True
                    try:
                        _llm_ctx.__exit__(None, None, None)
                    except Exception:
                        pass

            if a2_sdk_session_duration_seconds is not None:
                a2_sdk_session_duration_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(
                    time.monotonic() - _session_start
                )

            full_response = "".join(collected)
            if full_response:
                await log_entry("agent", full_response, session_id, model=resolved_model, tokens=_total_tokens or None)

            # AFC observability (#640): walk chat.history to surface any
            # function_call / function_response parts appended by the SDK
            # during this roundtrip. Emits one tool_use + tool_result row
            # per pair into trace.jsonl (matching a2-claude's shape for
            # dashboard TraceView #592 and OTel trace viewer #632) and
            # increments a2_sdk_tool_calls_total / observes
            # a2_sdk_tool_duration_seconds. No-op when AFC did not run.
            if _live:
                try:
                    await _emit_afc_history(
                        chat.history,
                        session_id=session_id,
                        model=resolved_model,
                    )
                except Exception as _afc_exc:
                    logger.debug("AFC history emit failed: %s", _afc_exc)

            # Persist updated history — log at ERROR on permanent failure so it is
            # visible in monitoring, but do not propagate so the completed response
            # is still returned to the caller.  Mark the session in history_save_failed
            # so the next request starts fresh rather than resuming inconsistent state (#409).
            try:
                await _save_history(session_id, chat.history)
                if history_save_failed is not None:
                    history_save_failed.discard(session_id)
            except Exception as _save_exc:
                logger.error(
                    "Permanently failed to save session history for %r: %s",
                    session_id, _save_exc, exc_info=True,
                )
                if a2_session_history_save_errors_total is not None:
                    a2_session_history_save_errors_total.labels(**_LABELS).inc()
                if history_save_failed is not None:
                    history_save_failed.add(session_id)

        if a2_sdk_query_duration_seconds is not None:
            a2_sdk_query_duration_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(time.monotonic() - _query_start)
        if a2_sdk_messages_per_query is not None:
            a2_sdk_messages_per_query.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(_message_count)
        if a2_sdk_turns_per_query is not None:
            a2_sdk_turns_per_query.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(_turn_count)
        if a2_text_blocks_per_query is not None:
            a2_text_blocks_per_query.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(len(collected))
        if _total_tokens is not None and max_tokens is not None:
            if a2_context_tokens is not None:
                a2_context_tokens.labels(**_LABELS).observe(_total_tokens)
            if a2_context_tokens_remaining is not None:
                a2_context_tokens_remaining.labels(**_LABELS).observe(max(0, max_tokens - _total_tokens))
            _pct = _total_tokens / max_tokens * 100
            if a2_context_usage_percent is not None:
                a2_context_usage_percent.labels(**_LABELS).observe(_pct)
            if _pct >= 100 and a2_context_exhaustion_total is not None:
                a2_context_exhaustion_total.labels(**_LABELS).inc()
            elif _pct >= 80 and a2_context_warnings_total is not None:
                a2_context_warnings_total.labels(**_LABELS).inc()

        try:
            ts = datetime.now(timezone.utc).isoformat()
            _trace_entry = {
                "ts": ts,
                "agent": AGENT_NAME, "agent_id": AGENT_ID,
                "session_id": session_id,
                "event_type": "response",
                "model": resolved_model,
                "chunks": len(collected),
            }
            await log_trace(json.dumps(_trace_entry))
        except Exception as e:
            logger.error(f"log_trace error: {e}")

        return collected
    finally:
        # Drop our refcount; the lock entry is evicted from session_locks only
        # when no other task holds or is waiting on it (#483).
        _release_session_lock(session_id, session_locks)


async def run(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    agent_md_content: str,
    session_locks: dict[str, "_RefCountedLock"],
    history_save_failed: set[str] | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
    on_chunk: Callable[[str], Awaitable[None]] | None = None,
    live_mcp_servers: list | None = None,
) -> str:
    if a2_concurrent_queries is not None:
        a2_concurrent_queries.labels(**_LABELS).inc()
    try:
        return await _run_inner(
            prompt, session_id, sessions, agent_md_content, session_locks,
            history_save_failed, model, max_tokens,
            on_chunk=on_chunk, live_mcp_servers=live_mcp_servers,
        )
    finally:
        if a2_concurrent_queries is not None:
            a2_concurrent_queries.labels(**_LABELS).dec()


async def _run_inner(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    agent_md_content: str,
    session_locks: dict[str, "_RefCountedLock"],
    history_save_failed: set[str] | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
    on_chunk: Callable[[str], Awaitable[None]] | None = None,
    live_mcp_servers: list | None = None,
) -> str:
    resolved_model = model or GEMINI_MODEL
    if a2_model_requests_total is not None:
        a2_model_requests_total.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).inc()

    # Treat sessions whose history failed to persist as new — resuming from a
    # partially-written or missing history file could produce incorrect context (#409).
    _save_failed = history_save_failed is not None and session_id in history_save_failed
    is_new = _save_failed or (session_id not in sessions and not await asyncio.to_thread(_session_file_exists, session_id))
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
            run_query(
                prompt, session_id, agent_md_content, session_locks,
                history_save_failed, model=model, max_tokens=max_tokens,
                on_chunk=on_chunk, live_mcp_servers=live_mcp_servers,
            ),
            timeout=TASK_TIMEOUT_SECONDS,
        )
        _track_session(sessions, session_id, session_locks, history_save_failed)
        # Lock-entry hygiene (#401) is now handled by the refcount in
        # run_query's finally clause so the pop cannot race with another
        # waiter (#483).
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: timed out after {TASK_TIMEOUT_SECONDS}s.")
        # Evict the session from the LRU cache on timeout. The underlying
        # ChatSession may be in an inconsistent state after a mid-stream
        # cancellation; removing it ensures the next call for this session_id
        # starts fresh rather than attempting to resume a broken session.
        sessions.pop(session_id, None)
        # Prune the timed-out session from history_save_failed so the set
        # does not grow unbounded across cycling session IDs (#485).
        if history_save_failed is not None:
            history_save_failed.discard(session_id)
        # Lock-entry hygiene on timeout is handled by run_query's finally
        # (refcount release). Popping here while another waiter holds the
        # lock would reintroduce the #483 race.
        # Also remove the on-disk history file so the next request for this
        # session_id starts with empty history rather than reloading the
        # potentially stale or mid-stream snapshot written before the timeout.
        _timeout_path = _session_path(session_id)
        try:
            os.remove(_timeout_path)
            logger.info("Removed stale session file for timed-out session %r", session_id)
        except FileNotFoundError:
            pass
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
            model=resolved_model,
        )
        collected = _bexc.collected
        _track_session(sessions, session_id, session_locks, history_save_failed)
        # Lock-entry hygiene handled by run_query's finally (#483).
    except Exception:
        # Lock-entry hygiene handled by run_query's finally (#483); popping
        # here races with other waiters.
        # Do NOT discard session_id from history_save_failed here (#520).
        # run_query's own except block intentionally ADDS session_id to
        # history_save_failed before re-raising (#493, #499), so the next
        # request for this session starts fresh rather than resuming a
        # partially-advanced chat.history. Discarding it here would silently
        # undo that protection. Unbounded-growth concerns (#485) are
        # addressed on the success and budget-exceeded paths via
        # _track_session's LRU-aligned pruning, and on the timeout path
        # above (which also removes the on-disk session file).
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

    response = "".join(collected) if collected else ""
    if not response:
        if a2_empty_responses_total is not None:
            a2_empty_responses_total.labels(**_LABELS).inc()
    elif a2_response_length_bytes is not None:
        a2_response_length_bytes.labels(**_LABELS).observe(len(response.encode()))
    return response


class AgentExecutor(A2AAgentExecutor):
    def __init__(self):
        # Validate API key at startup so missing credentials surface immediately
        # rather than on the first request (#417).
        _key = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY") or None
        if not _key:
            raise RuntimeError(
                "No Gemini API key configured. Set GEMINI_API_KEY or GOOGLE_API_KEY before starting."
            )
        self._sessions: OrderedDict[str, float] = OrderedDict()
        self._session_locks: dict[str, _RefCountedLock] = {}
        self._running_tasks: dict[str, asyncio.Task] = {}
        self._agent_md_content: str = _load_agent_md()
        self._mcp_watcher_tasks: list[asyncio.Task] = []
        # Session IDs whose history could not be persisted. On next request,
        # these sessions are treated as new rather than resuming potentially
        # inconsistent state (#409).
        self._history_save_failed: set[str] = set()
        # Hooks policy state (#631). Baseline rules ship with the image; the
        # extensions list starts empty and is populated by
        # ``hooks_config_watcher`` on startup and on every subsequent
        # hooks.yaml change. Held by reference so any future tool-call path
        # (#640) sees the latest rule set without re-reading the file.
        self._hook_state: HookState = HookState(
            baseline_enabled=HOOKS_BASELINE_ENABLED,
            baseline=list(BASELINE_RULES) if HOOKS_BASELINE_ENABLED else [],
            extensions=[],
        )
        # Lifespan-scoped MCP session stack (#640 — mirrors a2-codex #526).
        # MCP stdio subprocesses are entered once at startup (or on
        # hot-reload) and reused across requests. The lock serialises
        # reload-vs-request access to ``_live_mcp_servers`` so an in-flight
        # ``generate_content`` call with AFC cannot see a half-torn-down
        # ClientSession mid-stream.
        self._mcp_config: dict = {}
        self._mcp_stack: AsyncExitStack | None = None
        self._live_mcp_servers: list = []
        self._mcp_servers_lock: asyncio.Lock | None = None

    def _mcp_watchers(self):
        """Return callables for GEMINI.md, hooks.yaml, and mcp.json watching (#371, #631, #640)."""
        return [self.agent_md_watcher, self.hooks_config_watcher, self.mcp_config_watcher]

    async def _apply_mcp_config(self, mcp_config: dict) -> None:
        """Enter the given MCP config into a fresh lifespan-scoped stack (#640).

        Mirrors a2-codex.AgentExecutor._apply_mcp_config (#526). Tears down
        any previously-entered stack first, then opens a fresh
        ``stdio_client`` + ``ClientSession`` per server. Failures on
        individual servers are logged and skipped so one broken entry does
        not prevent others from starting. The ``a2_mcp_servers_active`` gauge
        reflects the actually-running count, not the config-loaded count.

        The ``ClientSession`` objects are what google-genai's AFC accepts in
        ``GenerateContentConfig(tools=[...])`` — they are the authoritative
        handle used everywhere downstream.
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

            if not mcp_config:
                if a2_mcp_servers_active is not None:
                    a2_mcp_servers_active.labels(**_LABELS).set(0)
                return

            try:
                from mcp import ClientSession  # type: ignore
                from mcp.client.stdio import stdio_client  # type: ignore
            except Exception as _imp_exc:
                logger.warning(
                    "mcp package not available (%s); MCP support disabled.",
                    _imp_exc,
                )
                if a2_mcp_servers_active is not None:
                    a2_mcp_servers_active.labels(**_LABELS).set(0)
                return

            new_stack = AsyncExitStack()
            await new_stack.__aenter__()
            new_live: list = []
            try:
                for name, cfg in mcp_config.items():
                    if not isinstance(cfg, dict):
                        logger.warning(
                            "MCP server %r: config must be a dict; got %r — skipping.",
                            name, type(cfg).__name__,
                        )
                        continue
                    params = _build_mcp_stdio_params(name, cfg)
                    if params is None:
                        continue
                    # mcp.call child span (#630) — wraps the stdio transport
                    # bring-up so the trace shows which MCP server the stack
                    # is spinning up (or failing to). kind=client reflects
                    # that the backend is dialling an external server.
                    with start_span(
                        "mcp.call",
                        kind="client",
                        attributes={"mcp.server": name, "mcp.tool": "__start__"},
                    ) as _mcp_span:
                        try:
                            read, write = await new_stack.enter_async_context(stdio_client(params))
                            session = await new_stack.enter_async_context(ClientSession(read, write))
                            await session.initialize()
                            new_live.append(session)
                        except Exception as _mcp_exc:
                            set_span_error(_mcp_span, _mcp_exc)
                            logger.warning(
                                "MCP server %r failed to start (%s); proceeding without it.",
                                name, _mcp_exc,
                            )
            except Exception:
                try:
                    await new_stack.aclose()
                except Exception:
                    pass
                raise

            self._mcp_stack = new_stack
            self._live_mcp_servers = new_live
            if a2_mcp_servers_active is not None:
                a2_mcp_servers_active.labels(**_LABELS).set(len(new_live))

            # Startup warning re: AFC vs hooks asymmetry (#640). Logged once
            # per stack bring-up so operators see it on every reload when
            # both sides are active. hooks skeleton in #631 cannot intercept
            # tool calls that AFC runs inside the SDK; the hand-rolled-loop
            # option lives in the #640 issue body.
            if new_live and os.environ.get("HOOKS_CONFIG_PATH") and (
                self._hook_state.extensions or self._hook_state.baseline
            ):
                logger.warning(
                    "a2-gemini hooks skeleton (#631) cannot intercept MCP tool calls "
                    "because google-genai's AFC runs the loop internally. "
                    "See #640 issue body option 2 to disable AFC and hand-roll the "
                    "loop if policy enforcement is required."
                )

    async def _snapshot_live_mcp_servers(self) -> list:
        """Return a defensive copy of the currently-live MCP server list (#640).

        Taken under the lock so a concurrent hot-reload cannot swap the list
        out from under the caller mid-read.
        """
        if self._mcp_servers_lock is None:
            self._mcp_servers_lock = asyncio.Lock()
        async with self._mcp_servers_lock:
            return list(self._live_mcp_servers)

    async def mcp_config_watcher(self) -> None:
        """Watch MCP_CONFIG_PATH and hot-reload the MCP server stack (#640).

        Mirrors a2-codex.AgentExecutor.mcp_config_watcher (#432, #526): load
        on startup, then watch the parent directory for any changes to the
        config file. Each reload restarts the lifespan-scoped MCP server
        stack so stdio subprocesses are respawned cleanly under the new
        config and existing request traffic sees a consistent snapshot.
        """
        from watchfiles import awatch as _awatch

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

        This ensures that updating GEMINI.md does not require a container restart,
        consistent with all other file-based configuration in the platform.
        """
        from watchfiles import awatch as _awatch

        # Perform an initial load so the watcher starts with current content.
        self._agent_md_content = _load_agent_md()
        logger.info("GEMINI.md loaded from %s", AGENT_MD)

        watch_dir = os.path.dirname(os.path.abspath(AGENT_MD))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("GEMINI.md directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in _awatch(watch_dir):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="agent_md").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(AGENT_MD):
                        self._agent_md_content = _load_agent_md()
                        logger.info("GEMINI.md reloaded from %s", AGENT_MD)
                        break
            logger.warning("GEMINI.md directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="agent_md").inc()
            await asyncio.sleep(10)

    async def hooks_config_watcher(self) -> None:
        """Watch hooks.yaml and hot-reload extension rules (#631).

        Mirrors ``a2-claude/executor.AgentExecutor.hooks_config_watcher`` so
        operators see identical semantics across backends: an initial load,
        then an ``awatch`` over the containing directory, re-parsing on every
        change to the target file. Failures during reload keep the previous
        rule set in place so a malformed edit cannot accidentally disable the
        policy.

        Note: this runs even though the gemini tool-call path is not yet
        exercising the engine (blocked on #640). Keeping the watcher active
        means rules are ready the moment #640 plumbs ``evaluate_pre_tool_use``
        into the dispatch path.
        """
        from watchfiles import awatch as _awatch

        self._hook_state.extensions = await asyncio.to_thread(load_hooks_config_sync)
        logger.info(
            "Hooks config loaded: baseline=%s (rules=%d) extensions=%d",
            self._hook_state.baseline_enabled,
            len(self._hook_state.baseline),
            len(self._hook_state.extensions),
        )

        watch_dir = os.path.dirname(os.path.abspath(HOOKS_CONFIG_PATH))
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("hooks.yaml directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in _awatch(watch_dir):
                if a2_watcher_events_total is not None:
                    a2_watcher_events_total.labels(**_LABELS, watcher="hooks").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(HOOKS_CONFIG_PATH):
                        try:
                            new_rules = await asyncio.to_thread(load_hooks_config_sync)
                            self._hook_state.extensions = new_rules
                            if a2_hooks_config_reloads_total is not None:
                                a2_hooks_config_reloads_total.labels(**_LABELS).inc()
                            logger.info("hooks.yaml reloaded: extensions=%d", len(new_rules))
                        except Exception as exc:
                            logger.warning("hooks.yaml reload failed — keeping previous rules: %s", exc)
                            if a2_hooks_config_errors_total is not None:
                                a2_hooks_config_errors_total.labels(
                                    **_LABELS, reason="yaml_reload_failed",
                                ).inc()
                        break
            logger.warning("hooks.yaml directory watcher exited — retrying in 10s.")
            if a2_file_watcher_restarts_total is not None:
                a2_file_watcher_restarts_total.labels(**_LABELS, watcher="hooks").inc()
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
        # Shared parser lives in shared/validation.py (#537, #428).
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
        # Streaming bridge (#430): forward each chunk text to the A2A
        # event_queue as it arrives. Tracks emission count so the
        # post-completion aggregated enqueue can be skipped when chunks were
        # already delivered.
        _chunks_emitted = 0
        # Pre-sanitize once for the streaming counter (#487): the inner closure
        # runs per chunk so resolving the bounded label here keeps it O(1) per
        # emit and guarantees a single canonical value for the whole request.
        _streaming_label_model = _sanitize_model_label(model or GEMINI_MODEL or "")

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
                "a2-gemini.execute",
                kind="server",
                parent_context=_otel_parent,
                attributes={
                    "a2.session_id": session_id,
                    "a2.model": model or GEMINI_MODEL or "",
                    "a2.agent": AGENT_NAME,
                    "a2.agent_id": AGENT_ID,
                },
            ) as _otel_span:
                _response = await run(
                    prompt,
                    session_id,
                    self._sessions,
                    self._agent_md_content,
                    self._session_locks,
                    history_save_failed=self._history_save_failed,
                    model=model,
                    max_tokens=max_tokens,
                    on_chunk=_emit_chunk,
                    live_mcp_servers=await self._snapshot_live_mcp_servers(),
                )
                _success = True
                # Skip the final aggregated event when chunks were streamed —
                # they already delivered the content.
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

    async def close(self) -> None:
        """Cancel and drain all watcher tasks, tear down the MCP stack (#640),
        then dispose the genai client.

        The genai client close runs *after* watchers are drained so in-flight
        A2A requests are not orphaned mid-call (#545). The MCP stack is
        torn down between the watchers and the genai client so stdio
        subprocesses and ClientSession pipes are released cleanly before
        the HTTP client pools go away.
        """
        for task in self._mcp_watcher_tasks:
            task.cancel()
        if self._mcp_watcher_tasks:
            await asyncio.gather(*self._mcp_watcher_tasks, return_exceptions=True)
        self._mcp_watcher_tasks.clear()
        if self._mcp_stack is not None:
            try:
                await self._mcp_stack.aclose()
            except Exception as _close_exc:
                logger.warning("MCP stack aclose on shutdown: %s", _close_exc)
            self._mcp_stack = None
            self._live_mcp_servers = []
            if a2_mcp_servers_active is not None:
                a2_mcp_servers_active.labels(**_LABELS).set(0)
        await _close_client()

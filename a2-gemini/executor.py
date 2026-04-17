import asyncio
import json
import logging
import os
import time
import uuid
from collections import OrderedDict
from datetime import datetime, timezone
from typing import Awaitable, Callable

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
    a2_sdk_client_errors_total,
    a2_sdk_errors_total,
    a2_sdk_query_error_duration_seconds,
    a2_sdk_result_errors_total,
    a2_sdk_session_duration_seconds,
    a2_sdk_time_to_first_message_seconds,
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

from log_utils import _append_log
from exceptions import BudgetExceededError
from validation import parse_max_tokens, sanitize_model_label

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
            _total_tokens = 0

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

            if a2_sdk_session_duration_seconds is not None:
                a2_sdk_session_duration_seconds.labels(**_LABELS, model=_sanitize_model_label(resolved_model)).observe(
                    time.monotonic() - _session_start
                )

            full_response = "".join(collected)
            if full_response:
                await log_entry("agent", full_response, session_id, model=resolved_model, tokens=_total_tokens or None)

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
) -> str:
    if a2_concurrent_queries is not None:
        a2_concurrent_queries.labels(**_LABELS).inc()
    try:
        return await _run_inner(prompt, session_id, sessions, agent_md_content, session_locks, history_save_failed, model, max_tokens, on_chunk=on_chunk)
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
            run_query(prompt, session_id, agent_md_content, session_locks, history_save_failed, model=model, max_tokens=max_tokens, on_chunk=on_chunk),
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

    def _mcp_watchers(self):
        """Return callables for GEMINI.md watching (#371)."""
        return [self.agent_md_watcher]

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
        """Cancel and drain all watcher tasks, then dispose the genai client.

        The genai client close runs *after* watchers are drained so in-flight
        A2A requests are not orphaned mid-call (#545).
        """
        for task in self._mcp_watcher_tasks:
            task.cancel()
        if self._mcp_watcher_tasks:
            await asyncio.gather(*self._mcp_watcher_tasks, return_exceptions=True)
        self._mcp_watcher_tasks.clear()
        await _close_client()

import asyncio
import json
import logging
import os
import time
import uuid

import yaml
from collections import OrderedDict
from datetime import datetime, timezone

from a2a.server.agent_execution import AgentExecutor as A2AAgentExecutor
from a2a.server.agent_execution import RequestContext
from a2a.server.events import EventQueue
from a2a.utils import new_agent_text_message
from backends.a2a import A2ABackend
from backends.config import BACKEND_CONFIG_PATH, BackendConfig, RoutingConfig, RoutingEntry, load_backends_config, load_routing_config
from bus import Message, MessageBus
from utils import ConsensusEntry
from log_utils import _append_log
from metrics import (
    agent_a2a_last_request_timestamp_seconds,
    agent_a2a_request_duration_seconds,
    agent_a2a_requests_total,
    agent_active_sessions,
    agent_concurrent_queries,
    agent_consensus_backend_errors_total,
    agent_consensus_runs_total,
    agent_empty_responses_total,
    agent_lru_cache_utilization_percent,
    agent_model_requests_total,
    agent_prompt_length_bytes,
    agent_response_length_bytes,
    agent_running_tasks,
    agent_session_age_seconds,
    agent_session_evictions_total,
    agent_session_idle_seconds,
    agent_session_starts_total,
    agent_task_cancellations_total,
    agent_task_duration_seconds,
    agent_task_error_duration_seconds,
    agent_task_last_error_timestamp_seconds,
    agent_task_last_success_timestamp_seconds,
    agent_task_restarts_total,
    agent_task_timeout_headroom_seconds,
    agent_tasks_total,
    agent_log_bytes_total,
    agent_log_entries_total,
    agent_log_write_errors_total,
)

logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "nyx")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")

MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Maximum number of bytes of prompt text included in INFO-level log messages.
# Set to 0 to suppress prompt text from logs entirely; set higher for more context.
LOG_PROMPT_MAX_BYTES = int(os.environ.get("LOG_PROMPT_MAX_BYTES", "200"))


async def log_entry(role: str, text: str, session_id: str, model: str | None = None, backend: str | None = None) -> None:
    try:
        entry = {
            "ts": datetime.now(timezone.utc).isoformat(),
            "agent": AGENT_NAME,
            "session_id": session_id,
            "role": role,
            "model": model,
            "backend": backend,
            "text": text,
        }
        _line = json.dumps(entry)
        await asyncio.to_thread(_append_log, CONVERSATION_LOG, _line)
        if agent_log_entries_total is not None:
            agent_log_entries_total.labels(logger="conversation").inc()
        if agent_log_bytes_total is not None:
            agent_log_bytes_total.labels(logger="conversation").inc(len(_line.encode()))
    except Exception as e:
        if agent_log_write_errors_total is not None:
            agent_log_write_errors_total.inc()
        logger.error(f"log_entry error: {e}")


def _build_backend(config: BackendConfig):
    return A2ABackend(config=config)


def load_backends():
    """Read backend.yaml once and return (backends dict, default_id, routing config)."""
    if not os.path.exists(BACKEND_CONFIG_PATH):
        raise FileNotFoundError(f"backend.yaml not found at {BACKEND_CONFIG_PATH}")
    with open(BACKEND_CONFIG_PATH) as f:
        raw = yaml.safe_load(f)
    configs = load_backends_config(raw)
    backends = {c.id: _build_backend(c) for c in configs}
    routing = load_routing_config(raw)
    if routing.default:
        if routing.default.agent not in backends:
            raise ValueError(f"routing.default agent '{routing.default.agent}' does not match any configured backend id.")
        default_id = routing.default.agent
    else:
        default_id = configs[0].id
        logger.info(f"No routing.default specified — using first backend: '{default_id}'")
    logger.info(f"Default backend: '{default_id}'")
    return backends, default_id, routing


def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    if session_id in sessions:
        sessions.move_to_end(session_id)
        sessions[session_id] = time.monotonic()
    else:
        if len(sessions) >= MAX_SESSIONS:
            _evicted_id, last_used_at = sessions.popitem(last=False)
            if agent_session_evictions_total is not None:
                agent_session_evictions_total.inc()
            if agent_session_age_seconds is not None:
                agent_session_age_seconds.observe(time.monotonic() - last_used_at)
        sessions[session_id] = time.monotonic()
    if agent_active_sessions is not None:
        agent_active_sessions.set(len(sessions))
    if agent_lru_cache_utilization_percent is not None:
        agent_lru_cache_utilization_percent.set(len(sessions) / MAX_SESSIONS * 100)


async def run(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    backends: dict,
    default_backend_id: str,
    backend_id: str | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
) -> str:
    if agent_concurrent_queries is not None:
        agent_concurrent_queries.inc()
    try:
        return await _run_inner(prompt, session_id, sessions, backends, default_backend_id, backend_id, model, max_tokens)
    finally:
        if agent_concurrent_queries is not None:
            agent_concurrent_queries.dec()


async def _run_inner(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    backends: dict,
    default_backend_id: str,
    backend_id: str | None = None,
    model: str | None = None,
    max_tokens: int | None = None,
) -> str:
    resolved_id = backend_id or default_backend_id
    backend = backends.get(resolved_id)
    if backend is None:
        raise ValueError(f"No backend configured with id '{resolved_id}'")

    if agent_model_requests_total is not None:
        agent_model_requests_total.labels(model=model or "default").inc()

    is_new = session_id not in sessions
    if not is_new and agent_session_idle_seconds is not None:
        agent_session_idle_seconds.observe(time.monotonic() - sessions[session_id])
    if agent_session_starts_total is not None:
        agent_session_starts_total.labels(type="new" if is_new else "resumed").inc()
    _track_session(sessions, session_id)

    _prompt_preview = prompt[:LOG_PROMPT_MAX_BYTES] + ("[truncated]" if len(prompt) > LOG_PROMPT_MAX_BYTES else "") if LOG_PROMPT_MAX_BYTES > 0 else "[redacted]"
    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) backend={resolved_id} — prompt: {_prompt_preview!r}")
    await log_entry("user", prompt, session_id, model=model, backend=resolved_id)

    if agent_prompt_length_bytes is not None:
        agent_prompt_length_bytes.observe(len(prompt.encode()))

    _start = time.monotonic()
    try:
        collected = await asyncio.wait_for(
            backend.run_query(prompt, session_id, is_new, model=model, max_tokens=max_tokens),
            timeout=TASK_TIMEOUT_SECONDS,
        )
    except asyncio.TimeoutError:
        logger.error(f"Session {session_id!r}: backend {resolved_id!r} timed out after {TASK_TIMEOUT_SECONDS}s.")
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

    if agent_tasks_total is not None:
        agent_tasks_total.labels(status="success").inc()
    if agent_task_last_success_timestamp_seconds is not None:
        agent_task_last_success_timestamp_seconds.set(time.time())
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


_BINARY_YES = frozenset({"yes", "true", "agree", "correct", "approved", "confirmed", "positive", "1"})
_BINARY_NO = frozenset({"no", "false", "disagree", "incorrect", "rejected", "denied", "negative", "0"})


def _classify_binary(text: str) -> str | None:
    """Return 'yes', 'no', or None if the response cannot be classified as binary."""
    normalised = text.strip().lower().rstrip(".")
    if normalised in _BINARY_YES:
        return "yes"
    if normalised in _BINARY_NO:
        return "no"
    return None


async def run_consensus(
    prompt: str,
    session_id: str,
    sessions: OrderedDict[str, float],
    backends: dict,
    default_backend_id: str,
    consensus_entries: list[ConsensusEntry],
    max_tokens: int | None = None,
) -> str:
    """Fan out *prompt* to matching backends concurrently and aggregate.

    *consensus_entries* is a list of ConsensusEntry (backend glob pattern + optional model).
    Each entry's backend pattern is matched against configured backend IDs via fnmatch.
    Model resolution per backend: entry model → BackendConfig.model → None.
    Binary responses (yes/no variants): majority vote; default backend wins ties.
    Freeform responses: a synthesis pass is sent to the default backend.
    """
    import fnmatch
    # Resolve entries to (call_key, backend_id, model) tuples, expanding glob patterns.
    # The same backend may appear multiple times with different models.
    # call_key = "bid:model" when model is set, else "bid" — used as the response label.
    resolved: list[tuple[str, str, str | None]] = []
    seen: set[tuple[str, str | None]] = set()
    for entry in consensus_entries:
        matched = [bid for bid in backends if fnmatch.fnmatch(bid, entry.backend)]
        for bid in matched:
            backend_model = entry.model or (backends[bid]._config.model if hasattr(backends[bid], "_config") else None)
            pair = (bid, backend_model)
            if pair not in seen:
                seen.add(pair)
                call_key = f"{bid}:{backend_model}" if backend_model else bid
                resolved.append((call_key, bid, backend_model))
    if not resolved:
        logger.warning("Consensus: no backends matched — falling back to default.")
        resolved = [(default_backend_id, default_backend_id, None)]

    async def _call(call_key: str, bid: str, model: str | None) -> tuple[str, str | Exception]:
        try:
            result = await _run_inner(prompt, session_id, sessions, backends, default_backend_id, backend_id=bid, model=model, max_tokens=max_tokens)
            return call_key, result
        except Exception as exc:
            return call_key, exc

    raw_results = await asyncio.gather(*[_call(k, bid, m) for k, bid, m in resolved])

    responses: dict[str, str] = {}
    for call_key, outcome in raw_results:
        if isinstance(outcome, Exception):
            logger.error(f"Consensus backend {call_key!r} failed: {outcome!r}")
            if agent_consensus_backend_errors_total is not None:
                agent_consensus_backend_errors_total.inc()
        else:
            responses[call_key] = outcome

    if not responses:
        raise RuntimeError("Consensus: all backends failed — no responses to aggregate.")

    # Attempt binary classification.
    classifications = {bid: _classify_binary(text) for bid, text in responses.items()}
    all_binary = all(v is not None for v in classifications.values())

    if all_binary:
        # Majority vote.
        yes_count = sum(1 for v in classifications.values() if v == "yes")
        no_count = sum(1 for v in classifications.values() if v == "no")
        if yes_count != no_count:
            winner = "yes" if yes_count > no_count else "no"
        else:
            # Tie — default backend wins (use first matching call_key).
            default_key = next((k for k, bid, _ in resolved if bid == default_backend_id), default_backend_id)
            winner = classifications.get(default_key, "yes")
        logger.info(f"Consensus (binary): yes={yes_count} no={no_count} → {winner}")
        if agent_consensus_runs_total is not None:
            agent_consensus_runs_total.labels(mode="binary", status="success").inc()
        return winner

    # Freeform: synthesis pass.
    parts = "\n\n".join(f"[{bid}]: {text}" for bid, text in responses.items())
    synthesis_prompt = (
        "The following responses were collected from multiple AI agents for the same prompt. "
        "Synthesise them into a single coherent, balanced answer, preserving the most important "
        "insights and noting any significant disagreements.\n\n"
        f"Original prompt: {prompt}\n\n"
        f"Agent responses:\n{parts}"
    )
    try:
        synthesised = await _run_inner(synthesis_prompt, session_id, sessions, backends, default_backend_id, backend_id=default_backend_id, model=model, max_tokens=max_tokens)
    except Exception as exc:
        logger.error(f"Consensus synthesis pass failed: {exc!r} — returning concatenated responses.")
        if agent_consensus_runs_total is not None:
            agent_consensus_runs_total.labels(mode="freeform", status="error").inc()
        return parts
    logger.info(f"Consensus (freeform): synthesised from {len(responses)} backend(s).")
    if agent_consensus_runs_total is not None:
        agent_consensus_runs_total.labels(mode="freeform", status="success").inc()
    return synthesised


async def _guarded(
    coro_fn,
    *args,
    restart_delay: float = 5.0,
    max_restarts: int = 5,
    on_not_ready=None,
    on_recovered=None,
) -> None:
    """Restart a coroutine function in a restart loop, catching unexpected exceptions.

    Replaces the former _guarded_watcher (which was a diverged subset of this
    implementation).  When on_not_ready / on_recovered callbacks are provided the
    caller can react to consecutive-crash and recovery events — e.g. to update a
    readiness flag (#363).

    The consecutive restart counter resets whenever a run lasts at least
    restart_delay seconds, so transient failures spread over time do not
    accumulate toward the threshold.
    """
    consecutive_restarts = 0
    while True:
        _attempt_start = time.monotonic()
        try:
            await coro_fn(*args)
            return  # clean exit — do not restart
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            if time.monotonic() - _attempt_start >= restart_delay:
                consecutive_restarts = 0
                if on_recovered is not None:
                    on_recovered()
            consecutive_restarts += 1
            logger.error(
                f"Task {coro_fn.__name__!r} crashed: {exc!r} — "
                f"restarting in {restart_delay}s (consecutive restart #{consecutive_restarts})"
            )
            if agent_task_restarts_total is not None:
                agent_task_restarts_total.labels(task=coro_fn.__name__).inc()
            if on_not_ready is not None and consecutive_restarts >= max_restarts:
                on_not_ready()
            await asyncio.sleep(restart_delay)


class AgentExecutor(A2AAgentExecutor):
    def __init__(self):
        self._sessions: OrderedDict[str, float] = OrderedDict()
        self._running_tasks: dict[str, asyncio.Task] = {}
        self._backends, self._default_backend_id, self._routing = load_backends()
        self._mcp_watcher_tasks: list[asyncio.Task] = []
        self._background_tasks: set[asyncio.Task] = set()
        self._continuation_runner = None
        self._webhook_runner = None
        self._bus = None

    def set_continuation_runner(self, runner: "ContinuationRunner", bus: MessageBus) -> None:
        self._continuation_runner = runner
        self._bus = bus

    def set_webhook_runner(self, runner: "WebhookRunner") -> None:
        self._webhook_runner = runner
        runner.set_backends(self._backends, self._default_backend_id)

    def _routing_entry_for_kind(self, kind: str) -> RoutingEntry | None:
        """Return the RoutingEntry for the given message kind, or None to use the default."""
        if kind == "a2a":
            return self._routing.a2a
        if kind == "heartbeat":
            return self._routing.heartbeat
        if kind.startswith("job"):
            return self._routing.job
        if kind.startswith("task"):
            return self._routing.task
        if kind.startswith("trigger"):
            return self._routing.trigger
        if kind.startswith("continuation"):
            return self._routing.continuation
        return None

    def _resolve_model(self, message_model: str | None, routing_entry: RoutingEntry | None, backend_id: str) -> str | None:
        """Resolve the model to use: per-message → routing entry → per-backend config.

        The routing entry model is only applied when the routing entry's agent matches
        the resolved backend. If a per-item agent override redirects to a different
        backend, the routing entry model is irrelevant and we fall through to the
        per-backend config model instead.
        """
        if message_model:
            return message_model
        if routing_entry and routing_entry.model and (routing_entry.agent is None or routing_entry.agent == backend_id):
            return routing_entry.model
        backend = self._backends.get(backend_id)
        if backend is not None and backend._config.model:
            return backend._config.model
        return None

    def _mcp_watchers(self):
        """Return callables for any backends that have an MCP config watcher."""
        watchers = []
        for backend in self._backends.values():
            watcher = getattr(backend, "mcp_config_watcher", None)
            if callable(watcher):
                watchers.append(watcher)
        return watchers

    async def on_prompt_completed(
        self,
        source: str,
        kind: str,
        session_id: str,
        success: bool,
        response: str,
        duration_seconds: float,
        error: str | None = None,
        model: str | None = None,
    ) -> None:
        if self._continuation_runner is not None:
            self._continuation_runner.notify(kind, session_id, success, response or "", self._bus)
        if self._webhook_runner is not None:
            self._webhook_runner.fire(
                source=source,
                kind=kind,
                session_id=session_id,
                success=success,
                response=response or "",
                duration_seconds=duration_seconds,
                error=error,
                model=model,
            )

    async def backends_watcher(self) -> None:
        """Watch BACKEND_CONFIG_PATH and reload backends on file change."""
        from backends.config import BACKEND_CONFIG_PATH
        from watchfiles import awatch

        watch_dir = os.path.dirname(os.path.abspath(BACKEND_CONFIG_PATH))
        logger.info(f"Backends watcher watching {BACKEND_CONFIG_PATH}")
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("Backends config directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in awatch(watch_dir):
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(BACKEND_CONFIG_PATH):
                        logger.info("backend.yaml changed — reloading.")
                        try:
                            new_backends, new_default_id, new_routing = load_backends()
                            old_backends = list(self._backends.values())
                            self._backends = new_backends
                            self._default_backend_id = new_default_id
                            self._routing = new_routing
                            if self._webhook_runner is not None:
                                self._webhook_runner.set_backends(new_backends, new_default_id)
                            logger.info(f"Backends reloaded: {list(new_backends.keys())} (default: {new_default_id})")
                            # Close old backend clients to release connection pool resources.
                            await asyncio.gather(
                                *[b.close() for b in old_backends if hasattr(b, "close")],
                                return_exceptions=True,
                            )
                            # Cancel old MCP watcher tasks and await their completion
                            # before starting new ones, so old and new watchers do not
                            # briefly overlap and cause duplicate reloads (#369).
                            _old_watcher_tasks = list(self._mcp_watcher_tasks)
                            for t in _old_watcher_tasks:
                                t.cancel()
                            if _old_watcher_tasks:
                                await asyncio.gather(*_old_watcher_tasks, return_exceptions=True)
                            self._mcp_watcher_tasks = []
                            for watcher in self._mcp_watchers():
                                task = asyncio.create_task(_guarded(watcher))
                                task.add_done_callback(
                                    lambda t, _w=watcher: logger.error(f"MCP watcher {_w.__name__!r} exited unexpectedly: {t.exception()!r}")
                                    if not t.cancelled() and t.exception() is not None
                                    else None
                                )
                                self._mcp_watcher_tasks.append(task)
                        except Exception as e:
                            logger.error("Failed to reload backends — keeping previous config: %s", e, exc_info=True)
                        break
            logger.warning("Backends watcher exited — retrying in 10s.")
            await asyncio.sleep(10)

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        _exec_start = time.monotonic()
        prompt = context.get_user_input()
        metadata = context.message.metadata or {}
        _raw_sid = "".join(c for c in str(context.context_id or metadata.get("session_id") or "").strip()[:256] if c >= " ")
        session_id = _raw_sid or str(uuid.uuid4())
        # Explicit backend_id in metadata takes priority; otherwise use routing config.
        _a2a_entry = self._routing_entry_for_kind("a2a")
        backend_id = metadata.get("backend_id") or (_a2a_entry.agent if _a2a_entry else None)
        model = metadata.get("model") or None
        if not model:
            _resolved_backend_id = backend_id or self._default_backend_id
            model = self._resolve_model(None, _a2a_entry, _resolved_backend_id)
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
                if agent_running_tasks is not None:
                    agent_running_tasks.inc()
        _response = ""
        _success = False
        _error: str | None = None
        try:
            _response = await run(
                prompt, session_id, self._sessions,
                self._backends, self._default_backend_id,
                backend_id=backend_id,
                model=model,
                max_tokens=max_tokens,
            )
            _success = True
            if _response:
                await event_queue.enqueue_event(new_agent_text_message(_response))
            if agent_a2a_requests_total is not None:
                agent_a2a_requests_total.labels(status="success").inc()
        except Exception as _exc:
            _error = repr(_exc)
            if agent_a2a_requests_total is not None:
                agent_a2a_requests_total.labels(status="error").inc()
            raise
        finally:
            _opc_task = asyncio.create_task(self.on_prompt_completed(
                source="a2a",
                kind="a2a",
                session_id=session_id,
                success=_success,
                response=_response,
                duration_seconds=time.monotonic() - _exec_start,
                error=_error,
                model=model,
            ))
            self._background_tasks.add(_opc_task)
            _opc_task.add_done_callback(self._background_tasks.discard)
            _opc_task.add_done_callback(
                lambda t: logger.error(f"on_prompt_completed error: {t.exception()}")
                if not t.cancelled() and t.exception() is not None
                else None
            )
            if agent_a2a_request_duration_seconds is not None:
                agent_a2a_request_duration_seconds.observe(time.monotonic() - _exec_start)
            if agent_a2a_last_request_timestamp_seconds is not None:
                agent_a2a_last_request_timestamp_seconds.set(time.time())
            if task_id and task_id in self._running_tasks:
                self._running_tasks.pop(task_id)
                if agent_running_tasks is not None:
                    agent_running_tasks.dec()

    async def close(self) -> None:
        """Cancel and drain all MCP watcher tasks."""
        for task in self._mcp_watcher_tasks:
            task.cancel()
        if self._mcp_watcher_tasks:
            await asyncio.gather(*self._mcp_watcher_tasks, return_exceptions=True)
        self._mcp_watcher_tasks.clear()

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        if agent_task_cancellations_total is not None:
            agent_task_cancellations_total.inc()
        task_id = context.task_id
        task = self._running_tasks.get(task_id) if task_id else None
        if task:
            task.cancel()
            logger.info(f"Task {task_id!r} cancellation requested.")
        else:
            logger.info(f"Task {task_id!r} cancellation requested but no running task found.")

    async def process_bus(self, message: Message) -> None:
        _bus_start = time.monotonic()
        _session_id = message.session_id or str(uuid.uuid4())
        _response = ""
        _success = False
        _error: str | None = None
        # Use per-item backend_id override, then routing config, then default.
        _entry = self._routing_entry_for_kind(message.kind)
        _routed_backend_id = message.backend_id or (_entry.agent if _entry else None)
        _resolved_id = _routed_backend_id or self._default_backend_id
        _model = self._resolve_model(message.model, _entry, _resolved_id)
        try:
            if message.consensus:
                _response = await run_consensus(
                    message.prompt,
                    _session_id,
                    self._sessions,
                    self._backends,
                    self._default_backend_id,
                    consensus_entries=message.consensus,
                    max_tokens=message.max_tokens,
                )
            else:
                _response = await run(
                    message.prompt,
                    _session_id,
                    self._sessions,
                    self._backends,
                    self._default_backend_id,
                    backend_id=_routed_backend_id,
                    model=_model,
                    max_tokens=message.max_tokens,
                )
            _success = True
            if message.result is not None and not message.result.done():
                message.result.set_result(_response)
        except Exception as e:
            _error = repr(e)
            logger.exception(f"process_bus error for session {_session_id!r}: {e}")
            if message.result is not None and not message.result.done():
                message.result.set_exception(e)
        finally:
            _opc_task = asyncio.create_task(self.on_prompt_completed(
                source="bus",
                kind=message.kind,
                session_id=_session_id,
                success=_success,
                response=_response,
                duration_seconds=time.monotonic() - _bus_start,
                error=_error,
                model=_model,
            ))
            self._background_tasks.add(_opc_task)
            _opc_task.add_done_callback(self._background_tasks.discard)
            _opc_task.add_done_callback(
                lambda t: logger.error(f"on_prompt_completed error: {t.exception()}")
                if not t.cancelled() and t.exception() is not None
                else None
            )

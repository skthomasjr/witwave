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
from tracing import (
    TraceContext,
    context_from_inbound,
    extract_otel_context,
    new_context,
    set_span_error,
    start_span,
)
from utils import ConsensusEntry
from log_utils import _append_log
from metrics import (
    agent_a2a_last_request_timestamp_seconds,
    agent_a2a_request_duration_seconds,
    agent_a2a_requests_total,
    agent_a2a_traces_received_total,
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
    agent_background_tasks,
    agent_background_tasks_shed_total,
    agent_background_tasks_timeout_total,
)

logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "nyx")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")

MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Maximum number of bytes of prompt text included in INFO-level log messages.
# Set to 0 to suppress prompt text from logs entirely; set higher for more context.
LOG_PROMPT_MAX_BYTES = int(os.environ.get("LOG_PROMPT_MAX_BYTES", "200"))
# Hard ceiling on how long an on_prompt_completed fan-out (continuation + webhook
# extraction) is allowed to run before the tracking task is cancelled. Chosen to
# be generous by default — 2× TASK_TIMEOUT_SECONDS + a 60s margin — so legitimate
# LLM extraction completes while still bounding stuck downstreams. Override via
# ON_PROMPT_COMPLETED_TIMEOUT (#549).
ON_PROMPT_COMPLETED_TIMEOUT = float(
    os.environ.get("ON_PROMPT_COMPLETED_TIMEOUT", str(TASK_TIMEOUT_SECONDS * 2 + 60))
)
# Maximum number of background tasks tracked by AgentExecutor at any one time.
# When the cap is hit new tasks are shed and counted in
# agent_background_tasks_shed_total rather than growing the set without bound (#549).
BACKGROUND_TASKS_MAX = int(os.environ.get("BACKGROUND_TASKS_MAX", "1000"))


async def log_entry(
    role: str,
    text: str,
    session_id: str,
    model: str | None = None,
    backend: str | None = None,
    trace_context: TraceContext | None = None,
) -> None:
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
        # Attach trace context to every conversation log line when present so
        # external log-correlation tools can join the JSONL with downstream
        # backend traces and webhooks (#468). Absent by default to keep old
        # logs backward-compatible.
        if trace_context is not None:
            entry["trace_id"] = trace_context.trace_id
            entry["span_id"] = trace_context.parent_id
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
    # Validate all routing entries reference known backend IDs.
    _routing_fields = {
        "default": routing.default,
        "a2a": routing.a2a,
        "heartbeat": routing.heartbeat,
        "job": routing.job,
        "task": routing.task,
        "trigger": routing.trigger,
        "continuation": routing.continuation,
    }
    for _field, _entry in _routing_fields.items():
        if _entry is not None and _entry.agent not in backends:
            raise ValueError(
                f"routing.{_field} agent '{_entry.agent}' does not match any configured backend id. "
                f"Known ids: {list(backends)}"
            )
    if routing.default:
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
    trace_context: TraceContext | None = None,
) -> str:
    if agent_concurrent_queries is not None:
        agent_concurrent_queries.inc()
    try:
        return await _run_inner(
            prompt, session_id, sessions, backends, default_backend_id,
            backend_id, model, max_tokens, trace_context=trace_context,
        )
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
    trace_context: TraceContext | None = None,
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
    _trace_tag = f" trace_id={trace_context.trace_id}" if trace_context is not None else ""
    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) backend={resolved_id}{_trace_tag} — prompt: {_prompt_preview!r}")
    await log_entry("user", prompt, session_id, model=model, backend=resolved_id, trace_context=trace_context)

    if agent_prompt_length_bytes is not None:
        agent_prompt_length_bytes.observe(len(prompt.encode()))

    _start = time.monotonic()
    try:
        collected = await asyncio.wait_for(
            backend.run_query(
                prompt, session_id, is_new,
                model=model, max_tokens=max_tokens,
                trace_context=trace_context,
            ),
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
    synthesis_backend_id: str | None = None,
    synthesis_model: str | None = None,
    max_tokens: int | None = None,
    trace_context: TraceContext | None = None,
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
        if not matched:
            logger.warning("Consensus: pattern %r matched no backends (known: %s)", entry.backend, list(backends))
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
            result = await _run_inner(
                prompt, session_id, sessions, backends, default_backend_id,
                backend_id=bid, model=model, max_tokens=max_tokens,
                trace_context=trace_context,
            )
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
            # If the default backend isn't among the consensus participants (or its
            # call_key isn't present in classifications), fall back deterministically
            # to the lexicographically first call_key rather than silently biasing
            # toward "yes" (#496).
            default_key = next((k for k, bid, _ in resolved if bid == default_backend_id), None)
            if default_key is not None and default_key in classifications:
                winner = classifications[default_key]
            else:
                fallback_key = min(classifications.keys())
                logger.warning(
                    "Consensus tie-break: default backend %r not present in "
                    "classifications (keys=%s); falling back deterministically to %r.",
                    default_backend_id, sorted(classifications.keys()), fallback_key,
                )
                winner = classifications[fallback_key]
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
        synthesised = await _run_inner(
            synthesis_prompt, session_id, sessions, backends, default_backend_id,
            backend_id=synthesis_backend_id or default_backend_id,
            model=synthesis_model, max_tokens=max_tokens,
            trace_context=trace_context,
        )
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
                if consecutive_restarts > 0 and on_recovered is not None:
                    on_recovered()
                consecutive_restarts = 0
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

    # Public read-only accessors for the two most-accessed private attributes
    # across executor-boundary call sites (narrow slice of #572). These are
    # additive: call sites still read ``_backends`` / ``_default_backend_id``
    # directly today; new code should prefer the public names so the underlying
    # storage can evolve without a big-bang rename. The broader API-extraction
    # refactor (all accessors, call-site migration, TriggerRunner wrapping,
    # A2ABackend._config exposure) remains deferred.
    @property
    def backends(self) -> dict:
        """Mapping of backend_id → A2ABackend. Reflects live reloads."""
        return self._backends

    @property
    def default_backend_id(self) -> str:
        """Currently configured default backend id. Reflects live reloads."""
        return self._default_backend_id

    def track_background(
        self,
        coro,
        *,
        source: str = "unknown",
        timeout: float | None = None,
        name: str | None = None,
    ) -> asyncio.Task | None:
        """Schedule ``coro`` as a tracked background task with a bounded lifetime.

        The task is added to ``_background_tasks`` and discarded via a done-callback
        when it finishes, so the set stays bounded even if the coroutine raises.
        A hard ceiling on in-flight tasks (``BACKGROUND_TASKS_MAX``) prevents the
        set from growing without bound when downstreams hang — excess tasks are
        shed and the ``agent_background_tasks_shed_total`` counter is incremented.
        A per-task timeout (``ON_PROMPT_COMPLETED_TIMEOUT`` by default) prevents a
        single stuck coroutine from pinning memory forever (#549).

        Returns the scheduled task, or ``None`` if the task was shed.
        """
        if len(self._background_tasks) >= BACKGROUND_TASKS_MAX:
            logger.warning(
                "track_background: shedding %r task (in-flight=%d, cap=%d)",
                source, len(self._background_tasks), BACKGROUND_TASKS_MAX,
            )
            if agent_background_tasks_shed_total is not None:
                agent_background_tasks_shed_total.labels(source=source).inc()
            # Close the coroutine so we don't leak a 'coroutine was never awaited'
            # warning and any resources it holds are released immediately.
            try:
                coro.close()
            except Exception:
                pass
            return None

        _effective_timeout = timeout if timeout is not None else ON_PROMPT_COMPLETED_TIMEOUT

        async def _bounded() -> None:
            try:
                await asyncio.wait_for(coro, timeout=_effective_timeout)
            except asyncio.TimeoutError:
                logger.error(
                    "track_background: %r task exceeded timeout=%.1fs and was cancelled",
                    source, _effective_timeout,
                )
                if agent_background_tasks_timeout_total is not None:
                    agent_background_tasks_timeout_total.labels(source=source).inc()

        task = asyncio.create_task(_bounded(), name=name or f"bg-{source}")
        self._background_tasks.add(task)
        if agent_background_tasks is not None:
            agent_background_tasks.set(len(self._background_tasks))

        def _on_done(t: asyncio.Task, _tasks=self._background_tasks) -> None:
            _tasks.discard(t)
            if agent_background_tasks is not None:
                agent_background_tasks.set(len(_tasks))
            if not t.cancelled():
                exc = t.exception()
                if exc is not None:
                    logger.error(f"track_background({source!r}) error: {exc!r}")

        task.add_done_callback(_on_done)
        return task

    def set_continuation_runner(self, runner: "ContinuationRunner", bus: MessageBus) -> None:
        self._continuation_runner = runner
        self._bus = bus

    def set_webhook_runner(self, runner: "WebhookRunner") -> None:
        self._webhook_runner = runner
        runner.set_backends(self._backends, self._default_backend_id)

    def _routing_entry_for_kind(self, kind: str) -> RoutingEntry | None:
        """Return the RoutingEntry for the given message kind, or None to use the default."""
        if kind == "a2a":
            entry = self._routing.a2a
        elif kind == "heartbeat":
            entry = self._routing.heartbeat
        elif kind.startswith("job"):
            entry = self._routing.job
        elif kind.startswith("task"):
            entry = self._routing.task
        elif kind.startswith("trigger"):
            entry = self._routing.trigger
        elif kind.startswith("continuation"):
            entry = self._routing.continuation
        else:
            entry = None
        logger.debug("routing kind=%r → entry agent=%r model=%r", kind, entry.agent if entry else None, entry.model if entry else None)
        return entry

    def _resolve_model(self, message_model: str | None, routing_entry: RoutingEntry | None, backend_id: str) -> str | None:
        """Resolve the model to use: per-message → routing entry → per-backend config.

        The routing entry model is only applied when the routing entry's agent matches
        the resolved backend. If a per-item agent override redirects to a different
        backend, the routing entry model is irrelevant and we fall through to the
        per-backend config model instead.
        """
        if message_model:
            logger.debug("model resolution: using per-message override %r", message_model)
            return message_model
        if routing_entry and routing_entry.model and (routing_entry.agent is None or routing_entry.agent == backend_id):
            logger.debug("model resolution: using routing entry model %r", routing_entry.model)
            return routing_entry.model
        backend = self._backends.get(backend_id)
        if backend is not None and backend._config.model:
            logger.debug("model resolution: using backend config model %r for %r", backend._config.model, backend_id)
            return backend._config.model
        logger.debug("model resolution: no model configured for backend %r — sending without override", backend_id)
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
        trace_context: TraceContext | None = None,
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
                trace_context=trace_context,
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
        # Resolve W3C trace context from inbound metadata (#468). The A2A SDK
        # doesn't surface raw HTTP headers here, but upstream callers echo the
        # traceparent into message.metadata so we can still continue the trace.
        _tp_header = metadata.get("traceparent")
        trace_context, _had_inbound = context_from_inbound(
            _tp_header if isinstance(_tp_header, str) else None
        )
        if agent_a2a_traces_received_total is not None:
            agent_a2a_traces_received_total.labels(has_inbound=str(_had_inbound).lower()).inc()
        # Bridge to OTel (#469). When OTel is enabled the extracted context
        # becomes the parent of the server span below; when disabled this
        # returns None and start_span silently emits a no-op span.
        _otel_parent = extract_otel_context({"traceparent": _tp_header}) if _tp_header else None
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
        _span_attrs = {
            "nyx.session_id": session_id,
            "nyx.backend_id": backend_id or self._default_backend_id,
            "nyx.model": model or "",
            "nyx.trace_id": trace_context.trace_id,
            "nyx.has_inbound_trace": _had_inbound,
        }
        try:
            with start_span(
                "a2a.execute",
                kind="server",
                parent_context=_otel_parent,
                attributes=_span_attrs,
            ) as _span:
                try:
                    _response = await run(
                        prompt, session_id, self._sessions,
                        self._backends, self._default_backend_id,
                        backend_id=backend_id,
                        model=model,
                        max_tokens=max_tokens,
                        trace_context=trace_context,
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
                    set_span_error(_span, _exc)
                    raise
        finally:
            self.track_background(
                self.on_prompt_completed(
                    source="a2a",
                    kind="a2a",
                    session_id=session_id,
                    success=_success,
                    response=_response,
                    duration_seconds=time.monotonic() - _exec_start,
                    error=_error,
                    model=model,
                    trace_context=trace_context,
                ),
                source="a2a",
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
        # Bus-originated work (heartbeats, jobs, tasks, triggers, continuations)
        # mints a fresh context when the message has no inbound trace. This
        # ensures every backend call carries a trace_id even for internally
        # scheduled work (#468).
        _trace_context = message.trace_context or new_context()
        try:
            if message.consensus:
                _response = await run_consensus(
                    message.prompt,
                    _session_id,
                    self._sessions,
                    self._backends,
                    self._default_backend_id,
                    consensus_entries=message.consensus,
                    synthesis_backend_id=_resolved_id,
                    synthesis_model=_model,
                    max_tokens=message.max_tokens,
                    trace_context=_trace_context,
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
                    trace_context=_trace_context,
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
            self.track_background(
                self.on_prompt_completed(
                    source="bus",
                    kind=message.kind,
                    session_id=_session_id,
                    success=_success,
                    response=_response,
                    duration_seconds=time.monotonic() - _bus_start,
                    error=_error,
                    model=_model,
                    trace_context=_trace_context,
                ),
                source="bus",
            )

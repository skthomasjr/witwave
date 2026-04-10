import asyncio
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
from backends.a2a import A2ABackend
from backends.config import BackendConfig, RoutingConfig, RoutingEntry, load_backends_config, load_routing_config
from bus import Message, MessageBus
from metrics import (
    agent_a2a_last_request_timestamp_seconds,
    agent_a2a_request_duration_seconds,
    agent_a2a_requests_total,
    agent_active_sessions,
    agent_concurrent_queries,
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
    agent_task_timeout_headroom_seconds,
    agent_tasks_total,
    agent_log_bytes_total,
    agent_log_entries_total,
    agent_log_write_errors_total,
)

logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.log")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")

MAX_LOG_BYTES = int(os.environ.get("MAX_LOG_BYTES", str(10 * 1024 * 1024)))
MAX_LOG_BACKUP_COUNT = int(os.environ.get("MAX_LOG_BACKUP_COUNT", "1"))
MAX_SESSIONS = int(os.environ.get("MAX_SESSIONS", "10000"))
TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))


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
        if agent_log_entries_total is not None:
            agent_log_entries_total.labels(logger="conversation").inc()
        if agent_log_bytes_total is not None:
            agent_log_bytes_total.labels(logger="conversation").inc(len(_formatted.encode()))
    except Exception as e:
        if agent_log_write_errors_total is not None:
            agent_log_write_errors_total.inc()
        logger.error(f"log_entry error: {e}")


def log_trace(text: str) -> None:
    try:
        trace = get_trace_logger()
        trace.info(text)
        if agent_log_entries_total is not None:
            agent_log_entries_total.labels(logger="trace").inc()
        if agent_log_bytes_total is not None:
            agent_log_bytes_total.labels(logger="trace").inc(len(text.encode()))
    except Exception as e:
        if agent_log_write_errors_total is not None:
            agent_log_write_errors_total.inc()
        logger.error(f"log_trace error: {e}")


def _build_backend(config: BackendConfig):
    return A2ABackend(config=config)


def load_backends():
    configs = load_backends_config()
    backends = {c.id: _build_backend(c) for c in configs}
    routing = load_routing_config()
    if routing.default:
        if routing.default.agent not in backends:
            raise ValueError(f"routing.default agent '{routing.default.agent}' does not match any configured backend id.")
        default_id = routing.default.agent
    else:
        default_id = configs[0].id
        logger.info(f"No routing.default specified — using first backend: '{default_id}'")
    logger.info(f"Default backend: '{default_id}'")
    return backends, default_id


def load_routing() -> RoutingConfig:
    try:
        return load_routing_config()
    except Exception as e:
        logger.warning(f"Failed to load routing config — using default backend for all concerns: {e}")
        return RoutingConfig()


def _track_session(sessions: OrderedDict[str, float], session_id: str) -> None:
    if session_id in sessions:
        sessions.move_to_end(session_id)
        sessions[session_id] = time.monotonic()
        return
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
) -> str:
    if agent_concurrent_queries is not None:
        agent_concurrent_queries.inc()
    try:
        return await _run_inner(prompt, session_id, sessions, backends, default_backend_id, backend_id, model)
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

    logger.info(f"Session {session_id} ({'new' if is_new else 'existing'}) backend={resolved_id} — prompt: {prompt!r}")
    if not isinstance(backend, A2ABackend):
        log_entry("user", prompt, session_id, suffix=f" [backend: {resolved_id}]")

    if agent_prompt_length_bytes is not None:
        agent_prompt_length_bytes.observe(len(prompt.encode()))

    _start = time.monotonic()
    try:
        collected = await asyncio.wait_for(
            backend.run_query(prompt, session_id, is_new, model=model),
            timeout=TASK_TIMEOUT_SECONDS,
        )
        _track_session(sessions, session_id)
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


async def _guarded_watcher(coro_fn, restart_delay: float = 5.0) -> None:
    """Restart a watcher coroutine on unexpected exceptions (mirrors main._guarded)."""
    while True:
        try:
            await coro_fn()
            return
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            logger.error(f"MCP watcher {coro_fn.__name__!r} crashed: {exc!r} — restarting in {restart_delay}s")
            if agent_task_restarts_total is not None:
                agent_task_restarts_total.labels(task=coro_fn.__name__).inc()
            await asyncio.sleep(restart_delay)


class AgentExecutor(A2AAgentExecutor):
    def __init__(self):
        self._sessions: OrderedDict[str, float] = OrderedDict()
        self._running_tasks: dict[str, asyncio.Task] = {}
        self._backends, self._default_backend_id = load_backends()
        self._routing: RoutingConfig = load_routing()
        self._mcp_watcher_tasks: list[asyncio.Task] = []
        self._background_tasks: set[asyncio.Task] = set()
        self._continuation_runner = None
        self._bus = None

    def set_continuation_runner(self, runner: "ContinuationRunner", bus: MessageBus) -> None:
        self._continuation_runner = runner
        self._bus = bus

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
        """Resolve the model to use: per-message → routing entry → per-backend config."""
        if message_model:
            return message_model
        if routing_entry and routing_entry.model:
            return routing_entry.model
        backend = self._backends.get(backend_id)
        if backend is not None and backend.config.model:
            return backend.config.model
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
    ) -> None:
        if self._continuation_runner is not None:
            self._continuation_runner.notify(kind, session_id, success, response or "", self._bus)

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
                            new_backends, new_default_id = load_backends()
                            self._backends = new_backends
                            self._default_backend_id = new_default_id
                            self._routing = load_routing()
                            logger.info(f"Backends reloaded: {list(new_backends.keys())} (default: {new_default_id})")
                            # Cancel old MCP watcher tasks and start new ones for the reloaded backends.
                            for t in self._mcp_watcher_tasks:
                                t.cancel()
                            self._mcp_watcher_tasks = []
                            for watcher in self._mcp_watchers():
                                task = asyncio.create_task(_guarded_watcher(watcher))
                                task.add_done_callback(
                                    lambda t, _w=watcher: logger.error(f"MCP watcher {_w.__name__!r} exited unexpectedly: {t.exception()!r}")
                                    if not t.cancelled() and t.exception() is not None
                                    else None
                                )
                                self._mcp_watcher_tasks.append(task)
                        except Exception as e:
                            logger.error(f"Failed to reload backends — keeping previous config: {e}")
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
            _response = await run(
                message.prompt,
                _session_id,
                self._sessions,
                self._backends,
                self._default_backend_id,
                backend_id=_routed_backend_id,
                model=_model,
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
            ))
            self._background_tasks.add(_opc_task)
            _opc_task.add_done_callback(self._background_tasks.discard)
            _opc_task.add_done_callback(
                lambda t: logger.error(f"on_prompt_completed error: {t.exception()}")
                if not t.cancelled() and t.exception() is not None
                else None
            )

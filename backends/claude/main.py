import asyncio
import hashlib
import hmac as hmac_mod
import logging
import os
import time
import uuid
from contextlib import AsyncExitStack, asynccontextmanager
from datetime import datetime, timezone

import prometheus_client
import uvicorn
from a2a.server.apps import A2AStarletteApplication
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.tasks import InMemoryTaskStore
from sqlite_task_store import SqliteTaskStore
from a2a.types import (
    AgentCapabilities,
    AgentCard,
    AgentSkill,
)
from conversations import (
    auth_disabled_escape_hatch,
    make_conversations_handler,
    make_trace_handler,
)
from executor import AgentExecutor
from session_binding import derive_session_id
from metrics import (
    backend_event_loop_lag_seconds,
    backend_health_checks_total,
    backend_info,
    backend_mcp_request_duration_seconds,
    backend_mcp_requests_total,
    backend_startup_duration_seconds,
    backend_task_restarts_total,
    backend_up,
    backend_uptime_seconds,
)
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import JSONResponse, Response
from starlette.routing import Mount, Route

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "claude")
AGENT_HOST = os.environ.get("AGENT_HOST", "0.0.0.0")
BACKEND_PORT = int(os.environ.get("BACKEND_PORT", "8000"))
AGENT_URL = os.environ.get("AGENT_URL", f"http://localhost:{BACKEND_PORT}/")
AGENT_VERSION = os.environ.get("AGENT_VERSION", "0.1.0")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/tool-activity.jsonl")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "claude")
_BACKEND_ID = "claude"
metrics_enabled = bool(os.environ.get("METRICS_ENABLED"))
WORKER_MAX_RESTARTS = int(os.environ.get("WORKER_MAX_RESTARTS", "5"))
CONVERSATIONS_AUTH_TOKEN = os.environ.get("CONVERSATIONS_AUTH_TOKEN", "")

_ready: bool = False
_startup_mono: float = 0.0
start_time: datetime = datetime.now(timezone.utc)


def load_agent_description() -> str:
    try:
        with open("/home/agent/.claude/agent-card.md") as f:
            return f.read()
    except OSError:
        return os.environ.get("AGENT_DESCRIPTION", "A Claude backend agent.")


def build_agent_card() -> AgentCard:
    return AgentCard(
        name=AGENT_NAME,
        description=load_agent_description(),
        url=AGENT_URL,
        version=AGENT_VERSION,
        capabilities=AgentCapabilities(streaming=True),
        default_input_modes=["text/plain"],
        default_output_modes=["text/plain"],
        skills=[
            AgentSkill(
                id="general",
                name="General",
                description="General-purpose task execution via Claude.",
                tags=["general", "claude"],
            )
        ],
    )


async def health(request: Request) -> JSONResponse:
    if backend_health_checks_total is not None:
        backend_health_checks_total.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID, probe="health").inc()
    if _ready:
        elapsed = (datetime.now(timezone.utc) - start_time).total_seconds()
        return JSONResponse({"status": "ok", "agent": AGENT_NAME, "uptime_seconds": elapsed})
    return JSONResponse({"status": "starting"}, status_code=503)


@asynccontextmanager
async def _sub_app_lifespan(app):
    """Drive the ASGI lifespan protocol on a sub-app."""
    loop = asyncio.get_running_loop()
    startup: asyncio.Future[bool] = loop.create_future()
    do_shutdown: asyncio.Event = asyncio.Event()
    shutdown: asyncio.Future[None] = loop.create_future()

    async def receive() -> dict:
        if not startup.done():
            return {"type": "lifespan.startup"}
        await do_shutdown.wait()
        return {"type": "lifespan.shutdown"}

    async def send(message: dict) -> None:
        t = message.get("type", "")
        if t == "lifespan.startup.complete" and not startup.done():
            startup.set_result(True)
        elif t == "lifespan.startup.failed" and not startup.done():
            startup.set_exception(RuntimeError(message.get("message", "lifespan startup failed")))
        elif t == "lifespan.shutdown.complete" and not shutdown.done():
            shutdown.set_result(None)

    async def _run() -> None:
        try:
            await app({"type": "lifespan", "asgi": {"version": "3.0"}}, receive, send)
        except Exception as exc:
            if not startup.done():
                startup.set_exception(exc)
            if not shutdown.done():
                shutdown.set_result(None)
        finally:
            # App exited without sending startup.complete → no lifespan support.
            if not startup.done():
                startup.set_result(False)
            if not shutdown.done():
                shutdown.set_result(None)

    task = asyncio.create_task(_run())
    try:
        supported = await startup
    except Exception:
        # Sub-app startup failed; cancel and drain the _run task before propagating
        # so we don't leak a suspended coroutine waiting on do_shutdown.wait().
        task.cancel()
        try:
            await task
        except (asyncio.CancelledError, Exception):
            pass
        raise
    if not supported:
        # App does not implement lifespan — proceed normally, matching agent/main.py behaviour.
        yield
        return

    try:
        yield
    finally:
        do_shutdown.set()
        try:
            await shutdown
        except Exception as exc:
            logger.warning("Sub-app lifespan shutdown error: %s", exc)
        await task


async def _guarded(coro_fn, *args, restart_delay: float = 5.0, critical: bool = False) -> None:
    """Run a coroutine function in a restart loop, catching unexpected exceptions.

    The consecutive restart counter resets whenever a run lasts at least restart_delay
    seconds, so transient failures spread over time do not accumulate toward the threshold.
    """
    global _ready
    consecutive_restarts = 0
    while True:
        _attempt_start = time.monotonic()
        try:
            await coro_fn(*args)
            return
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            if time.monotonic() - _attempt_start >= restart_delay:
                consecutive_restarts = 0
            consecutive_restarts += 1
            logger.error(f"Task {coro_fn.__name__!r} crashed: {exc!r} — restarting in {restart_delay}s (consecutive restart #{consecutive_restarts})")
            if backend_task_restarts_total is not None:
                backend_task_restarts_total.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID, task=coro_fn.__name__).inc()
            if critical and consecutive_restarts >= WORKER_MAX_RESTARTS:
                logger.error(f"Task {coro_fn.__name__!r} has crashed {consecutive_restarts} consecutive times — marking agent not ready")
                _ready = False
            await asyncio.sleep(restart_delay)


async def _event_loop_monitor() -> None:
    _interval = 1.0
    while True:
        _before = time.monotonic()
        await asyncio.sleep(_interval)
        lag = time.monotonic() - _before - _interval
        if lag > 0 and backend_event_loop_lag_seconds is not None:
            backend_event_loop_lag_seconds.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).observe(lag)


async def _set_ready_when_started(server: uvicorn.Server) -> None:
    while not server.started:
        await asyncio.sleep(0.05)
    global _ready
    _ready = True
    if backend_startup_duration_seconds is not None:
        backend_startup_duration_seconds.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).set(time.monotonic() - _startup_mono)
    logger.info(f"Backend agent {AGENT_NAME} is ready")


async def main():
    global start_time, _startup_mono
    start_time = datetime.now(timezone.utc)
    _startup_mono = time.monotonic()

    # Initialise OTel before the executor so every request gets a span if
    # enabled (#469). No-op when OTEL_ENABLED is falsy.
    from otel import init_otel_if_enabled
    init_otel_if_enabled(service_name=os.environ.get("OTEL_SERVICE_NAME") or f"claude-{os.environ.get('AGENT_OWNER', 'unknown')}")

    agent_card = build_agent_card()
    executor = AgentExecutor()
    _task_store_path = os.environ.get("TASK_STORE_PATH", "")
    if _task_store_path:
        logger.info("Using SqliteTaskStore at %s", _task_store_path)
        task_store = SqliteTaskStore(_task_store_path)
    else:
        logger.warning(
            "TASK_STORE_PATH is not set — using InMemoryTaskStore. "
            "In-flight A2A task state will be lost on process restart. "
            "Set TASK_STORE_PATH to a file path (e.g. /home/agent/logs/tasks.db) "
            "to enable persistence."
        )
        task_store = InMemoryTaskStore()
    request_handler = DefaultRequestHandler(
        agent_executor=executor,
        task_store=task_store,
    )
    a2a_app = A2AStarletteApplication(
        agent_card=agent_card,
        http_handler=request_handler,
    )
    a2a_built = a2a_app.build()

    if metrics_enabled:
        if backend_up is not None:
            backend_up.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).set(1.0)
        if backend_info is not None:
            backend_info.info({"version": AGENT_VERSION, "agent": AGENT_OWNER, "agent_id": AGENT_ID, "backend": _BACKEND_ID})
        if backend_uptime_seconds is not None:
            backend_uptime_seconds.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).set_function(lambda: (datetime.now(timezone.utc) - start_time).total_seconds())
        logger.info("Prometheus metrics enabled at /metrics")
    else:
        logger.warning(
            "METRICS_ENABLED is not set — Prometheus metrics are disabled. "
            "Set METRICS_ENABLED=1 to enable /metrics and all instrumentation."
        )

    async def metrics_handler(request: Request) -> Response:
        body = prometheus_client.exposition.generate_latest()
        return Response(content=body, media_type=prometheus_client.exposition.CONTENT_TYPE_LATEST)

    conversations_handler = make_conversations_handler(CONVERSATIONS_AUTH_TOKEN, CONVERSATION_LOG)
    trace_handler = make_trace_handler(CONVERSATIONS_AUTH_TOKEN, TRACE_LOG)

    _agent_description = load_agent_description()

    # Label schema shared by the per-request metrics (#790). Matches
    # gemini so cross-backend dashboards can union by (agent, agent_id,
    # backend, method).
    _MCP_METRIC_LABELS = {
        "agent": AGENT_OWNER,
        "agent_id": AGENT_ID,
        "backend": _BACKEND_ID,
    }

    async def mcp_handler(request: Request) -> JSONResponse:
        """Minimal MCP JSON-RPC server: initialize / tools/list / tools/call.

        Wrapped with per-request metrics (#790): every return path records
        ``backend_mcp_requests_total{method,status}`` and
        ``backend_mcp_request_duration_seconds{method}`` so operators can
        alert on rate / p95 latency without rewriting labels vs gemini.
        """
        import time as _time_for_mcp
        _mcp_start = _time_for_mcp.monotonic()
        _method_box: list[str] = ["unknown"]
        _status_box: list[str] = ["ok"]
        try:
            return await _mcp_handler_inner(request, _method_box, _status_box)
        except Exception:
            _status_box[0] = "error"
            raise
        finally:
            _elapsed = _time_for_mcp.monotonic() - _mcp_start
            try:
                if backend_mcp_requests_total is not None:
                    backend_mcp_requests_total.labels(
                        **_MCP_METRIC_LABELS,
                        method=_method_box[0],
                        status=_status_box[0],
                    ).inc()
                if backend_mcp_request_duration_seconds is not None:
                    backend_mcp_request_duration_seconds.labels(
                        **_MCP_METRIC_LABELS,
                        method=_method_box[0],
                    ).observe(_elapsed)
            except Exception:
                pass

    async def _mcp_handler_inner(
        request: Request,
        _method_box: list[str],
        _status_box: list[str],
    ) -> JSONResponse:
        # Gate on the same bearer token used by /conversations and /trace.
        # Fail-closed when the token is missing (#718) unless the operator
        # explicitly set CONVERSATIONS_AUTH_DISABLED=true for local dev;
        # previously an empty token silently disabled auth and any network
        # caller could drive the LLM via tools/call -> ask_agent (#518).
        if not CONVERSATIONS_AUTH_TOKEN:
            if not auth_disabled_escape_hatch():
                _status_box[0] = "auth_not_configured"
                return JSONResponse({"error": "auth not configured"}, status_code=503)
        else:
            header = request.headers.get("Authorization", "")
            if not hmac_mod.compare_digest(f"Bearer {CONVERSATIONS_AUTH_TOKEN}", header):
                _status_box[0] = "unauthorized"
                return JSONResponse({"error": "unauthorized"}, status_code=401)
        try:
            body = await request.json()
        except Exception:
            _status_box[0] = "parse_error"
            return JSONResponse({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "Parse error"}}, status_code=400)
        rpc_id = body.get("id")
        method = body.get("method", "")
        params = body.get("params") or {}
        # Populate method for the outer observer — captured AFTER the
        # parse succeeds so malformed bodies land under method='unknown'.
        if isinstance(method, str) and method:
            _method_box[0] = method

        if method == "initialize":
            return JSONResponse({
                "jsonrpc": "2.0",
                "id": rpc_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": AGENT_NAME, "version": AGENT_VERSION},
                },
            })

        if method == "tools/list":
            return JSONResponse({
                "jsonrpc": "2.0",
                "id": rpc_id,
                "result": {
                    "tools": [
                        {
                            "name": "ask_agent",
                            "description": _agent_description,
                            "inputSchema": {
                                "type": "object",
                                "properties": {
                                    "prompt": {"type": "string", "description": "The prompt to send to the agent."},
                                    "session_id": {
                                        "type": "string",
                                        "description": "Optional session identifier for conversation continuity (#596). "
                                                       "A valid UUID is passed through verbatim; any other string is "
                                                       "hashed via uuid5(NAMESPACE_URL, value). Omit for a fresh session.",
                                    },
                                    "max_tokens": {
                                        "type": "integer",
                                        "minimum": 1,
                                        "description": "Optional per-call token budget (#460). Positive integers only; "
                                                       "non-positive or invalid values are logged and ignored.",
                                    },
                                },
                                "required": ["prompt"],
                            },
                        }
                    ]
                },
            })

        if method == "tools/call":
            tool_name = params.get("name", "")
            if tool_name != "ask_agent":
                return JSONResponse({
                    "jsonrpc": "2.0",
                    "id": rpc_id,
                    "error": {"code": -32602, "message": f"Unknown tool: {tool_name!r}"},
                })
            arguments = params.get("arguments") or {}
            prompt = arguments.get("prompt", "")
            if not prompt:
                return JSONResponse({
                    "jsonrpc": "2.0",
                    "id": rpc_id,
                    "error": {"code": -32602, "message": "Missing required argument: prompt"},
                })
            # Optional max_tokens — same parsing semantics as the A2A path
            # (positive int; non-positive or invalid is logged and dropped) (#460).
            _max_tokens_raw = arguments.get("max_tokens")
            mcp_max_tokens: int | None = None
            if _max_tokens_raw is not None:
                try:
                    _parsed = int(_max_tokens_raw)
                    if _parsed <= 0:
                        logger.warning("MCP tools/call: max_tokens=%s is non-positive; ignoring (#460).", _parsed)
                    else:
                        mcp_max_tokens = _parsed
                except (ValueError, TypeError):
                    logger.warning("MCP tools/call: invalid max_tokens %r; ignoring.", _max_tokens_raw)
            # Session continuity (#596) + caller-bound derivation (#867).
            # Route /mcp session_id through shared.session_binding.derive_session_id
            # with a bearer-token fingerprint as caller_identity so two /mcp
            # callers presenting the same raw session_id do NOT collide when
            # SESSION_ID_SECRET is set. On endpoints without caller auth, or
            # without the secret, derive_session_id falls back to the legacy
            # uuid5 derivation for backward compatibility.
            _raw_sid = "".join(
                c for c in str(arguments.get("session_id") or "").strip()[:256] if c >= " "
            )
            _bearer_header = request.headers.get("Authorization", "")
            _bearer_token = (
                _bearer_header[len("Bearer "):]
                if _bearer_header.startswith("Bearer ")
                else ""
            )
            _caller_identity = (
                hashlib.sha256(_bearer_token.encode("utf-8")).hexdigest()
                if _bearer_token
                else None
            )
            session_id = derive_session_id(_raw_sid, caller_identity=_caller_identity)
            try:
                from executor import run as _run_query_for_mcp
                response = await _run_query_for_mcp(
                    prompt,
                    session_id,
                    executor._sessions,
                    executor._mcp_servers,
                    executor._agent_md_content,
                    model=None,
                    max_tokens=mcp_max_tokens,
                )
            except Exception as exc:
                logger.error(f"MCP tools/call error: {exc!r}")
                return JSONResponse({
                    "jsonrpc": "2.0",
                    "id": rpc_id,
                    # Generic message — full exception detail is logged server-side
                    # (line above) but not leaked to MCP clients (#455).
                    "error": {"code": -32603, "message": "Internal server error"},
                })
            return JSONResponse({
                "jsonrpc": "2.0",
                "id": rpc_id,
                "result": {"content": [{"type": "text", "text": response}]},
            })

        return JSONResponse({
            "jsonrpc": "2.0",
            "id": rpc_id,
            "error": {"code": -32601, "message": f"Method not found: {method!r}"},
        })

    # OTel in-memory span store (#otel-in-cluster). Serves the Jaeger v1
    # shape so the harness's fan-out aggregator can merge backend spans
    # into the cross-pod trace view.
    #
    # Gated on CONVERSATIONS_AUTH_TOKEN (#709) to match /conversations, /trace,
    # and /mcp. Span attributes carry session IDs (bearer-equivalent), tool
    # input-derived fields, and agent identity — information disclosure across
    # pods/tenants was possible when these routes were unauthenticated.
    def _require_traces_auth(request: Request) -> JSONResponse | None:
        # Fail-closed when the token is missing unless the escape hatch is set (#718).
        if not CONVERSATIONS_AUTH_TOKEN:
            if auth_disabled_escape_hatch():
                return None
            return JSONResponse({"error": "auth not configured"}, status_code=503)
        header = request.headers.get("Authorization", "")
        if not hmac_mod.compare_digest(f"Bearer {CONVERSATIONS_AUTH_TOKEN}", header):
            return JSONResponse({"error": "unauthorized"}, status_code=401)
        return None

    async def otel_traces_list_handler(request: Request) -> JSONResponse:
        unauthorized = _require_traces_auth(request)
        if unauthorized is not None:
            return unauthorized
        try:
            limit_raw = request.query_params.get("limit")
            limit = int(limit_raw) if limit_raw else 20
        except ValueError:
            limit = 20
        try:
            from otel import get_in_memory_traces  # type: ignore
            traces = get_in_memory_traces()
        except Exception:
            traces = []
        return JSONResponse({"data": traces[:limit], "total": len(traces)})

    async def otel_traces_detail_handler(request: Request) -> JSONResponse:
        unauthorized = _require_traces_auth(request)
        if unauthorized is not None:
            return unauthorized
        trace_id = request.path_params.get("trace_id") or ""
        try:
            from otel import get_in_memory_traces  # type: ignore
            traces = get_in_memory_traces()
        except Exception:
            traces = []
        match = next((t for t in traces if t.get("traceID") == trace_id), None)
        if match is None:
            return JSONResponse({"data": [], "total": 0}, status_code=404)
        return JSONResponse({"data": [match], "total": 1})

    _routes = [
        Route("/health", health),
        Route("/conversations", conversations_handler, methods=["GET"]),
        Route("/trace", trace_handler, methods=["GET"]),
        Route("/mcp", mcp_handler, methods=["GET", "POST"]),
        Route("/api/traces", otel_traces_list_handler, methods=["GET"]),
        Route("/api/traces/{trace_id}", otel_traces_detail_handler, methods=["GET"]),
    ]
    # Metrics live on a dedicated port (:9000 by default, configurable via
    # METRICS_PORT), NOT on the main app listener (#643, #646). Started
    # inside the lifespan hook below.
    _routes.append(Mount("/", app=a2a_built))

    @asynccontextmanager
    async def lifespan(_app: Starlette):
        async with AsyncExitStack() as stack:
            for route in _routes:
                if isinstance(route, Mount) and route.path == "/":
                    await stack.enter_async_context(_sub_app_lifespan(route.app))
            if metrics_enabled:
                from metrics_server import start_metrics_server

                start_metrics_server(metrics_handler, logger=logger)
            try:
                yield
            finally:
                await executor.close()
                # Flush the SQLite WAL and release the connection on
                # graceful shutdown (#713). Guarded on close() presence
                # so InMemoryTaskStore (no close method) still works.
                _close = getattr(task_store, "close", None)
                if callable(_close):
                    try:
                        await _close()
                    except Exception as _close_exc:
                        logger.warning("task_store close error: %r", _close_exc)

    full_app = Starlette(routes=_routes, lifespan=lifespan)
    # Wrap with the ASGI middleware that extracts the inbound traceparent
    # so the A2A SDK's @trace_class spans become children of the harness
    # trace rather than orphaned roots (#otel-cross-pod).
    from otel import TraceparentASGIMiddleware
    full_app = TraceparentASGIMiddleware(full_app)

    logger.info(f"Starting {AGENT_NAME} on {AGENT_HOST}:{BACKEND_PORT}")
    config = uvicorn.Config(full_app, host=AGENT_HOST, port=BACKEND_PORT)
    server = uvicorn.Server(config)

    # Start MCP watcher tasks
    # These watchers (hooks/MCP/agent_md reloaders) are required for correct
    # operation — a persistently crashing watcher should take readiness down
    # via WORKER_MAX_RESTARTS rather than silently crash-loop forever (#585).
    for _w in executor._mcp_watchers():
        _mcp_task = asyncio.create_task(_guarded(_w, critical=True))
        _mcp_task.add_done_callback(
            lambda t, _wn=_w.__name__: logger.error(f"MCP watcher {_wn!r} exited unexpectedly: {t.exception()!r}")
            if not t.cancelled() and t.exception() is not None
            else None
        )
        executor._mcp_watcher_tasks.append(_mcp_task)

    await asyncio.gather(
        server.serve(),
        _guarded(_event_loop_monitor),
        _set_ready_when_started(server),
    )


if __name__ == "__main__":
    asyncio.run(main())

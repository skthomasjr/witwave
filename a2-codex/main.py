import asyncio
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
from conversations import make_conversations_handler, make_trace_handler
import executor as _executor_module
from executor import AgentExecutor
from metrics import (
    a2_event_loop_lag_seconds,
    a2_health_checks_total,
    a2_info,
    a2_startup_duration_seconds,
    a2_task_restarts_total,
    a2_up,
    a2_uptime_seconds,
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

AGENT_NAME = os.environ.get("AGENT_NAME", "a2-codex")
AGENT_HOST = os.environ.get("AGENT_HOST", "0.0.0.0")
BACKEND_PORT = int(os.environ.get("BACKEND_PORT", "8080"))
AGENT_URL = os.environ.get("AGENT_URL", f"http://localhost:{BACKEND_PORT}/")
AGENT_VERSION = os.environ.get("AGENT_VERSION", "0.1.0")
CONVERSATION_LOG = os.environ.get("CONVERSATION_LOG", "/home/agent/logs/conversation.jsonl")
TRACE_LOG = os.environ.get("TRACE_LOG", "/home/agent/logs/trace.jsonl")
AGENT_OWNER = os.environ.get("AGENT_OWNER", AGENT_NAME)
AGENT_ID = os.environ.get("AGENT_ID", "codex")
_BACKEND_ID = "codex"
metrics_enabled = bool(os.environ.get("METRICS_ENABLED"))
WORKER_MAX_RESTARTS = int(os.environ.get("WORKER_MAX_RESTARTS", "5"))
CONVERSATIONS_AUTH_TOKEN = os.environ.get("CONVERSATIONS_AUTH_TOKEN", "")

_ready: bool = False
_startup_mono: float = 0.0
start_time: datetime = datetime.now(timezone.utc)


def load_agent_description() -> str:
    try:
        with open("/home/agent/agent-card.md") as f:
            return f.read()
    except OSError:
        return os.environ.get("AGENT_DESCRIPTION", "A Codex backend agent.")


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
                description="General-purpose task execution via Codex.",
                tags=["general", "codex"],
            )
        ],
    )


async def health(request: Request) -> JSONResponse:
    if a2_health_checks_total is not None:
        a2_health_checks_total.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID, probe="health").inc()
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
    supported = await startup
    if not supported:
        logger.critical("A2A sub-app lifespan startup failed — aborting server startup")
        task.cancel()
        raise RuntimeError("A2A sub-app lifespan startup failed")

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
            if a2_task_restarts_total is not None:
                a2_task_restarts_total.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID, task=coro_fn.__name__).inc()
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
        if lag > 0 and a2_event_loop_lag_seconds is not None:
            a2_event_loop_lag_seconds.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).observe(lag)


async def _set_ready_when_started(server: uvicorn.Server) -> None:
    while not server.started:
        await asyncio.sleep(0.05)
    global _ready
    _ready = True
    if a2_startup_duration_seconds is not None:
        a2_startup_duration_seconds.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).set(time.monotonic() - _startup_mono)
    logger.info(f"Backend agent {AGENT_NAME} is ready")


async def main():
    global start_time, _startup_mono
    start_time = datetime.now(timezone.utc)
    _startup_mono = time.monotonic()

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
        if a2_up is not None:
            a2_up.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).set(1.0)
        if a2_info is not None:
            a2_info.info({"version": AGENT_VERSION, "agent": AGENT_OWNER, "agent_id": AGENT_ID, "backend": _BACKEND_ID})
        if a2_uptime_seconds is not None:
            a2_uptime_seconds.labels(agent=AGENT_OWNER, agent_id=AGENT_ID, backend=_BACKEND_ID).set_function(lambda: (datetime.now(timezone.utc) - start_time).total_seconds())
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

    async def mcp_handler(request: Request) -> JSONResponse:
        """Minimal MCP JSON-RPC server: initialize / tools/list / tools/call."""
        try:
            body = await request.json()
        except Exception:
            return JSONResponse({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "Parse error"}}, status_code=400)
        rpc_id = body.get("id")
        method = body.get("method", "")
        params = body.get("params") or {}

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
                                "properties": {"prompt": {"type": "string", "description": "The prompt to send to the agent."}},
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
            session_id = str(uuid.uuid4())
            try:
                from executor import run as _run_for_mcp
                response = await _run_for_mcp(
                    prompt,
                    session_id,
                    executor._sessions,
                    executor._agent_md_content,
                    model=None,
                )
            except Exception as exc:
                logger.error(f"MCP tools/call error: {exc!r}")
                return JSONResponse({
                    "jsonrpc": "2.0",
                    "id": rpc_id,
                    "error": {"code": -32603, "message": f"Internal error: {exc}"},
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

    _routes = [
        Route("/health", health),
        Route("/conversations", conversations_handler, methods=["GET"]),
        Route("/trace", trace_handler, methods=["GET"]),
        Route("/mcp", mcp_handler, methods=["GET", "POST"]),
    ]
    if metrics_enabled:
        _routes.append(Route("/metrics", metrics_handler))
    _routes.append(Mount("/", app=a2a_built))

    @asynccontextmanager
    async def lifespan(_app: Starlette):
        async with AsyncExitStack() as stack:
            for route in _routes:
                if isinstance(route, Mount) and route.path == "/":
                    await stack.enter_async_context(_sub_app_lifespan(route.app))
            try:
                yield
            finally:
                if _executor_module._computer is not None:
                    try:
                        await _executor_module._computer.close()
                    except Exception as exc:
                        logger.warning("PlaywrightComputer close error: %s", exc)
                await executor.close()

    full_app = Starlette(routes=_routes, lifespan=lifespan)

    logger.info(f"Starting {AGENT_NAME} on {AGENT_HOST}:{BACKEND_PORT}")
    config = uvicorn.Config(full_app, host=AGENT_HOST, port=BACKEND_PORT)
    server = uvicorn.Server(config)

    # Start MCP watcher tasks (none for codex, but kept for structural parity)
    for _w in executor._mcp_watchers():
        _mcp_task = asyncio.create_task(_guarded(_w))
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

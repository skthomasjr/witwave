import asyncio
import logging
import os
import time
from contextlib import AsyncExitStack, asynccontextmanager
from datetime import datetime, timezone

import prometheus_client
import uvicorn
from a2a.server.apps import A2AStarletteApplication
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.tasks import InMemoryTaskStore
from a2a.types import (
    AgentCapabilities,
    AgentCard,
    AgentSkill,
)
from agenda import AgendaRunner
from bus import MessageBus
from executor import AgentExecutor
from heartbeat import heartbeat_runner
from metrics import (
    agent_bus_consumer_idle_seconds,
    agent_bus_error_processing_duration_seconds,
    agent_bus_errors_total,
    agent_bus_last_processed_timestamp_seconds,
    agent_bus_messages_total,
    agent_bus_processing_duration_seconds,
    agent_bus_wait_seconds,
    agent_event_loop_lag_seconds,
    agent_health_checks_total,
    agent_info,
    agent_startup_duration_seconds,
    agent_up,
    agent_uptime_seconds,
)
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.routing import Mount, Route

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger(__name__)

AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")
AGENT_HOST = os.environ.get("AGENT_HOST", "0.0.0.0")
AGENT_PORT = int(os.environ.get("AGENT_PORT", "8000"))
AGENT_URL = os.environ.get("AGENT_URL", f"http://localhost:{AGENT_PORT}/")
AGENT_VERSION = os.environ.get("AGENT_VERSION", "0.1.0")
metrics_enabled = bool(os.environ.get("METRICS_ENABLED"))

_ready: bool = False
_startup_mono: float = 0.0
start_time: datetime = datetime.now(timezone.utc)


def load_agent_description() -> str:
    path = os.environ.get("AGENT_MD_PATH", "/home/agent/.nyx/agent-card.md")
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return os.environ.get("AGENT_DESCRIPTION", "A Claude Code agent.")


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
                description="General-purpose task execution.",
                tags=["general"],
            )
        ],
    )


async def health_start(request: Request) -> JSONResponse:
    if agent_health_checks_total is not None:
        agent_health_checks_total.labels(probe="start").inc()
    if _ready:
        return JSONResponse({"status": "ok"})
    return JSONResponse({"status": "starting"}, status_code=503)


async def health_live(request: Request) -> JSONResponse:
    if agent_health_checks_total is not None:
        agent_health_checks_total.labels(probe="live").inc()
    elapsed = (datetime.now(timezone.utc) - start_time).total_seconds()
    return JSONResponse({"status": "ok", "agent": AGENT_NAME, "uptime_seconds": elapsed})


async def health_ready(request: Request) -> JSONResponse:
    if agent_health_checks_total is not None:
        agent_health_checks_total.labels(probe="ready").inc()
    if _ready:
        return JSONResponse({"status": "ready"})
    return JSONResponse({"status": "starting"}, status_code=503)


@asynccontextmanager
async def _sub_app_lifespan(app):
    """Drive the ASGI lifespan protocol on a sub-app.

    Uses the standard ASGI lifespan scope rather than Starlette-private
    attributes, so this remains correct across Starlette version upgrades.
    Apps that do not support the lifespan scope return before sending
    ``lifespan.startup.complete``; the finally block detects this and skips
    propagation without raising.
    """
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
                shutdown.set_exception(exc)
        finally:
            # App exited without sending startup.complete → no lifespan support.
            if not startup.done():
                startup.set_result(False)
            if not shutdown.done():
                shutdown.set_result(None)

    task = asyncio.create_task(_run())
    supported = await startup
    if not supported:
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


async def _guarded(coro_fn, *args, restart_delay: float = 5.0) -> None:
    """Run a coroutine function in a restart loop, catching unexpected exceptions."""
    while True:
        try:
            await coro_fn(*args)
            return  # clean exit — do not restart
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            logger.error(f"Task {coro_fn.__name__!r} crashed: {exc!r} — restarting in {restart_delay}s")
            await asyncio.sleep(restart_delay)


async def _event_loop_monitor() -> None:
    _interval = 1.0
    while True:
        _before = time.monotonic()
        await asyncio.sleep(_interval)
        lag = time.monotonic() - _before - _interval
        if lag > 0 and agent_event_loop_lag_seconds is not None:
            agent_event_loop_lag_seconds.observe(lag)


async def bus_worker(bus: MessageBus, executor: AgentExecutor) -> None:
    logger.info("Message bus worker started.")
    _idle_start = time.monotonic()
    while True:
        message = await bus.receive()
        if agent_bus_consumer_idle_seconds is not None:
            agent_bus_consumer_idle_seconds.observe(time.monotonic() - _idle_start)
        if agent_bus_messages_total is not None:
            agent_bus_messages_total.labels(kind=message.kind).inc()
        if agent_bus_wait_seconds is not None and message.enqueued_at:
            agent_bus_wait_seconds.labels(kind=message.kind).observe(time.monotonic() - message.enqueued_at)
        t0 = time.monotonic()
        try:
            await executor.process_bus(message)
        except Exception as e:
            logger.error(f"Bus worker error: {e}")
            if agent_bus_errors_total is not None:
                agent_bus_errors_total.inc()
            if agent_bus_error_processing_duration_seconds is not None:
                agent_bus_error_processing_duration_seconds.labels(kind=message.kind).observe(time.monotonic() - t0)
        finally:
            if message.result is not None and not message.result.done():
                message.result.cancel()
            if agent_bus_processing_duration_seconds is not None:
                agent_bus_processing_duration_seconds.labels(kind=message.kind).observe(time.monotonic() - t0)
            if agent_bus_last_processed_timestamp_seconds is not None:
                agent_bus_last_processed_timestamp_seconds.set(time.time())
            _idle_start = time.monotonic()


async def _set_ready_when_started(server: uvicorn.Server) -> None:
    while not server.started:
        await asyncio.sleep(0.05)
    global _ready
    _ready = True
    if agent_startup_duration_seconds is not None:
        agent_startup_duration_seconds.set(time.monotonic() - _startup_mono)
    logger.info(f"Agent {AGENT_NAME} is ready")


async def main():
    global start_time, _startup_mono
    start_time = datetime.now(timezone.utc)
    _startup_mono = time.monotonic()

    bus = MessageBus()
    agent_card = build_agent_card()
    executor = AgentExecutor()
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

    metrics_asgi = None
    if metrics_enabled:
        if agent_up is not None:
            agent_up.labels(agent=AGENT_NAME).set(1.0)
        if agent_info is not None:
            agent_info.info({"version": AGENT_VERSION, "agent": AGENT_NAME})
        if agent_uptime_seconds is not None:
            agent_uptime_seconds.set_function(lambda: (datetime.now(timezone.utc) - start_time).total_seconds())
        metrics_asgi = prometheus_client.make_asgi_app()
        logger.info("Prometheus metrics enabled at /metrics")

    _routes = [
        Route("/health/start", health_start),
        Route("/health/live", health_live),
        Route("/health/ready", health_ready),
    ]
    if metrics_enabled:
        _routes.append(Mount("/metrics", app=metrics_asgi))
    _routes.append(Mount("/", app=a2a_built))

    @asynccontextmanager
    async def lifespan(_app: Starlette):
        """Forward ASGI lifespan events to all mounted sub-apps.

        Starlette's Router collects on_startup/on_shutdown from mounted apps
        via Mount.on_startup, but does not forward the ASGI lifespan scope.
        A mounted app that registers lifecycle hooks via the lifespan context
        manager API would have those hooks silently skipped.  Iterating the
        routes and driving each sub-app's lifespan via the standard ASGI
        lifespan scope protocol ensures startup and shutdown are propagated
        regardless of which API is used, and remains correct across Starlette
        version upgrades and as new mounts are added in the future.
        """
        async with AsyncExitStack() as stack:
            for route in _routes:
                if isinstance(route, Mount):
                    await stack.enter_async_context(_sub_app_lifespan(route.app))
            yield

    full_app = Starlette(routes=_routes, lifespan=lifespan)

    logger.info(f"Starting {AGENT_NAME} on {AGENT_HOST}:{AGENT_PORT}")
    config = uvicorn.Config(full_app, host=AGENT_HOST, port=AGENT_PORT)
    server = uvicorn.Server(config)

    agenda_runner = AgendaRunner(bus)

    await asyncio.gather(
        server.serve(),
        _guarded(bus_worker, bus, executor),
        _guarded(heartbeat_runner, bus),
        _guarded(agenda_runner.run),
        _guarded(_event_loop_monitor),
        *executor._mcp_watchers(),
        _set_ready_when_started(server),
    )


if __name__ == "__main__":
    asyncio.run(main())

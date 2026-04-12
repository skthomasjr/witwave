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
import prometheus_client.exposition
import uvicorn
from a2a.server.apps import A2AStarletteApplication
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.tasks import InMemoryTaskStore
from a2a.types import (
    AgentCapabilities,
    AgentCard,
    AgentSkill,
)
from continuations import ContinuationRunner
from jobs import JobRunner
from tasks import TaskRunner
from triggers import TriggerItem, TriggerRunner
from webhooks import WebhookRunner
from bus import MessageBus
from executor import AgentExecutor, run as executor_run
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
    agent_task_restarts_total,
    agent_triggers_requests_total,
    agent_up,
    agent_uptime_seconds,
)
from conversations_proxy import fetch_backend_conversations, fetch_backend_trace
from metrics_proxy import fetch_backend_metrics
from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.cors import CORSMiddleware
from starlette.requests import Request
from starlette.responses import JSONResponse, Response
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
WORKER_MAX_RESTARTS = int(os.environ.get("WORKER_MAX_RESTARTS", "5"))

_ready: bool = False
_startup_mono: float = 0.0
start_time: datetime = datetime.now(timezone.utc)


def _check_trigger_auth(request: Request, item: TriggerItem, body_bytes: bytes) -> bool:
    """Validate trigger request auth. HMAC takes priority over Bearer token."""
    if item.secret_env_var:
        secret = os.environ.get(item.secret_env_var, "")
        if not secret:
            logger.warning(
                f"Trigger '{item.name}': 'secret-env-var' is set to {item.secret_env_var!r} "
                "but the environment variable is absent or empty — request rejected."
            )
            return False
        expected = "sha256=" + hmac_mod.new(secret.encode(), body_bytes, hashlib.sha256).hexdigest()
        return hmac_mod.compare_digest(expected, request.headers.get("X-Hub-Signature-256", ""))

    auth_token = os.environ.get("TRIGGERS_AUTH_TOKEN", "")
    if auth_token:
        header = request.headers.get("Authorization", "")
        return hmac_mod.compare_digest(f"Bearer {auth_token}", header)

    return True


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

    If critical=True, sets _ready=False after WORKER_MAX_RESTARTS consecutive crashes,
    signalling to Kubernetes that the pod can no longer serve traffic.

    The consecutive restart counter resets whenever a run lasts at least restart_delay
    seconds, so transient failures spread over time do not accumulate toward the threshold.
    """
    global _ready
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
            consecutive_restarts += 1
            logger.error(f"Task {coro_fn.__name__!r} crashed: {exc!r} — restarting in {restart_delay}s (consecutive restart #{consecutive_restarts})")
            if agent_task_restarts_total is not None:
                agent_task_restarts_total.labels(task=coro_fn.__name__).inc()
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

    if metrics_enabled:
        if agent_up is not None:
            agent_up.labels(agent=AGENT_NAME).set(1.0)
        if agent_info is not None:
            agent_info.info({"version": AGENT_VERSION, "agent": AGENT_NAME})
        if agent_uptime_seconds is not None:
            agent_uptime_seconds.set_function(lambda: (datetime.now(timezone.utc) - start_time).total_seconds())
        logger.info("Prometheus metrics enabled at /metrics")

    async def metrics_handler(request: Request) -> Response:
        """Return nyx-agent metrics plus relabelled metrics from all reachable backends."""
        from backends.config import load_backends_config
        nyx_output = prometheus_client.exposition.generate_latest().decode("utf-8")
        try:
            backend_configs = load_backends_config()
        except Exception:
            backend_configs = []
        backend_output = await fetch_backend_metrics(backend_configs)
        body = nyx_output + backend_output
        return Response(
            content=body,
            media_type=prometheus_client.exposition.CONTENT_TYPE_LATEST,
        )

    async def agents_handler(request: Request) -> JSONResponse:
        from backends.config import load_backends_config
        try:
            backend_configs = load_backends_config()
        except Exception:
            backend_configs = []
        agents = []
        # Own card
        own_card = build_agent_card()
        agents.append({
            "id": AGENT_NAME,
            "url": AGENT_URL,
            "role": "nyx",
            "card": own_card.model_dump() if hasattr(own_card, "model_dump") else vars(own_card),
        })
        # Backend cards
        import httpx
        async with httpx.AsyncClient(timeout=5.0) as client:
            for backend in backend_configs:
                if not backend.url:
                    continue
                entry = {"id": backend.id, "url": backend.url, "role": "backend", "model": backend.model, "card": None}
                try:
                    resp = await client.get(backend.url.rstrip("/") + "/.well-known/agent.json")
                    if resp.status_code == 200:
                        entry["card"] = resp.json()
                except Exception as exc:
                    logger.debug(f"Backend {backend.id!r} agent card unreachable: {exc!r}")
                agents.append(entry)
        return JSONResponse(agents)

    async def conversations_handler(request: Request) -> JSONResponse:
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        from backends.config import load_backends_config
        try:
            backend_configs = load_backends_config()
        except Exception:
            backend_configs = []
        entries = await fetch_backend_conversations(backend_configs, since=since, limit=limit_n)
        return JSONResponse(entries)

    async def trace_handler(request: Request) -> JSONResponse:
        since = request.query_params.get("since")
        limit = request.query_params.get("limit")
        try:
            limit_n = int(limit) if limit else None
        except ValueError:
            return JSONResponse({"error": "invalid limit"}, status_code=400)
        from backends.config import load_backends_config
        try:
            backend_configs = load_backends_config()
        except Exception:
            backend_configs = []
        entries = await fetch_backend_trace(backend_configs, since=since, limit=limit_n)
        return JSONResponse(entries)

    async def proxy_handler(request: Request) -> Response:
        """Proxy an A2A JSON-RPC request to a named team member, optionally targeting a specific backend."""
        import json as _json
        import httpx
        agent_name = request.path_params["agent_name"]
        backend_id = request.query_params.get("backend")
        manifest_path = os.environ.get("MANIFEST_PATH", "/home/agent/manifest.json")
        try:
            with open(manifest_path) as f:
                manifest = _json.load(f)
            team = manifest.get("team", [])
        except Exception:
            team = []
        target_url = next((m.get("url") for m in team if m.get("name") == agent_name), None)
        if not target_url:
            return JSONResponse({"error": f"agent {agent_name!r} not found"}, status_code=404)
        # If a specific backend is requested, resolve its URL from backend.yaml of the target agent
        if backend_id:
            try:
                from backends.config import load_backends_config as _lbc
                # We can only load our own backend.yaml; for other agents fan out via their /agents endpoint
                async with httpx.AsyncClient(timeout=5.0) as _c:
                    _r = await _c.get(target_url.rstrip("/") + "/agents")
                    if _r.status_code == 200:
                        _agents = _r.json()
                        _b = next((a for a in _agents if a.get("role") == "backend" and a.get("id") == backend_id), None)
                        if _b and _b.get("url"):
                            target_url = _b["url"]
            except Exception:
                pass
        body = await request.body()
        if len(body) > 1_048_576:
            return JSONResponse({"error": "request body too large"}, status_code=413)
        async with httpx.AsyncClient(timeout=120.0) as client:
            try:
                resp = await client.post(
                    target_url.rstrip("/") + "/",
                    content=body,
                    headers={"Content-Type": "application/json"},
                )
                return Response(content=resp.content, status_code=resp.status_code, media_type="application/json")
            except Exception as exc:
                return JSONResponse({"error": str(exc)}, status_code=502)

    async def conversations_proxy_handler(request: Request) -> JSONResponse:
        """Proxy /conversations from a named team member's backend, optionally filtered by backend id."""
        import json as _json
        import httpx
        agent_name = request.path_params["agent_name"]
        backend_id = request.query_params.get("backend")
        since = request.query_params.get("since")
        limit = request.query_params.get("limit", "200")
        manifest_path = os.environ.get("MANIFEST_PATH", "/home/agent/manifest.json")
        try:
            with open(manifest_path) as f:
                manifest = _json.load(f)
            team = manifest.get("team", [])
        except Exception:
            team = []
        target_url = next((m.get("url") for m in team if m.get("name") == agent_name), None)
        if not target_url:
            return JSONResponse({"error": f"agent {agent_name!r} not found"}, status_code=404)
        # Resolve specific backend URL if requested
        if backend_id:
            try:
                async with httpx.AsyncClient(timeout=5.0) as _c:
                    _r = await _c.get(target_url.rstrip("/") + "/agents")
                    if _r.status_code == 200:
                        _agents = _r.json()
                        _b = next((a for a in _agents if a.get("role") == "backend" and a.get("id") == backend_id), None)
                        if _b and _b.get("url"):
                            target_url = _b["url"]
            except Exception:
                pass
        params: dict = {"limit": limit}
        if since:
            params["since"] = since
        async with httpx.AsyncClient(timeout=10.0) as client:
            try:
                resp = await client.get(target_url.rstrip("/") + "/conversations", params=params)
                return JSONResponse(resp.json(), status_code=resp.status_code)
            except Exception as exc:
                return JSONResponse({"error": str(exc)}, status_code=502)

    async def team_handler(request: Request) -> JSONResponse:
        """Return agent cards for all team members by reading manifest.json and fanning out to /agents."""
        import json as _json
        import httpx
        manifest_path = os.environ.get("MANIFEST_PATH", "/home/agent/manifest.json")
        try:
            with open(manifest_path) as f:
                manifest = _json.load(f)
            team = manifest.get("team", [])
        except Exception:
            team = [{"name": AGENT_NAME, "url": AGENT_URL}]
        result = []
        async with httpx.AsyncClient(timeout=5.0) as client:
            for member in team:
                url = member.get("url", "").rstrip("/")
                if not url:
                    continue
                try:
                    resp = await client.get(f"{url}/agents")
                    if resp.status_code == 200:
                        agents = resp.json()
                        result.append({"name": member.get("name"), "url": url, "agents": agents})
                    else:
                        result.append({"name": member.get("name"), "url": url, "agents": [], "error": f"HTTP {resp.status_code}"})
                except Exception as exc:
                    result.append({"name": member.get("name"), "url": url, "agents": [], "error": str(exc)})
        return JSONResponse(result)

    trigger_runner = TriggerRunner()

    async def triggers_discovery(request: Request) -> JSONResponse:
        items = trigger_runner.items_by_endpoint()
        payload = [
            {
                "endpoint": item.endpoint,
                "name": item.name,
                "description": item.description,
                "methods": ["POST"],
                "session_id": item.session_id,
            }
            for item in items.values()
        ]
        return JSONResponse(payload)

    async def trigger_handler(request: Request) -> JSONResponse:
        endpoint = request.path_params["endpoint"]
        # TODO(#71): HEAD /triggers/{endpoint} returns 405 (Starlette default). Should it return 200 with metadata?

        items = trigger_runner.items_by_endpoint()
        item = items.get(endpoint)
        if item is None:
            if agent_triggers_requests_total is not None:
                agent_triggers_requests_total.labels(method=request.method, code="404").inc()
            return JSONResponse({"error": "not found", "endpoint": endpoint}, status_code=404)

        if endpoint in trigger_runner._running:
            if agent_triggers_requests_total is not None:
                agent_triggers_requests_total.labels(method=request.method, code="409").inc()
            return JSONResponse({"error": "already running", "endpoint": endpoint}, status_code=409)

        # Claim the slot before any await to prevent concurrent requests from
        # both passing the 409 check above.
        trigger_runner._running.add(endpoint)
        try:
            body_bytes = await request.body()
        except Exception:
            trigger_runner._running.discard(endpoint)
            if agent_triggers_requests_total is not None:
                agent_triggers_requests_total.labels(method=request.method, code="500").inc()
            raise

        if not _check_trigger_auth(request, item, body_bytes):
            trigger_runner._running.discard(endpoint)
            if agent_triggers_requests_total is not None:
                agent_triggers_requests_total.labels(method=request.method, code="401").inc()
            return JSONResponse({"error": "unauthorized"}, status_code=401)

        # Build prompt from request
        filtered_headers = "\n".join(
            f"{k}: {v}"
            for k, v in request.headers.items()
            if k.lower() not in ("authorization", "x-hub-signature-256", "cookie")
        )
        try:
            body_text = body_bytes[:262144].decode("utf-8")
        except (UnicodeDecodeError, ValueError):
            body_text = body_bytes[:4096].hex()

        prompt = (
            f"Trigger: {item.name}\n\n"
            f"Request:\n"
            f"{request.method} {request.url.path}\n"
            f"{filtered_headers}\n\n"
            f"{body_text}\n\n"
            f"---\n\n"
            f"{item.content}"
        )

        delivery_id = str(uuid.uuid4())

        async def _fire() -> None:
            _fire_start = time.monotonic()
            _response = ""
            _success = False
            _error: str | None = None
            _model = None
            backend_id = None
            try:
                _entry = executor._routing_entry_for_kind(f"trigger:{endpoint}")
                backend_id = item.backend_id or (_entry.agent if _entry else None)
                _resolved_id = backend_id or executor._default_backend_id
                _model = executor._resolve_model(item.model, _entry, _resolved_id)
                _response = await executor_run(
                    prompt,
                    item.session_id,
                    executor._sessions,
                    executor._backends,
                    executor._default_backend_id,
                    backend_id=backend_id,
                    model=_model,
                )
                _success = True
            except Exception as exc:
                _error = repr(exc)
                logger.error(f"Trigger '{item.name}' execution error: {exc!r}")
            finally:
                trigger_runner._running.discard(endpoint)
                _opc_task = asyncio.create_task(executor.on_prompt_completed(
                    source="trigger",
                    kind=f"trigger:{endpoint}",
                    session_id=item.session_id,
                    success=_success,
                    response=_response,
                    duration_seconds=time.monotonic() - _fire_start,
                    error=_error,
                    model=_model,
                ))
                executor._background_tasks.add(_opc_task)
                _opc_task.add_done_callback(executor._background_tasks.discard)
                _opc_task.add_done_callback(
                    lambda t: logger.error(f"on_prompt_completed error: {t.exception()}")
                    if not t.cancelled() and t.exception() is not None
                    else None
                )

        try:
            _task = asyncio.ensure_future(_fire())
            executor._background_tasks.add(_task)
            _task.add_done_callback(executor._background_tasks.discard)
            _task.add_done_callback(
                lambda t: logger.error(f"Trigger '{item.name}' task exited unexpectedly: {t.exception()!r}")
                if not t.cancelled() and t.exception() is not None
                else None
            )
        except Exception as exc:
            trigger_runner._running.discard(endpoint)
            logger.error(f"Trigger '{item.name}': failed to schedule background task: {exc!r}")
            if agent_triggers_requests_total is not None:
                agent_triggers_requests_total.labels(method=request.method, code="500").inc()
            return JSONResponse({"error": "internal error"}, status_code=500)

        if agent_triggers_requests_total is not None:
            agent_triggers_requests_total.labels(method=request.method, code="202").inc()
        return JSONResponse(
            {"delivery_id": delivery_id, "session_id": item.session_id, "endpoint": endpoint},
            status_code=202,
        )

    _routes = [
        Route("/health/start", health_start),
        Route("/health/live", health_live),
        Route("/health/ready", health_ready),
        Route("/.well-known/agent-triggers.json", triggers_discovery, methods=["GET"]),
        Route("/triggers/{endpoint}", trigger_handler, methods=["POST"]),
        Route("/agents", agents_handler, methods=["GET"]),
        Route("/team", team_handler, methods=["GET"]),
        Route("/proxy/{agent_name}", proxy_handler, methods=["POST"]),
        Route("/conversations", conversations_handler, methods=["GET"]),
        Route("/conversations/{agent_name}", conversations_proxy_handler, methods=["GET"]),
        Route("/trace", trace_handler, methods=["GET"]),
    ]
    if metrics_enabled:
        _routes.append(Route("/metrics", metrics_handler, methods=["GET"]))
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

    full_app = Starlette(
        routes=_routes,
        lifespan=lifespan,
        middleware=[
            Middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["GET", "POST", "OPTIONS"], allow_headers=["*"]),
        ],
    )

    logger.info(f"Starting {AGENT_NAME} on {AGENT_HOST}:{AGENT_PORT}")
    config = uvicorn.Config(full_app, host=AGENT_HOST, port=AGENT_PORT)
    server = uvicorn.Server(config)

    job_runner = JobRunner(bus)
    task_runner = TaskRunner(bus)
    continuation_runner = ContinuationRunner()
    executor.set_continuation_runner(continuation_runner, bus)
    webhook_runner = WebhookRunner()
    executor.set_webhook_runner(webhook_runner)

    # Start MCP watcher tasks as tracked background tasks so backends_watcher
    # can cancel and replace them when backends are hot-reloaded.
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
        _guarded(bus_worker, bus, executor, critical=True),
        _guarded(heartbeat_runner, bus),
        _guarded(job_runner.run),
        _guarded(task_runner.run),
        _guarded(trigger_runner.run),
        _guarded(continuation_runner.run),
        _guarded(webhook_runner.run),
        _guarded(_event_loop_monitor),
        _guarded(executor.backends_watcher),
        _set_ready_when_started(server),
    )


if __name__ == "__main__":
    asyncio.run(main())

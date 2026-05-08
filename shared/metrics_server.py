"""Shared dedicated metrics-listener helper (#643).

Each long-running service container (harness, backends/a2-*, tools/*)
serves ``/metrics`` on a dedicated port — by default **9000** — instead of
sharing the main app listener. The split lets NetworkPolicy + auth posture
diverge cleanly between app traffic and monitoring scrapes.

Usage from a container's ``main.py``::

    from metrics_server import start_metrics_server

    if metrics_enabled:
        start_metrics_server(metrics_handler, logger=logger)

The helper binds to ``METRICS_PORT`` (default 9000) on ``0.0.0.0`` and
spawns a uvicorn server in a background asyncio task. It returns
immediately; the server runs for the lifetime of the process.

The handler passed in is the same Starlette ``Request -> Response``
coroutine each container already uses for ``/metrics``, so we don't
rewrite the generation / aggregation / auth logic — we just host it on a
second port.
"""

from __future__ import annotations

import asyncio
import logging
import os
import threading
from typing import Awaitable, Callable, Optional

from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import Response
from starlette.routing import Route

_DEFAULT_METRICS_PORT = 9000


def _resolve_metrics_port() -> int:
    """Return the port the metrics listener should bind to.

    ``METRICS_PORT`` env var wins; otherwise default 9000. When the value
    is non-numeric or out of range we log and fall back to the default so
    a misconfigured env doesn't crash the container.
    """
    raw = os.environ.get("METRICS_PORT", "").strip()
    if not raw:
        return _DEFAULT_METRICS_PORT
    try:
        port = int(raw)
    except ValueError:
        logging.getLogger(__name__).warning(
            "METRICS_PORT=%r is not an integer; falling back to %d",
            raw,
            _DEFAULT_METRICS_PORT,
        )
        return _DEFAULT_METRICS_PORT
    if port < 1 or port > 65535:
        logging.getLogger(__name__).warning(
            "METRICS_PORT=%d out of range; falling back to %d",
            port,
            _DEFAULT_METRICS_PORT,
        )
        return _DEFAULT_METRICS_PORT
    return port


def start_metrics_server_in_thread(
    handler: Callable[[Request], Awaitable[Response]],
    *,
    logger: Optional[logging.Logger] = None,
    port: Optional[int] = None,
    path: str = "/metrics",
) -> threading.Thread:
    """Same as :func:`start_metrics_server` but runs in a daemon thread with
    its own asyncio loop. Use this when the caller does not own the main
    event loop (e.g. FastMCP's ``mcp.run()`` which sets up its own loop
    internally).

    Constraint: the handler MUST NOT touch asyncio primitives bound to a
    different event loop (locks, events, futures). Pure ``prom_client``
    metric exposition — what MCP tools need today — is safe because the
    default registry is thread-safe.
    """
    if logger is None:
        logger = logging.getLogger(__name__)
    bind_port = port if port is not None else _resolve_metrics_port()

    async def _health(_request: Request) -> Response:
        return Response(content=b"ok", media_type="text/plain")

    app = Starlette(
        routes=[
            Route(path, handler, methods=["GET"]),
            Route("/health", _health, methods=["GET"]),
        ]
    )

    import uvicorn

    config = uvicorn.Config(
        app,
        host="0.0.0.0",  # nosec B104
        port=bind_port,
        log_level="warning",
        access_log=False,
    )
    server = uvicorn.Server(config)

    def _run() -> None:
        # uvicorn.Server.run() sets up its own asyncio loop inside the
        # thread. We never await on it from the caller's loop.
        server.run()

    thread = threading.Thread(target=_run, name="metrics-server", daemon=True)
    thread.start()
    # Best-effort graceful shutdown on Python interpreter exit (#1818).
    # Daemon threads are killed abruptly on hard exits, but a normal
    # exit (FastMCP's signal-handled SIGTERM path, atexit-driven module
    # teardown) will fire this hook and give uvicorn a chance to close
    # listening sockets and drain in-flight scrapes.
    import atexit

    def _shutdown_metrics() -> None:
        try:
            server.should_exit = True
            thread.join(timeout=5.0)
        except Exception as _exc:  # pragma: no cover
            logger.warning("metrics-server shutdown error: %r", _exc)

    atexit.register(_shutdown_metrics)
    logger.info(
        "Prometheus metrics enabled on dedicated listener :%d%s (thread mode)",
        bind_port,
        path,
    )
    return thread


def start_metrics_server(
    handler: Callable[[Request], Awaitable[Response]],
    *,
    logger: Optional[logging.Logger] = None,
    port: Optional[int] = None,
    path: str = "/metrics",
    extra_routes: Optional[list] = None,
) -> asyncio.Task:
    """Start a uvicorn server hosting ``handler`` at ``path`` in the background.

    Returns the asyncio task running the server. The caller typically
    ignores the return value — the listener is treated as a daemon and
    exits when the event loop exits.

    ``port`` overrides the resolved ``METRICS_PORT`` when supplied (useful
    for tests). ``path`` defaults to ``/metrics``; override for custom
    scrape paths.

    ``extra_routes`` are appended to the listener's route table (#924).
    Callers use this to host privileged internal endpoints (e.g. the
    harness ``/internal/events/hook-decision`` receiver) on the dedicated
    metrics port so a restrictive NetworkPolicy on the app port cannot be
    bypassed by in-pod peers spoofing the internal bearer token.

    The ``/health`` endpoint on this port responds with 200 OK so that a
    Kubernetes readiness probe pointed at the metrics port works without
    extra code. This keeps the dedicated-port listener observable on its
    own — operators can confirm it's alive without going through the
    main app's health endpoints.
    """
    if logger is None:
        logger = logging.getLogger(__name__)
    bind_port = port if port is not None else _resolve_metrics_port()

    async def _health(_request: Request) -> Response:
        return Response(content=b"ok", media_type="text/plain")

    routes = [
        Route(path, handler, methods=["GET"]),
        Route("/health", _health, methods=["GET"]),
    ]
    if extra_routes:
        routes.extend(extra_routes)
    app = Starlette(routes=routes)

    # Import uvicorn lazily so containers that don't enable metrics never
    # pay the import cost.
    import uvicorn

    config = uvicorn.Config(
        app,
        host="0.0.0.0",  # nosec B104 — intentional; in-cluster listener
        port=bind_port,
        log_level="warning",
        access_log=False,
    )
    server = uvicorn.Server(config)
    task = asyncio.get_running_loop().create_task(server.serve(), name="metrics-server")
    logger.info("Prometheus metrics enabled on dedicated listener :%d%s", bind_port, path)
    return task

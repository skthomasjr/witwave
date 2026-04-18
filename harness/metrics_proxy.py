"""Fetch metrics from backend agents.

Each backend emits agent, agent_id, and backend labels on every metric sample,
so no relabeling is needed at the proxy layer — raw text is concatenated as-is.
"""

import asyncio
import logging
import os
from urllib.parse import urlparse, urlunparse

import httpx

from backends.config import BackendConfig
from metrics import agent_metrics_backend_fetch_errors_total

logger = logging.getLogger(__name__)


def _metrics_url(backend_url: str) -> str:
    """Rewrite a backend's app-port URL to point at its metrics listener (#643).

    Backends now serve /metrics on a dedicated port (METRICS_PORT, default
    9000) instead of the app port. `backend.url` in backend.yaml still
    carries the APP URL (so routing / A2A continue to work), so we swap
    the port here at fetch time. Preserves scheme, host, and path — only
    the port changes.
    """
    metrics_port = int(os.environ.get("METRICS_PORT", "9000"))
    parsed = urlparse(backend_url.rstrip('/'))
    hostname = parsed.hostname or parsed.netloc.split(':', 1)[0]
    if parsed.username is not None or parsed.password is not None:
        userinfo = parsed.username or ''
        if parsed.password is not None:
            userinfo += f":{parsed.password}"
        new_netloc = f"{userinfo}@{hostname}:{metrics_port}"
    else:
        new_netloc = f"{hostname}:{metrics_port}"
    return urlunparse((parsed.scheme, new_netloc, '/metrics', '', '', ''))


async def fetch_backend_metrics(backends: list[BackendConfig]) -> str:
    """Fetch /metrics from each backend concurrently and return concatenated Prometheus text.

    Backends that are unreachable or return non-200 are skipped and counted in
    agent_metrics_backend_fetch_errors_total so that silent omissions are visible
    in Prometheus dashboards (#372).

    Backends are fetched concurrently via asyncio.gather so that one slow or
    unreachable backend does not delay the response for all others (#370).
    """
    reachable = [b for b in backends if b.url]

    async def _fetch_one(client: httpx.AsyncClient, backend: BackendConfig) -> str:
        metrics_url = _metrics_url(backend.url)
        try:
            resp = await client.get(metrics_url)
            if resp.status_code == 200:
                return resp.text
            else:
                logger.warning(
                    f"Backend {backend.id!r} /metrics returned {resp.status_code} — skipping"
                )
                if agent_metrics_backend_fetch_errors_total is not None:
                    agent_metrics_backend_fetch_errors_total.labels(backend=backend.id).inc()
        except Exception as exc:
            logger.warning(
                f"Backend {backend.id!r} /metrics unreachable: {exc!r} — skipping"
            )
            if agent_metrics_backend_fetch_errors_total is not None:
                agent_metrics_backend_fetch_errors_total.labels(backend=backend.id).inc()
        return ''

    async with httpx.AsyncClient(timeout=5.0) as client:
        results = await asyncio.gather(
            *[_fetch_one(client, b) for b in reachable],
            return_exceptions=True,
        )

    parts = []
    for backend, result in zip(reachable, results):
        if isinstance(result, BaseException):
            logger.warning(
                f"Backend {backend.id!r} /metrics gather error: {result!r} — skipping"
            )
            if agent_metrics_backend_fetch_errors_total is not None:
                agent_metrics_backend_fetch_errors_total.labels(backend=backend.id).inc()
        elif result:
            parts.append(result)

    return ''.join(parts)

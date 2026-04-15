"""Fetch metrics from backend agents.

Each backend emits agent, agent_id, and backend labels on every metric sample,
so no relabeling is needed at the proxy layer — raw text is concatenated as-is.
"""

import asyncio
import logging

import httpx

from backends.config import BackendConfig
from metrics import agent_metrics_backend_fetch_errors_total

logger = logging.getLogger(__name__)


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
        metrics_url = backend.url.rstrip('/') + '/metrics'
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

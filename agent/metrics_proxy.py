"""Fetch metrics from backend agents.

Each backend emits agent, agent_id, and backend labels on every metric sample,
so no relabeling is needed at the proxy layer — raw text is concatenated as-is.
"""

import logging

import httpx

from backends.config import BackendConfig

logger = logging.getLogger(__name__)


async def fetch_backend_metrics(backends: list[BackendConfig]) -> str:
    """Fetch /metrics from each backend and return concatenated Prometheus text.

    Backends that are unreachable or return non-200 are silently skipped.
    """
    parts = []
    async with httpx.AsyncClient(timeout=5.0) as client:
        for backend in backends:
            if not backend.url:
                continue
            metrics_url = backend.url.rstrip('/') + '/metrics'
            try:
                resp = await client.get(metrics_url)
                if resp.status_code == 200:
                    parts.append(resp.text)
                else:
                    logger.debug(f"Backend {backend.id!r} /metrics returned {resp.status_code} — skipping")
            except Exception as exc:
                logger.debug(f"Backend {backend.id!r} /metrics unreachable: {exc!r} — skipping")
    return ''.join(parts)

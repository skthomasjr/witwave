"""Fetch and relabel metrics from backend agents.

Each backend's /metrics output is rewritten to inject a backend="<id>" label
on every metric sample line, disambiguating a2_* metrics across backends.
"""

import logging
import re

import httpx

from backends.config import BackendConfig

logger = logging.getLogger(__name__)

# Matches a Prometheus metric sample line — name, optional existing labels, value.
# Examples:
#   a2_tasks_total{status="success"} 3.0
#   a2_tasks_total 3.0
_SAMPLE_RE = re.compile(
    r'^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})?\s+(.+)$'
)


def _inject_backend_label(line: str, backend_id: str) -> str:
    """Inject backend="<id>" into a Prometheus sample line."""
    m = _SAMPLE_RE.match(line)
    if not m:
        return line
    name, labels, rest = m.group(1), m.group(2), m.group(3)
    escaped = backend_id.replace('\\', '\\\\').replace('"', '\\"')
    new_label = f'backend="{escaped}"'
    if labels:
        # Insert before the closing brace
        new_labels = labels[:-1] + ',' + new_label + '}'
    else:
        new_labels = '{' + new_label + '}'
    return f'{name}{new_labels} {rest}'


def _relabel(text: str, backend_id: str) -> str:
    """Rewrite all sample lines in a Prometheus text exposition to add backend label."""
    out = []
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith('#') or stripped == '':
            out.append(line)
        else:
            out.append(_inject_backend_label(stripped, backend_id))
    return '\n'.join(out) + '\n'


async def fetch_backend_metrics(backends: list[BackendConfig]) -> str:
    """Fetch /metrics from each backend and return relabelled Prometheus text.

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
                    parts.append(_relabel(resp.text, backend.id))
                else:
                    logger.debug(f"Backend {backend.id!r} /metrics returned {resp.status_code} — skipping")
            except Exception as exc:
                logger.debug(f"Backend {backend.id!r} /metrics unreachable: {exc!r} — skipping")
    return ''.join(parts)

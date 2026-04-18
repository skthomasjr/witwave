"""Prometheus metrics for MCP tool servers (#851).

Every MCP tool handler should open an ``mcp.handler`` span and record a
corresponding counter/histogram pair so operators can see call rate,
p95 latency, and error rate per tool without scraping traces. This
module defines the shared label schema — ``server`` (the FastMCP name
like ``kubernetes`` / ``helm``), ``tool`` (the handler name like
``apply`` / ``install``), and ``outcome`` (``ok`` or ``error``) — and
returns a context manager that both tool servers reuse so metric names
and labels stay in lockstep.

The label schema intentionally matches the claude backend's
``backend_mcp_tool_calls_total`` family so PromQL joins / recording
rules covering backend-side and server-side views of the same call
can line up on ``tool`` and ``server`` without label rewrites.

When prometheus_client is unavailable (bare dev checkout), the
helpers degrade to no-ops so imports remain safe.
"""

from __future__ import annotations

import time
from contextlib import contextmanager
from typing import Iterator

try:
    from prometheus_client import Counter, Histogram

    _PROM_AVAILABLE = True
except Exception:  # pragma: no cover - defensive fallback
    _PROM_AVAILABLE = False

# Latency buckets chosen to cover quick Kubernetes GETs (<50ms) through
# slow Helm install/upgrade operations (>10s) with enough resolution in
# the sub-second range to compute useful p95/p99 numbers.
_LATENCY_BUCKETS = (
    0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0,
)

if _PROM_AVAILABLE:
    MCP_TOOL_CALLS_TOTAL = Counter(
        "mcp_tool_calls_total",
        "MCP tool invocations (successful + failed).",
        ["server", "tool", "outcome"],
    )
    MCP_TOOL_DURATION_SECONDS = Histogram(
        "mcp_tool_duration_seconds",
        "Wall-clock duration of an MCP tool handler, from span open to return.",
        ["server", "tool", "outcome"],
        buckets=_LATENCY_BUCKETS,
    )
else:  # pragma: no cover - import-time fallback
    MCP_TOOL_CALLS_TOTAL = None  # type: ignore[assignment]
    MCP_TOOL_DURATION_SECONDS = None  # type: ignore[assignment]


@contextmanager
def record_tool_call(server: str, tool: str) -> Iterator[None]:
    """Record a tool invocation's duration + outcome.

    Yields control to the caller; on exit, observes latency and
    increments the call counter with outcome=``ok`` or ``error`` based
    on whether an exception escaped the ``with`` block. Re-raises the
    original exception so the caller's own error path still runs.
    """
    start = time.monotonic()
    outcome = "ok"
    try:
        yield
    except BaseException:
        outcome = "error"
        raise
    finally:
        if _PROM_AVAILABLE:
            elapsed = time.monotonic() - start
            try:
                MCP_TOOL_CALLS_TOTAL.labels(server=server, tool=tool, outcome=outcome).inc()
                MCP_TOOL_DURATION_SECONDS.labels(
                    server=server, tool=tool, outcome=outcome
                ).observe(elapsed)
            except Exception:
                # Never let a metrics failure mask the original outcome.
                pass

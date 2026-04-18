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

import logging
import os
import threading
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
    # Rate-limit / concurrency-cap counter (#850). Incremented whenever a
    # tool call is rejected because the per-tool concurrency semaphore is
    # saturated or would block past the acquisition timeout. Operators
    # alert on rate() of this series to know the cap is biting in
    # production.
    MCP_TOOL_RATE_LIMITED_TOTAL = Counter(
        "mcp_tool_rate_limited_total",
        "MCP tool invocations rejected by the per-tool concurrency cap.",
        ["server", "tool", "reason"],
    )
else:  # pragma: no cover - import-time fallback
    MCP_TOOL_CALLS_TOTAL = None  # type: ignore[assignment]
    MCP_TOOL_DURATION_SECONDS = None  # type: ignore[assignment]
    MCP_TOOL_RATE_LIMITED_TOTAL = None  # type: ignore[assignment]


class ConcurrencyCapExceeded(RuntimeError):
    """Raised by record_tool_call when the per-tool concurrency cap is saturated.

    MCP servers can translate this into the transport-appropriate error
    (FastMCP converts exceptions into JSON-RPC error responses
    automatically), so an overloaded tool surfaces back to the agent
    rather than silently queueing requests that then time out mid-stream.
    """


_CAP_LOCK = threading.Lock()
# Values: threading.BoundedSemaphore OR the string "nocap" (sentinel for
# "env says no cap"). Absent key = not yet resolved.
_CAP_SEMAPHORES: dict[tuple[str, str], object] = {}
_logger = logging.getLogger(__name__)


def _cap_for(server: str, tool: str) -> threading.BoundedSemaphore | None:
    """Return a process-wide BoundedSemaphore for (server, tool), or None.

    Cap is read from env at first use per (server, tool) and cached:
      1. MCP_CONCURRENCY_<SERVER>_<TOOL> (per-tool override, upper-cased)
      2. MCP_CONCURRENCY_<SERVER>       (per-server default)
      3. MCP_CONCURRENCY                 (global default)
    Missing / <=0 / unparseable disables the cap for that (server, tool).
    """
    key = (server, tool)
    cached = _CAP_SEMAPHORES.get(key)
    if cached is not None:
        return None if cached == "nocap" else cached  # type: ignore[return-value]
    with _CAP_LOCK:
        cached = _CAP_SEMAPHORES.get(key)
        if cached is not None:
            return None if cached == "nocap" else cached  # type: ignore[return-value]
        # Normalise env-var names: '.' '-' go to '_', upper-case.
        def _norm(s: str) -> str:
            return s.replace(".", "_").replace("-", "_").upper()
        candidates = (
            f"MCP_CONCURRENCY_{_norm(server)}_{_norm(tool)}",
            f"MCP_CONCURRENCY_{_norm(server)}",
            "MCP_CONCURRENCY",
        )
        cap_value = 0
        for name in candidates:
            raw = os.environ.get(name, "").strip()
            if not raw:
                continue
            try:
                cap_value = int(raw)
            except ValueError:
                _logger.warning(
                    "mcp_metrics: ignoring non-integer %s=%r", name, raw,
                )
                cap_value = 0
            break
        if cap_value <= 0:
            # Sentinel: cache a no-cap marker so repeat lookups stay cheap.
            _CAP_SEMAPHORES[key] = "nocap"
            return None
        sem = threading.BoundedSemaphore(cap_value)
        _CAP_SEMAPHORES[key] = sem
        return sem


def _cap_acquire_timeout() -> float:
    """Seconds to wait for a cap slot before rejecting with ConcurrencyCapExceeded."""
    raw = os.environ.get("MCP_CONCURRENCY_ACQUIRE_TIMEOUT_SECONDS", "").strip()
    if not raw:
        return 0.0  # non-blocking by default — fail fast, backend can retry
    try:
        return max(0.0, float(raw))
    except ValueError:
        return 0.0


@contextmanager
def record_tool_call(server: str, tool: str) -> Iterator[None]:
    """Record a tool invocation's duration + outcome, enforcing the concurrency cap (#850, #851).

    1. Tries to acquire the per-tool BoundedSemaphore. If saturated and
       no timeout is configured, immediately raises
       ``ConcurrencyCapExceeded`` and increments
       ``mcp_tool_rate_limited_total`` — the backend gets a fast
       failure it can surface to the agent.
    2. Yields control to the caller.
    3. On exit, observes latency and increments
       ``mcp_tool_calls_total`` with outcome=``ok`` or ``error``.
    Re-raises the original exception so the caller's own error path
    still runs; always releases the semaphore in ``finally``.
    """
    sem = _cap_for(server, tool)
    acquired = False
    if sem is not None:
        timeout = _cap_acquire_timeout()
        if timeout > 0:
            acquired = sem.acquire(timeout=timeout)
        else:
            acquired = sem.acquire(blocking=False)
        if not acquired:
            if _PROM_AVAILABLE:
                try:
                    MCP_TOOL_RATE_LIMITED_TOTAL.labels(
                        server=server, tool=tool, reason="concurrency_cap"
                    ).inc()
                except Exception:
                    pass
            raise ConcurrencyCapExceeded(
                f"mcp {server}.{tool}: per-tool concurrency cap exceeded "
                f"(set MCP_CONCURRENCY_* env vars to tune)"
            )
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
        if acquired and sem is not None:
            try:
                sem.release()
            except ValueError:
                # BoundedSemaphore over-release — indicates a programming
                # bug elsewhere; log but do not mask the original outcome.
                _logger.exception("mcp_metrics: over-release on %s.%s", server, tool)

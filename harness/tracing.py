"""W3C trace-context helpers (#468).

Smallest-viable distributed tracing: parse and emit `traceparent` headers
following the W3C Trace Context spec (https://www.w3.org/TR/trace-context/),
without bringing in any external dependency. This makes cross-agent
correlation possible in the existing logs and metrics, and lays the
groundwork for a future full-OpenTelemetry integration (#469) that can
consume the same parent context.

Wire format (traceparent):

    {version}-{trace_id}-{parent_id}-{trace_flags}

    version       : 2 hex chars, currently always "00"
    trace_id      : 32 hex chars (128-bit)
    parent_id     : 16 hex chars (64-bit) — the span_id of the immediate
                    parent in the calling service
    trace_flags   : 2 hex chars (sampling, etc.) — we always emit "01"
                    (sampled) since we want every request traceable

Example: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01

This module intentionally does not depend on opentelemetry packages so
the harness keeps its existing dependency footprint until #469 is approved.

The module is deliberately named ``tracing`` (not ``trace``) to avoid
shadowing the Python stdlib ``trace`` module on the harness sys.path.
"""
from __future__ import annotations

import logging
import os
import re
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any, Iterator

logger = logging.getLogger(__name__)

# Regex matching the W3C version-00 traceparent. Strict per spec — anything
# else is treated as absent and a fresh context is minted.
_TRACEPARENT_RE = re.compile(
    r"^(?P<version>[0-9a-f]{2})-(?P<trace_id>[0-9a-f]{32})-(?P<parent_id>[0-9a-f]{16})-(?P<flags>[0-9a-f]{2})$"
)

# Trace IDs and span IDs are random hex strings of the appropriate width.
# The all-zero trace_id and the all-zero span_id are both invalid per spec,
# so we generate from os.urandom and reject if (extremely unlikely) we get
# a zero value.
_TRACE_ID_BYTES = 16
_SPAN_ID_BYTES = 8


@dataclass(frozen=True)
class TraceContext:
    """A W3C trace-context tuple.

    `parent_id` is the span_id of the calling service's span — i.e. the
    parent of any span this service emits. When this service goes on to
    call a downstream service, it picks a fresh span_id for itself and
    sends `traceparent` with that fresh span_id as the new parent_id.
    """

    trace_id: str  # 32 hex chars
    parent_id: str  # 16 hex chars (the inbound parent's span_id)
    flags: str = "01"  # sampled
    version: str = "00"

    def to_header(self) -> str:
        """Format as a W3C traceparent header value."""
        return f"{self.version}-{self.trace_id}-{self.parent_id}-{self.flags}"

    def child(self) -> "TraceContext":
        """Return a new context for an outbound call: same trace_id, fresh parent_id (span)."""
        return TraceContext(
            trace_id=self.trace_id,
            parent_id=_random_hex(_SPAN_ID_BYTES),
            flags=self.flags,
            version=self.version,
        )


def _random_hex(num_bytes: int) -> str:
    """Return ``num_bytes * 2`` hex chars from os.urandom, rejecting all-zero."""
    while True:
        b = os.urandom(num_bytes)
        if any(b):
            return b.hex()


def parse_traceparent(header_value: str | None) -> TraceContext | None:
    """Parse a W3C ``traceparent`` header value.

    Returns None if the header is missing or malformed (the caller should
    mint a fresh context in that case via :func:`new_context`).

    Per spec (https://www.w3.org/TR/trace-context/#trace-id):
      - trace_id of all zeros is invalid
      - parent_id of all zeros is invalid
    """
    if not header_value:
        return None
    m = _TRACEPARENT_RE.match(header_value.strip())
    if not m:
        return None
    trace_id = m.group("trace_id")
    parent_id = m.group("parent_id")
    if trace_id == "0" * 32 or parent_id == "0" * 16:
        return None
    return TraceContext(
        trace_id=trace_id,
        parent_id=parent_id,
        flags=m.group("flags"),
        version=m.group("version"),
    )


def new_context() -> TraceContext:
    """Mint a fresh trace context (called when no inbound traceparent is present)."""
    return TraceContext(
        trace_id=_random_hex(_TRACE_ID_BYTES),
        parent_id=_random_hex(_SPAN_ID_BYTES),
    )


def context_from_inbound(header_value: str | None) -> tuple[TraceContext, bool]:
    """Resolve the active trace context for an inbound request.

    Returns ``(context, had_inbound)`` where ``had_inbound`` is True when
    the caller supplied a valid traceparent. The caller can use this for
    metric labelling.
    """
    parsed = parse_traceparent(header_value)
    if parsed is None:
        return new_context(), False
    return parsed, True


# ---------------------------------------------------------------------------
# OpenTelemetry layer (#469) — re-exported from ``shared/otel.py`` so the
# harness, a2-claude, a2-codex, and a2-gemini all share the same bootstrap
# and propagator wiring. Existing call sites in the harness continue to
# import from ``tracing`` without change.
# ---------------------------------------------------------------------------

from otel import (  # noqa: E402, F401 — re-export for backward compat
    SPAN_KIND_CLIENT,
    SPAN_KIND_INTERNAL,
    SPAN_KIND_SERVER,
    extract_otel_context,
    init_otel_if_enabled,
    inject_traceparent,
    otel_enabled,
    set_span_error,
    start_span,
)

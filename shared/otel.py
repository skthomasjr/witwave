"""Shared OpenTelemetry bootstrap + helper layer (#469).

Used by harness and all three backends (claude, codex, gemini).
The module is entirely optional: when ``OTEL_ENABLED`` is falsy,
:func:`init_otel_if_enabled` does nothing and every helper below routes to
the OTel API's built-in no-op tracer. Call sites can therefore use
:func:`start_span` unconditionally without paying a runtime cost or pulling
in exporter-side imports when tracing is disabled.

When enabled, spans are emitted via OTLP/HTTP to the endpoint given by
``OTEL_EXPORTER_OTLP_ENDPOINT`` (standard OTel env var). Resource
attributes come from ``OTEL_SERVICE_NAME`` plus a small set of per-agent
labels (``agent``, ``agent_id``, ``backend``) stamped at init-time from
the container's own environment. Sampling is controlled by the standard
OTel env vars (``OTEL_TRACES_SAMPLER`` etc.) so operators can dial it
without touching code.

The OTel propagator uses W3C trace-context under the hood, so spans
emitted here will correlate with trace_ids produced by the bare
``harness/tracing.py`` helpers — the two layers share the wire format by
construction.
"""
from __future__ import annotations

import logging
import os
from contextlib import contextmanager
from typing import Any, Iterator

logger = logging.getLogger(__name__)

_otel_enabled: bool = False
_otel_tracer: Any = None  # opentelemetry.trace.Tracer | None, lazily populated

SPAN_KIND_SERVER = "server"
SPAN_KIND_CLIENT = "client"
SPAN_KIND_INTERNAL = "internal"


def init_otel_if_enabled(
    service_name: str | None = None,
    resource_attributes: dict[str, str] | None = None,
) -> bool:
    """Initialise the OTel TracerProvider + OTLP exporter when enabled.

    Reads ``OTEL_ENABLED`` (default ``false``). When truthy, configures a
    single ``BatchSpanProcessor`` pointing at ``OTEL_EXPORTER_OTLP_ENDPOINT``
    and registers it as the process-wide TracerProvider. Safe to call more
    than once — subsequent calls are a no-op. Returns True when OTel is now
    active in this process, False when it stayed disabled or failed.

    *service_name* defaults to the ``OTEL_SERVICE_NAME`` env var, or a
    generic fallback. *resource_attributes* are merged on top of the
    baseline ``{agent, agent_id, backend}`` attributes sourced from env.
    """
    global _otel_enabled, _otel_tracer
    if _otel_enabled:
        return True
    if os.environ.get("OTEL_ENABLED", "false").lower() in ("0", "false", "no", "off", ""):
        return False
    try:
        from opentelemetry import trace as _otel_trace
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
    except ImportError as exc:
        logger.warning("OTel enabled but opentelemetry packages missing: %s — staying disabled.", exc)
        return False

    _service = service_name or os.environ.get("OTEL_SERVICE_NAME") or "nyx"
    attrs: dict[str, str] = {
        "service.name": _service,
        "agent": os.environ.get("AGENT_OWNER") or os.environ.get("AGENT_NAME") or "",
        "agent_id": os.environ.get("AGENT_ID", ""),
        "backend": os.environ.get("BACKEND_ID", ""),
    }
    if resource_attributes:
        attrs.update({k: str(v) for k, v in resource_attributes.items()})
    attrs = {k: v for k, v in attrs.items() if v}

    try:
        resource = Resource.create(attrs)
        provider = TracerProvider(resource=resource)
        exporter = OTLPSpanExporter()  # reads OTEL_EXPORTER_OTLP_ENDPOINT and friends
        provider.add_span_processor(BatchSpanProcessor(exporter))
        _otel_trace.set_tracer_provider(provider)
        _otel_tracer = _otel_trace.get_tracer(_service)
    except Exception as exc:
        logger.warning("OTel init failed — staying disabled: %s", exc)
        return False

    _otel_enabled = True
    logger.info("OTel tracing initialised: service=%s attrs=%s", _service, attrs)
    return True


def otel_enabled() -> bool:
    """Return True iff OTel is active in this process."""
    return _otel_enabled


def _get_tracer() -> Any:
    """Return the active tracer, or the OTel-API default no-op tracer."""
    global _otel_tracer
    if _otel_tracer is not None:
        return _otel_tracer
    try:
        from opentelemetry import trace as _otel_trace

        return _otel_trace.get_tracer("nyx")
    except ImportError:
        return None


def extract_otel_context(carrier: dict[str, str] | None) -> Any:
    """Extract an OTel Context from a carrier dict (headers or metadata)."""
    if not carrier:
        return None
    try:
        from opentelemetry.propagate import extract

        return extract(carrier)
    except Exception:
        return None


def inject_traceparent(carrier: dict[str, str], context: Any = None) -> None:
    """Inject the current OTel span's traceparent into *carrier*.

    When OTel is disabled (no active span), this is a no-op.
    """
    try:
        from opentelemetry.propagate import inject

        inject(carrier, context=context)
    except Exception:
        pass


@contextmanager
def start_span(
    name: str,
    kind: str = SPAN_KIND_INTERNAL,
    parent_context: Any = None,
    attributes: dict[str, Any] | None = None,
) -> Iterator[Any]:
    """Start an OTel span (or a no-op stand-in when OTel is disabled).

    Use as a context manager::

        with start_span("a2a.execute", kind="server") as span:
            ...
    """
    tracer = _get_tracer()
    if tracer is None:
        yield None
        return

    try:
        from opentelemetry import trace as _otel_trace

        span_kind_map = {
            SPAN_KIND_SERVER: _otel_trace.SpanKind.SERVER,
            SPAN_KIND_CLIENT: _otel_trace.SpanKind.CLIENT,
            SPAN_KIND_INTERNAL: _otel_trace.SpanKind.INTERNAL,
        }
        otel_kind = span_kind_map.get(kind, _otel_trace.SpanKind.INTERNAL)
    except ImportError:
        yield None
        return

    with tracer.start_as_current_span(
        name,
        kind=otel_kind,
        context=parent_context,
        attributes=attributes or {},
    ) as span:
        yield span


def set_span_error(span: Any, exc: BaseException) -> None:
    """Record *exc* on *span* and mark it as errored. No-op when span is None."""
    if span is None:
        return
    try:
        from opentelemetry import trace as _otel_trace

        span.record_exception(exc)
        span.set_status(_otel_trace.Status(_otel_trace.StatusCode.ERROR, str(exc)))
    except Exception:
        pass

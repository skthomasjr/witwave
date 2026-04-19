"""Prometheus MCP tool server (#853).

Wraps a Prometheus HTTP API endpoint so agents can run PromQL queries
against a deployed Prometheus instance. Targets the cluster where this
container is deployed — the operator points ``PROMETHEUS_URL`` at the
in-cluster Prometheus Service (e.g. ``http://prom-kube-prometheus-stack-prometheus.monitoring:9090``)
and agents consume the tool over MCP. Grafana / Loki / OTel surfaces are
deferred to follow-up tools.

Exposes five query tools that map 1:1 onto the Prometheus HTTP API:

- ``query`` → ``GET /api/v1/query``
- ``query_range`` → ``GET /api/v1/query_range``
- ``series`` → ``GET /api/v1/series``
- ``labels`` → ``GET /api/v1/labels``
- ``label_values`` → ``GET /api/v1/label/<name>/values``

Distributed tracing (#637): each tool handler opens an ``mcp.handler``
SERVER span. Each Prometheus HTTP call is wrapped in a ``prom.api.call``
child span with ``prom.endpoint`` / ``prom.query`` attributes. OTel is
a no-op when ``OTEL_ENABLED`` is unset.

Response/time bounds (#778 parity): every API call honours
``MCP_SUBPROCESS_TIMEOUT_SEC`` (default 30s — Prometheus query budget is
already short) and every response is byte-capped by
``MCP_RESPONSE_MAX_BYTES`` before returning.
"""

from __future__ import annotations

import contextlib
import json
import logging
import os
import sys
from typing import Any

import httpx
from mcp.server.fastmcp import FastMCP

# shared/otel.py is copied into the image (see Dockerfile) and imported
# as a top-level module. Falls back to no-op shims if the shared module
# isn't on sys.path (e.g. running tests outside the container).
sys.path.insert(0, "/home/tool/shared")
try:
    from otel import (  # type: ignore
        init_otel_if_enabled,
        start_span,
        set_span_error,
        SPAN_KIND_SERVER,
        SPAN_KIND_INTERNAL,
    )
except Exception:  # pragma: no cover - defensive fallback
    SPAN_KIND_SERVER = "server"
    SPAN_KIND_INTERNAL = "internal"

    def init_otel_if_enabled(*_a: Any, **_kw: Any) -> bool:  # type: ignore
        return False

    from contextlib import contextmanager

    @contextmanager  # type: ignore
    def start_span(*_a: Any, **_kw: Any):
        yield None

    def set_span_error(*_a: Any, **_kw: Any) -> None:  # type: ignore
        return None

try:
    from mcp_metrics import record_tool_call  # type: ignore
except Exception:  # pragma: no cover - defensive fallback
    from contextlib import contextmanager as _cm

    @_cm  # type: ignore
    def record_tool_call(*_a: Any, **_kw: Any):
        yield None

log = logging.getLogger("tools.prometheus")

mcp = FastMCP("prometheus")


# Base URL of the target Prometheus instance. The operator sets this via
# the chart values (env on the MCP tool pod). Stripped of any trailing
# slash so our endpoint joiner can safely concatenate "/api/v1/...".
_PROMETHEUS_URL = (os.environ.get("PROMETHEUS_URL") or "").rstrip("/")

# Optional bearer token for the Prometheus endpoint itself (distinct
# from the MCP_TOOL_AUTH_TOKEN that gates callers coming *into* this
# MCP server). Useful when Prometheus is fronted by a gateway that
# requires auth.
_PROMETHEUS_BEARER_TOKEN = os.environ.get("PROMETHEUS_BEARER_TOKEN") or ""

# Request timeout on Prometheus HTTP calls (#778 parity). Prometheus's
# own query budget is already short; a slow query usually means the
# server is genuinely struggling or the operator supplied a pathological
# expression. Keep the default tight and let operators raise it.
_MCP_REQUEST_TIMEOUT_SECONDS = float(
    os.environ.get("MCP_SUBPROCESS_TIMEOUT_SEC") or "30"
)

# Per-response byte cap on tool output (#778 parity). Prometheus query
# results can be arbitrarily large (a range query over a long window
# with high-cardinality labels returns MBs of JSON); cap so one bad
# query cannot OOM the pod.
_MCP_RESPONSE_MAX_BYTES = int(
    os.environ.get("MCP_RESPONSE_MAX_BYTES") or str(8 * 1024 * 1024)
)


class PrometheusError(RuntimeError):
    """Raised when a Prometheus HTTP API call fails."""


def _truncate_json(value: Any, *, tool: str) -> Any:
    """Cap a JSON-able payload to MCP_RESPONSE_MAX_BYTES (#778 parity)."""
    cap = _MCP_RESPONSE_MAX_BYTES
    if cap <= 0 or value is None:
        return value
    try:
        raw = json.dumps(value, default=str)
    except Exception:
        return value
    if len(raw.encode("utf-8", errors="replace")) <= cap:
        return value
    return {
        "_truncated": True,
        "_cap_bytes": cap,
        "_note": (
            f"mcp-prometheus:{tool} response exceeded "
            f"MCP_RESPONSE_MAX_BYTES ({cap}); raw payload suppressed. "
            "Narrow the time range, lower `step`, or add label filters."
        ),
    }


@contextlib.contextmanager
def _handler_span(tool: str, attributes: dict[str, Any] | None = None):
    """Open the outer ``mcp.handler`` SERVER span for a tool invocation."""
    attrs: dict[str, Any] = {"mcp.server": "prometheus", "mcp.tool": tool}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    with record_tool_call("prometheus", tool):
        with start_span("mcp.handler", kind=SPAN_KIND_SERVER, attributes=attrs) as span:
            yield span


def _ensure_configured() -> None:
    if not _PROMETHEUS_URL:
        raise PrometheusError(
            "mcp-prometheus: PROMETHEUS_URL is not set. Point this env var "
            "at the cluster Prometheus HTTP endpoint (e.g. "
            "http://prometheus-server.monitoring:9090) in the tool's pod "
            "spec and restart."
        )


def _prom_get(endpoint: str, params: dict[str, Any]) -> Any:
    """Issue ``GET {PROMETHEUS_URL}{endpoint}`` and return the parsed JSON.

    Wraps the call in a ``prom.api.call`` INTERNAL span. Raises
    :class:`PrometheusError` on a non-200 response or when the JSON
    envelope reports ``status != "success"``.
    """
    _ensure_configured()
    url = f"{_PROMETHEUS_URL}{endpoint}"
    headers: dict[str, str] = {}
    if _PROMETHEUS_BEARER_TOKEN:
        headers["Authorization"] = f"Bearer {_PROMETHEUS_BEARER_TOKEN}"
    # Strip None-valued params so we don't send "&foo=" on the wire.
    clean_params: dict[str, Any] = {
        k: v for k, v in params.items() if v is not None and v != ""
    }
    with start_span(
        "prom.api.call",
        kind=SPAN_KIND_INTERNAL,
        attributes={"prom.endpoint": endpoint},
    ) as _exec_span:
        try:
            with httpx.Client(timeout=_MCP_REQUEST_TIMEOUT_SECONDS) as client:
                resp = client.get(url, params=clean_params, headers=headers)
        except httpx.TimeoutException as exc:
            err = PrometheusError(
                f"prometheus {endpoint} timed out after "
                f"{_MCP_REQUEST_TIMEOUT_SECONDS}s (MCP_SUBPROCESS_TIMEOUT_SEC)"
            )
            set_span_error(_exec_span, err)
            raise err from exc
        except httpx.HTTPError as exc:
            err = PrometheusError(f"prometheus {endpoint} transport error: {exc}")
            set_span_error(_exec_span, err)
            raise err from exc

        if resp.status_code != 200:
            err = PrometheusError(
                f"prometheus {endpoint} HTTP {resp.status_code}: "
                f"{resp.text[:512]}"
            )
            set_span_error(_exec_span, err)
            raise err
        try:
            body = resp.json()
        except ValueError as exc:
            err = PrometheusError(
                f"prometheus {endpoint} returned non-JSON body: {resp.text[:512]}"
            )
            set_span_error(_exec_span, err)
            raise err from exc
        if body.get("status") != "success":
            err = PrometheusError(
                f"prometheus {endpoint} error: "
                f"{body.get('errorType')}: {body.get('error')}"
            )
            set_span_error(_exec_span, err)
            raise err
        return body


def _require_str(label: str, value: Any) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"prometheus: {label!r} must be a non-empty string")
    return value


@mcp.tool()
def query(expr: str, time: str | None = None) -> dict:
    """Evaluate an instant PromQL query.

    Maps to ``GET /api/v1/query``. ``time`` is an optional RFC3339 or
    unix-seconds timestamp; omit to query against the latest scrape.

    Returns the parsed Prometheus response envelope — typically
    ``{"status": "success", "data": {"resultType": ..., "result": [...]}}``.
    """
    _require_str("expr", expr)
    with _handler_span("query", {"prom.query": expr}) as _h:
        try:
            body = _prom_get("/api/v1/query", {"query": expr, "time": time})
            return _truncate_json(body, tool="query")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def query_range(
    expr: str,
    start: str,
    end: str,
    step: str,
) -> dict:
    """Evaluate a PromQL expression over a time range.

    Maps to ``GET /api/v1/query_range``. ``start`` and ``end`` are
    RFC3339 or unix-seconds timestamps; ``step`` is a Prometheus
    duration string (``"15s"``, ``"1m"``, …) or raw seconds.

    Returns the parsed response envelope with matrix result data.
    """
    _require_str("expr", expr)
    _require_str("start", start)
    _require_str("end", end)
    _require_str("step", step)
    with _handler_span(
        "query_range",
        {"prom.query": expr, "prom.start": start, "prom.end": end, "prom.step": step},
    ) as _h:
        try:
            body = _prom_get(
                "/api/v1/query_range",
                {"query": expr, "start": start, "end": end, "step": step},
            )
            return _truncate_json(body, tool="query_range")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def series(
    match: list[str],
    start: str | None = None,
    end: str | None = None,
) -> dict:
    """Return the list of time series matching one or more selectors.

    Maps to ``GET /api/v1/series``. ``match`` is a list of series
    selectors (e.g. ``['up', '{job="prometheus"}']``). ``start`` / ``end``
    are optional bounds for the lookback window.
    """
    if not isinstance(match, list) or not match:
        raise ValueError("prometheus: 'match' must be a non-empty list of selectors")
    for i, item in enumerate(match):
        if not isinstance(item, str) or not item:
            raise ValueError(
                f"prometheus: match[{i}] must be a non-empty string (got {item!r})"
            )
    with _handler_span("series", {"prom.match": ",".join(match)}) as _h:
        try:
            # Prometheus HTTP API expects multiple match[]=... params;
            # httpx accepts a list value to emit repeated query pairs.
            body = _prom_get(
                "/api/v1/series",
                {"match[]": match, "start": start, "end": end},
            )
            return _truncate_json(body, tool="series")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def labels(
    start: str | None = None,
    end: str | None = None,
    match: list[str] | None = None,
) -> dict:
    """Return the set of label names visible to Prometheus.

    Maps to ``GET /api/v1/labels``. ``match`` is an optional list of
    series selectors to narrow the lookup to a subset of series.
    """
    if match is not None:
        if not isinstance(match, list):
            raise ValueError("prometheus: 'match' must be a list of selectors")
        for i, item in enumerate(match):
            if not isinstance(item, str) or not item:
                raise ValueError(
                    f"prometheus: match[{i}] must be a non-empty string (got {item!r})"
                )
    with _handler_span("labels") as _h:
        try:
            params: dict[str, Any] = {"start": start, "end": end}
            if match:
                params["match[]"] = match
            body = _prom_get("/api/v1/labels", params)
            return _truncate_json(body, tool="labels")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def label_values(
    label: str,
    start: str | None = None,
    end: str | None = None,
    match: list[str] | None = None,
) -> dict:
    """Return the set of values observed for a given label.

    Maps to ``GET /api/v1/label/<name>/values``. The ``label`` name must
    be a Prometheus identifier ([a-zA-Z_][a-zA-Z0-9_]*); we validate up
    front so an LLM-supplied value cannot smuggle path segments into
    the endpoint.
    """
    _require_str("label", label)
    # Prometheus label-name grammar — conservative allow-list prevents
    # path-escape via encoded slashes / newlines.
    import re as _re
    if not _re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", label):
        raise ValueError(
            f"prometheus: 'label' must match [A-Za-z_][A-Za-z0-9_]* (got {label!r})"
        )
    if match is not None:
        if not isinstance(match, list):
            raise ValueError("prometheus: 'match' must be a list of selectors")
        for i, item in enumerate(match):
            if not isinstance(item, str) or not item:
                raise ValueError(
                    f"prometheus: match[{i}] must be a non-empty string (got {item!r})"
                )
    with _handler_span("label_values", {"prom.label": label}) as _h:
        try:
            params: dict[str, Any] = {"start": start, "end": end}
            if match:
                params["match[]"] = match
            body = _prom_get(f"/api/v1/label/{label}/values", params)
            return _truncate_json(body, tool="label_values")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


def _get_info_doc() -> dict[str, Any]:
    """Build the /info document for the prometheus tool server (#1122 parity)."""
    image_version = (
        os.environ.get("IMAGE_VERSION")
        or os.environ.get("IMAGE_TAG")
        or os.environ.get("VERSION")
        or "unknown"
    )
    try:
        tool_names = sorted(mcp._tool_manager._tools.keys())  # type: ignore[attr-defined]
    except Exception:
        tool_names = []
    return {
        "server": "mcp-prometheus",
        "image_version": image_version,
        "prometheus_url_configured": bool(_PROMETHEUS_URL),
        "features": {
            "otel": bool(os.environ.get("OTEL_ENABLED")),
            "metrics": bool(os.environ.get("METRICS_ENABLED")),
            "bearer_token": bool(_PROMETHEUS_BEARER_TOKEN),
        },
        "tools": tool_names,
    }


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    # Initialise OTel up-front; no-op unless OTEL_ENABLED is truthy (#637).
    init_otel_if_enabled(
        service_name=os.environ.get("OTEL_SERVICE_NAME") or "mcp-prometheus",
    )

    # Dedicated Prometheus metrics listener on :METRICS_PORT (default 9000)
    # separate from the streamable-http MCP port (#643). Mirrors the pattern
    # in tools/kubernetes/server.py.
    if os.environ.get("METRICS_ENABLED"):
        try:
            import prometheus_client
            from starlette.requests import Request as _Request
            from starlette.responses import Response as _Response

            async def _metrics_handler(_request: _Request) -> _Response:
                body = prometheus_client.exposition.generate_latest()
                return _Response(
                    content=body,
                    media_type=prometheus_client.exposition.CONTENT_TYPE_LATEST,
                )

            from metrics_server import start_metrics_server_in_thread  # type: ignore

            start_metrics_server_in_thread(
                _metrics_handler,
                logger=logging.getLogger("mcp-prometheus.metrics"),
            )
        except Exception as _e:  # pragma: no cover - defensive
            logging.getLogger(__name__).warning(
                "metrics listener failed to start — continuing without it: %r", _e
            )

    if not _PROMETHEUS_URL:
        log.warning(
            "PROMETHEUS_URL is not set; tool calls will fail until it is "
            "configured. Set the env var on the mcp-prometheus pod."
        )

    # Streamable-HTTP transport (#644) wrapped with the shared bearer-token
    # middleware (#771). Falls back to mcp.run() when uvicorn/mcp_auth
    # cannot be imported (e.g. bare dev checkout).
    try:
        import uvicorn  # type: ignore
        from mcp_auth import require_bearer_token  # type: ignore
        _app = mcp.streamable_http_app()
        _app = require_bearer_token(_app, info_provider=_get_info_doc)
        uvicorn.run(
            _app,
            host="0.0.0.0",
            port=int(os.environ.get("MCP_PORT", "8000")),
            log_config=None,
        )
    except ImportError:
        mcp.run(
            transport="streamable-http",
            host="0.0.0.0",
            port=int(os.environ.get("MCP_PORT", "8000")),
        )

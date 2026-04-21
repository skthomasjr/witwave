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
from urllib.parse import urlparse

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

# Validate the scheme at module load time (#1213). An LLM-supplied or
# misconfigured URL with a file:// or gopher:// scheme would otherwise
# silently hand control to httpx's default transport and could be used
# to exfiltrate or probe unintended surfaces. Accept only http/https.
if _PROMETHEUS_URL:
    _parsed_prom_url = urlparse(_PROMETHEUS_URL)
    if _parsed_prom_url.scheme not in ("http", "https"):
        raise RuntimeError(
            f"mcp-prometheus: PROMETHEUS_URL must use http:// or https:// "
            f"(got scheme {_parsed_prom_url.scheme!r} from {_PROMETHEUS_URL!r}). "
            "See #1213."
        )

# #1528: warn when PROMETHEUS_URL points at a link-local or cloud-
# metadata IP. These are common misconfigurations that silently send
# bearer tokens to attacker-controlled or introspection endpoints.
# Optional hard allow-list via MCP_PROM_URL_ALLOWLIST (comma-separated
# hostnames, matching the mcp-helm pattern). Empty allow-list means
# "warn but accept"; populated means "refuse anything not on the list".
if _PROMETHEUS_URL:
    import ipaddress as _ipaddress
    import logging as _logging
    _prom_hostname = (_parsed_prom_url.hostname or "").lower().rstrip(".")
    _prom_allowlist_raw = os.environ.get("MCP_PROM_URL_ALLOWLIST", "")
    _prom_allowlist = {
        h.strip().lower() for h in _prom_allowlist_raw.split(",") if h.strip()
    }
    _prom_log = _logging.getLogger("tools.prometheus")
    if _prom_allowlist and _prom_hostname not in _prom_allowlist:
        raise RuntimeError(
            f"mcp-prometheus: PROMETHEUS_URL host {_prom_hostname!r} is "
            f"not in MCP_PROM_URL_ALLOWLIST ({sorted(_prom_allowlist)!r}). "
            "See #1528."
        )
    # Even without an allow-list, flag obviously-dangerous hosts so
    # operators notice in pod logs.
    if _prom_hostname:
        try:
            _prom_ip = _ipaddress.ip_address(_prom_hostname)
        except ValueError:
            _prom_ip = None
        if _prom_ip is not None and (
            _prom_ip.is_link_local
            or str(_prom_ip) in {"169.254.169.254", "fd00:ec2::254"}
        ):
            _prom_log.warning(
                "mcp-prometheus: PROMETHEUS_URL points at a link-local / "
                "cloud-metadata host (%s). Any bearer token will be sent "
                "there. Set MCP_PROM_URL_ALLOWLIST to constrain. (#1528)",
                _prom_hostname,
            )

# Hard byte cap for Prometheus response bodies (#1211). Streamed reads
# abort once the cap is exceeded so one pathological query cannot pin
# the pod's memory. Default 1MiB — Prometheus query results that exceed
# this either need narrower selectors or a shorter time range.
_MCP_PROM_MAX_RESPONSE_BYTES = int(
    os.environ.get("MCP_PROM_MAX_RESPONSE_BYTES") or str(1 * 1024 * 1024)
)

# Optional bearer token for the Prometheus endpoint itself (distinct
# from the MCP_TOOL_AUTH_TOKEN that gates callers coming *into* this
# MCP server). Useful when Prometheus is fronted by a gateway that
# requires auth.
_PROMETHEUS_BEARER_TOKEN = os.environ.get("PROMETHEUS_BEARER_TOKEN") or ""

# #1527: refuse to attach a bearer token on a plain-http URL — the token
# would cross the wire in cleartext and could be sniffed on any shared
# network hop between this pod and Prometheus. Operators running a
# genuinely trusted in-cluster link (mesh with mTLS, loopback-only
# topology) can opt in via PROMETHEUS_ALLOW_PLAINTEXT_BEARER=true; the
# startup log stays loud so the decision is auditable.
_PROMETHEUS_ALLOW_PLAINTEXT_BEARER = os.environ.get(
    "PROMETHEUS_ALLOW_PLAINTEXT_BEARER", ""
).strip().lower() in {"1", "true", "yes", "on"}
if (
    _PROMETHEUS_BEARER_TOKEN
    and _PROMETHEUS_URL
    and urlparse(_PROMETHEUS_URL).scheme == "http"
):
    if not _PROMETHEUS_ALLOW_PLAINTEXT_BEARER:
        raise RuntimeError(
            "mcp-prometheus: PROMETHEUS_BEARER_TOKEN is set but "
            f"PROMETHEUS_URL is plaintext http:// ({_PROMETHEUS_URL!r}). "
            "Refusing to send the token in cleartext. Either switch "
            "PROMETHEUS_URL to https://, or set "
            "PROMETHEUS_ALLOW_PLAINTEXT_BEARER=true to acknowledge the "
            "in-cleartext posture. See #1527."
        )
    # Loud WARN when the escape hatch is engaged so the choice shows
    # up in pod logs and dashboards.
    log.warning(
        "mcp-prometheus: bearer token will be sent over plaintext http "
        "because PROMETHEUS_ALLOW_PLAINTEXT_BEARER=true. Token is "
        "exposed to any on-path observer. See #1527."
    )

# Request timeout on Prometheus HTTP calls (#778 parity). Prometheus's
# own query budget is already short; a slow query usually means the
# server is genuinely struggling or the operator supplied a pathological
# expression. Keep the default tight and let operators raise it.
_MCP_REQUEST_TIMEOUT_SECONDS = float(
    os.environ.get("MCP_SUBPROCESS_TIMEOUT_SEC") or "30"
)


# #1398: module-level shared httpx.Client so TLS handshakes + connection
# pool amortise across queries. Lazy init keeps import-time cheap and
# defers failure to first use.
# #1407: double-checked locking so concurrent first-touches from the
# FastMCP thread-pool can't each build + orphan a client.
import threading as _threading
_SHARED_HTTPX_CLIENT: "httpx.Client | None" = None
_SHARED_HTTPX_CLIENT_LOCK = _threading.Lock()


def _get_shared_httpx_client() -> "httpx.Client":
    global _SHARED_HTTPX_CLIENT
    if _SHARED_HTTPX_CLIENT is None:
        with _SHARED_HTTPX_CLIENT_LOCK:
            if _SHARED_HTTPX_CLIENT is None:
                _SHARED_HTTPX_CLIENT = httpx.Client(
                    timeout=_MCP_REQUEST_TIMEOUT_SECONDS,
                    limits=httpx.Limits(
                        max_connections=50, max_keepalive_connections=10
                    ),
                )
    return _SHARED_HTTPX_CLIENT

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
    # #1529: keep empty-string params and warn instead. Silently
    # dropping ``v == ""`` masked caller mistakes (a blank ``query``
    # or missing ``match[]`` used to return a seemingly-fine response
    # for the wrong question). An empty string is still sent as
    # ``&foo=`` so Prometheus returns a clear 400 that the caller can
    # act on; only None keys are stripped.
    clean_params: dict[str, Any] = {}
    for k, v in params.items():
        if v is None:
            continue
        if v == "":
            log.warning(
                "prom._prom_get: caller supplied empty-string param %r "
                "on %s — forwarding to prometheus so the misuse surfaces "
                "as a 400 instead of a silent success (#1529).",
                k, endpoint,
            )
        clean_params[k] = v
    with start_span(
        "prom.api.call",
        kind=SPAN_KIND_INTERNAL,
        attributes={"prom.endpoint": endpoint},
    ) as _exec_span:
        # Stream the response body with a hard byte cap (#1211). Buffer
        # incrementally via ``iter_bytes`` so a pathological query that
        # returns multi-GB of JSON aborts at the cap instead of pinning
        # the pod's memory. We still need the full (bounded) buffer to
        # parse JSON, but we guarantee the buffer cannot exceed the cap.
        cap = _MCP_PROM_MAX_RESPONSE_BYTES
        try:
            # #1398: use a module-level httpx.Client so TLS handshakes +
            # connection pool amortise across queries. Fresh-per-call
            # construction defeats keep-alive and burns CPU on concurrent
            # load. Lazy-init keeps import-time cheap.
            _client = _get_shared_httpx_client()
            with _client.stream(
                "GET", url, params=clean_params, headers=headers
            ) as resp:
                    status = resp.status_code
                    buf = bytearray()
                    truncated = False
                    for chunk in resp.iter_bytes():
                        if not chunk:
                            continue
                        remaining = cap - len(buf)
                        if remaining <= 0:
                            truncated = True
                            break
                        if len(chunk) > remaining:
                            buf.extend(chunk[:remaining])
                            truncated = True
                            break
                        buf.extend(chunk)
                    if truncated:
                        err = PrometheusError(
                            f"prometheus {endpoint} response exceeded "
                            f"MCP_PROM_MAX_RESPONSE_BYTES ({cap} bytes); "
                            "aborted before full body was received. Narrow "
                            "the time range, lower `step`, or add label "
                            "filters."
                        )
                        set_span_error(_exec_span, err)
                        raise err
        except PrometheusError:
            raise
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

        if status != 200:
            # Log the upstream body snippet for operator debugging but
            # never return it to the caller (#1212) — upstream bodies
            # can leak Prometheus internals / tenancy data / stack
            # traces into agent memory. Return only the status code.
            try:
                snippet = bytes(buf[:512]).decode("utf-8", errors="replace")
            except Exception:
                snippet = "<undecodable>"
            log.warning(
                "prometheus %s upstream HTTP %d body snippet: %s",
                endpoint, status, snippet,
            )
            err = PrometheusError(f"prometheus returned HTTP {status}")
            set_span_error(_exec_span, err)
            raise err
        try:
            body = json.loads(bytes(buf).decode("utf-8", errors="replace"))
        except ValueError as exc:
            try:
                snippet = bytes(buf[:512]).decode("utf-8", errors="replace")
            except Exception:
                snippet = "<undecodable>"
            log.warning(
                "prometheus %s non-JSON body snippet: %s", endpoint, snippet,
            )
            err = PrometheusError(
                f"prometheus {endpoint} returned non-JSON body"
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
        # #1400: defensive lookup — try multiple known paths so a FastMCP minor
        # bump that renames the private attr doesn't silently empty /info.
        try:
            tool_names = sorted(mcp._tool_manager._tools.keys())  # type: ignore[attr-defined]
        except AttributeError:
            # #1404: mcp.list_tools() is async in current FastMCP; calling
            # .keys() on the coroutine raised silently in the prior fallback.
            # Log the attribute shape change so operators notice, rather
            # than silently emitting an empty tool list.
            log.warning(
                'mcp server: FastMCP internal _tool_manager._tools attr missing — '
                'falling back to empty tool_names (#1400/#1404). Upgrade guard needed.'
            )
            tool_names = []
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

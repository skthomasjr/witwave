"""Kubernetes MCP tool server.

Targets the cluster where this container is deployed. Loads in-cluster config
by default and falls back to the local kubeconfig for development.

All operations go through the Kubernetes API via the official Python client.
The dynamic client is used for kind-agnostic operations so we can reach any
resource the ServiceAccount has RBAC for.

Distributed tracing (#637): each tool handler opens an ``mcp.handler`` SERVER
span. The real Kubernetes API calls are wrapped in child ``k8s.api.call``
spans with ``k8s.verb`` / ``k8s.resource`` attributes. OTel is a no-op when
``OTEL_ENABLED`` is unset, so non-tracing installs pay no runtime cost.
"""

from __future__ import annotations

import contextlib
import logging
import os
import re
import sys
import threading
from typing import Any

import yaml
from kubernetes import client, config, dynamic
from kubernetes.client.rest import ApiException
from mcp.server.fastmcp import FastMCP

# shared/otel.py is copied into the image (see Dockerfile) and imported as a
# top-level module. Falls back to no-op shims if the shared module isn't on
# sys.path (e.g. running tests outside the container).
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

try:
    from mcp_audit import audit as _audit  # type: ignore
except Exception:  # pragma: no cover - defensive fallback
    def _audit(*_a: Any, **_kw: Any) -> None:  # type: ignore
        return None

log = logging.getLogger("tools.kubernetes")

mcp = FastMCP("kubernetes")


# Read-only / maintenance-mode gate (#1123). Mutating tools check this
# at handler entry and raise PermissionError with a clear message so
# the operator sees "MCP_READ_ONLY refused this call" rather than a
# confusing downstream apiserver rejection. Evaluated per-call so
# toggling the env var on a running pod takes effect without restart.
_READ_ONLY_ENV_VARS = {"MCP_READ_ONLY", "MCP_KUBERNETES_READ_ONLY"}


def _is_read_only() -> bool:
    for name in _READ_ONLY_ENV_VARS:
        if os.environ.get(name, "").strip().lower() in {"1", "true", "yes", "on"}:
            return True
    return False


def _refuse_if_read_only(tool: str) -> None:
    if _is_read_only():
        raise PermissionError(
            f"kubernetes {tool}: refused because MCP_READ_ONLY is set (#1123). "
            "Unset the env var or restart the pod without it to allow mutations."
        )

FIELD_MANAGER = "witwave-mcp-kubernetes"

# Per-call network timeout applied to Kubernetes API requests (#778).
# Defends against a stalled apiserver / slow log stream / hung watch
# pinning the FastMCP handler task. 120s is generous for most get/list
# calls; bulk log pulls and slow describe() paths may legitimately
# approach it. Override with MCP_SUBPROCESS_TIMEOUT_SEC for env
# parity with the helm tool (the knob applies to apiserver round-trips
# here rather than a subprocess, but the operator contract is the same:
# "no single MCP call blocks longer than this").
_MCP_REQUEST_TIMEOUT_SECONDS = float(
    os.environ.get("MCP_SUBPROCESS_TIMEOUT_SEC") or "120"
)

# Per-response byte cap on tool output (#778). Log fetches and large
# object returns are the two footguns the original issue flagged.
# 0 or negative disables the cap.
_MCP_RESPONSE_MAX_BYTES = int(
    os.environ.get("MCP_RESPONSE_MAX_BYTES") or str(8 * 1024 * 1024)
)

# #1526: upper bound on the caller-supplied ``limit`` in list_resources.
# Without a cap, an LLM-supplied ``limit=1000000`` makes the apiserver
# build a response body we'll only truncate client-side, wasting
# apiserver CPU and etcd bandwidth. 500 is the Kubernetes-project
# default chunk size; operators can raise or lower via env.
_MCP_LIST_LIMIT_MAX = int(
    os.environ.get("MCP_LIST_LIMIT_MAX") or "500"
)

# Hard ceiling on the tail_lines argument for logs() (#778). Even when
# the byte cap is disabled, the apiserver should not be asked for an
# unbounded log tail — the worst case is a multi-GB streaming fetch that
# holds kubelet and apiserver resources open. 50k lines is comfortably
# larger than any reasonable diagnostic window.
_LOGS_TAIL_LINES_MAX = int(
    os.environ.get("MCP_LOGS_TAIL_LINES_MAX") or "50000"
)


def _truncate_text(value: str, *, tool: str) -> str:
    """Cap ``value`` to MCP_RESPONSE_MAX_BYTES with a visible marker (#778)."""
    if not isinstance(value, str):
        return value
    cap = _MCP_RESPONSE_MAX_BYTES
    if cap <= 0:
        return value
    encoded = value.encode("utf-8", errors="replace")
    if len(encoded) <= cap:
        return value
    head = encoded[:cap].decode("utf-8", errors="replace")
    return (
        head
        + f"\n\n# [mcp-kubernetes:{tool}] response truncated: "
        f"{len(encoded)} bytes exceeded MCP_RESPONSE_MAX_BYTES={cap}."
    )


def _truncate_json(value: Any, *, tool: str) -> Any:
    """Cap a JSON-able payload to MCP_RESPONSE_MAX_BYTES (#778).

    Mirrors the helm tool's helper so operators see consistent
    truncation behaviour across MCP servers.
    """
    import json as _json
    cap = _MCP_RESPONSE_MAX_BYTES
    if cap <= 0 or value is None:
        return value
    try:
        raw = _json.dumps(value, default=str)
    except Exception:
        return value
    if len(raw.encode("utf-8", errors="replace")) <= cap:
        return value
    if isinstance(value, list):
        # #1324: on oversize lists, use the initial `raw` serialisation
        # (already computed above) to estimate a target-K up-front,
        # bounding per-item dumps to roughly O(K) rather than O(N).
        # Estimate: average item serialised length ≈ len(raw)/len(value),
        # target K so K * avg + framing < cap.
        _n = len(value)
        _avg = max(1, len(raw) // max(1, _n))
        _target_k = max(1, min(_n, cap // (_avg + 1)))
        trimmed: list[Any] = []
        running = 2
        # #1522: previously we hard-capped iteration at ``_target_k * 2``
        # so pathological small items caused early termination and
        # callers got far fewer rows than the cap allowed (continue
        # token was simultaneously cleared, so paging couldn't recover).
        # Let the per-item running-size check drive termination; the
        # _target_k/_avg estimate is still useful as the initial guess
        # but is no longer a scan ceiling. Full-list scan bounded by the
        # fact that we break the moment running + chunk exceeds cap.
        _ = _target_k  # retained for potential future heuristics
        for item in value:
            try:
                chunk = _json.dumps(item, default=str)
            except Exception:
                chunk = "null"
            if running + len(chunk) + 1 > cap:
                break
            trimmed.append(item)
            running += len(chunk) + 1
        return {
            "_truncated": True,
            "_original_length": _n,
            "_returned_length": len(trimmed),
            "_cap_bytes": cap,
            "items": trimmed,
        }
    if isinstance(value, dict) and "items" in value and isinstance(value["items"], list):
        inner = _truncate_json(value["items"], tool=tool)
        out = dict(value)
        if isinstance(inner, dict) and inner.get("_truncated"):
            out["items"] = inner["items"]
            out["_truncated"] = True
            out["_cap_bytes"] = cap
            out["_original_item_count"] = inner["_original_length"]
            out["_returned_item_count"] = inner["_returned_length"]
            # #1303/#1304: when we truncate, the apiserver's continue
            # token points PAST our trimmed rows. Returning it would
            # cause callers to skip data. Null it out so callers lower
            # `limit` and re-issue from the same position.
            # #1524: only write the top-level ``continue`` key when the
            # input envelope actually carried one. Previously any dict
            # with a ``metadata`` field picked up ``out["continue"] = ""``,
            # which callers reading ``result.get("continue")`` would
            # interpret as "paging exhausted" and stop — hiding truncation
            # from clients that don't speak the marker fields.
            _touched = False
            if "continue" in out:
                out["continue"] = ""
                _touched = True
            if "_continue" in out:
                out["_continue"] = ""
                _touched = True
            if isinstance(out.get("metadata"), dict):
                out["metadata"] = {**out["metadata"], "continue": ""}
                _touched = True
            if _touched:
                out["_continue_cleared_due_to_truncation"] = True
        else:
            out["items"] = inner
        return out
    return {
        "_truncated": True,
        "_cap_bytes": cap,
        "_note": (
            f"mcp-kubernetes:{tool} response exceeded "
            f"MCP_RESPONSE_MAX_BYTES ({cap}); raw payload suppressed."
        ),
    }

# Maximum length of a server-side-apply field manager string. The
# apiserver caps manager names at 128 characters; we truncate rather
# than fail so a long caller_id never breaks an apply.
_FIELD_MANAGER_MAX = 128
# DNS-1123-ish character allow-list for the caller-supplied suffix so
# the field_manager string stays well-behaved in audit output and SSA
# conflict messages. Everything else is replaced with '-'.
_FIELD_MANAGER_ALLOWED_RE = re.compile(r"[^A-Za-z0-9._-]")


def _resolve_field_manager(caller_id: str | None) -> str:
    """Derive the SSA field_manager for a given caller (#776).

    Base = FIELD_MANAGER. When ``caller_id`` is supplied (non-empty
    string), append a sanitised ``:<caller_id>`` suffix so two agents
    racing on the same resource land distinct SSA conflict messages
    and their writes are independently debuggable. Falls back to the
    AGENT_NAME env var, then the bare base when neither is available.
    """
    raw = caller_id
    if raw is None or (isinstance(raw, str) and raw.strip() == ""):
        raw = os.environ.get("AGENT_NAME") or ""
    raw = (raw or "").strip()
    if not raw:
        return FIELD_MANAGER
    sanitised = _FIELD_MANAGER_ALLOWED_RE.sub("-", raw)
    combined = f"{FIELD_MANAGER}:{sanitised}"
    if len(combined) > _FIELD_MANAGER_MAX:
        combined = combined[:_FIELD_MANAGER_MAX]
    return combined

# Positive allow-match patterns for values that flow into the Event
# field-selector (#773). The field-selector is comma/equals delimited and
# we want to prevent *any* character that could smuggle extra clauses,
# now or in future client-go revisions. Rather than enumerate the
# forbidden characters, we assert the grammar the apiserver actually
# accepts: DNS-1123 subdomain for metadata.name and PascalCase
# identifier for kind. Anything else is rejected.
_DNS1123_SUBDOMAIN_RE = re.compile(
    r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
)
_KIND_RE = re.compile(r"^[A-Z][A-Za-z0-9]*$")

_api_client: client.ApiClient | None = None
_dyn_client: dynamic.DynamicClient | None = None

# Prometheus counters for auth/discovery self-healing (#1082, #1083).
try:
    from prometheus_client import Counter as _Counter  # type: ignore

    mcp_k8s_token_reload_total = _Counter(
        "mcp_k8s_token_reload_total",
        "Count of Kubernetes client reloads triggered by a 401 response, "
        "e.g. projected ServiceAccount token rotation.",
        ["outcome"],
    )
    mcp_discovery_reload_total = _Counter(
        "mcp_discovery_reload_total",
        "Count of DynamicClient discovery-cache refreshes triggered by a "
        "ResourceNotFound / 404 during _resolve.",
        ["outcome"],
    )
    # Inner-work histogram for apiserver latency (#1126). Distinct from
    # mcp_tool_duration_seconds (the outer handler span) — operators
    # alert on this to attribute slowness to the apiserver round-trip
    # specifically, rather than to in-process redaction or parsing work.
    from prometheus_client import Histogram as _Histogram  # type: ignore

    k8s_api_call_duration_seconds = _Histogram(
        "k8s_api_call_duration_seconds",
        "Wall-clock duration of a Kubernetes API call from the MCP server.",
        ["verb", "resource", "outcome"],
        buckets=(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0),
    )
except Exception:  # pragma: no cover - metrics disabled
    mcp_k8s_token_reload_total = None  # type: ignore
    mcp_discovery_reload_total = None  # type: ignore
    k8s_api_call_duration_seconds = None  # type: ignore


def _load_kube_config() -> None:
    try:
        # try_refresh_token=True keeps the projected SA token fresh
        # across rotations rather than snapshotting it at ApiClient
        # construction (#1082). Older kubernetes client releases lack
        # the kwarg; fall back to the default signature in that case.
        try:
            config.load_incluster_config(try_refresh_token=True)
        except TypeError:
            config.load_incluster_config()
        log.info("loaded in-cluster kube config")
    except config.ConfigException:
        config.load_kube_config()
        log.info("loaded local kube config")


# Lazy-init locks (#1209). FastMCP streamable-http dispatches tool calls
# concurrently from a shared event loop, so two first-requests racing
# into _dyn() could each trigger a DynamicClient discovery pass — which
# costs a round-trip per apiserver resource and (with stale caches) can
# even return inconsistent discovery results. These helpers run under
# synchronous Python so threading.Lock is sufficient; asyncio.Lock is
# not needed because neither helper awaits.
_api_lock = threading.Lock()
_dyn_lock = threading.Lock()
# #1371: composite lock acquired during _reload_kube_clients so a 401
# retry can't race concurrent first-touches in _api()/_dyn() that
# might otherwise build a fresh ApiClient against the stale config.
_reload_lock = threading.Lock()


def _api() -> client.ApiClient:
    global _api_client
    if _api_client is None:
        # #1371: take the reload lock in SHARED mode conceptually — reader
        # path; but threading.Lock is exclusive-only, so briefly acquire
        # it to wait for any in-progress reload to finish.
        with _reload_lock:
            pass
        with _api_lock:
            if _api_client is None:
                _api_client = client.ApiClient()
    return _api_client


def _dyn() -> dynamic.DynamicClient:
    global _dyn_client
    if _dyn_client is None:
        # #1371: same wait-for-reload pattern as _api().
        with _reload_lock:
            pass
        with _dyn_lock:
            if _dyn_client is None:
                _dyn_client = dynamic.DynamicClient(_api())
    return _dyn_client


def _reload_kube_clients() -> None:
    """Drop cached ApiClient/DynamicClient and reload kube config (#1082).

    #1371: hold the composite reload lock across the nil-assign and
    the _load_kube_config() call so a concurrent _api()/_dyn() call
    waits for the reload to complete instead of constructing a fresh
    client against the stale config.
    """
    global _api_client, _dyn_client
    with _reload_lock:
        _api_client = None
        _dyn_client = None
        _load_kube_config()


# TODO(#1208): apply() resource.patch and delete() resource.delete are
# not yet wrapped in with_kube_retry. describe()'s resource.get and the
# event-list calls are now wrapped (#1641). The remaining write paths
# will follow in a separate pass.
def with_kube_retry(fn, *args, **kwargs):
    """Run ``fn`` and retry once on 401 after reloading kube config (#1082).

    Scope is narrow: only 401 Unauthorized triggers a reload. Other
    ApiException codes propagate unchanged so RBAC 403s, 404s,
    conflicts stay loud. Single-retry prevents a genuinely-broken
    auth posture from hammering the apiserver in a tight loop.
    """
    try:
        return fn(*args, **kwargs)
    except ApiException as exc:
        if getattr(exc, "status", None) != 401:
            raise
        try:
            _reload_kube_clients()
        except Exception:
            if mcp_k8s_token_reload_total is not None:
                try:
                    mcp_k8s_token_reload_total.labels(outcome="error").inc()
                except Exception:
                    pass
            raise
        if mcp_k8s_token_reload_total is not None:
            try:
                mcp_k8s_token_reload_total.labels(outcome="ok").inc()
            except Exception:
                pass
        log.info("kube 401: reloaded config, retrying once")
        return fn(*args, **kwargs)


def _resolve(kind: str, api_version: str | None = None):
    """Resolve a Kubernetes resource by kind, optionally disambiguated by apiVersion.

    A ResourceNotFoundError / 404 here often means the DynamicClient
    discovery cache is stale relative to the live apiserver — e.g. a
    CRD installed after this pod started. Invalidate the cache and
    retry once before raising (#1083).
    """
    def _do():
        if api_version:
            return _dyn().resources.get(api_version=api_version, kind=kind)
        return _dyn().resources.get(kind=kind)

    try:
        return _do()
    except Exception as exc:
        exc_name = type(exc).__name__
        status = getattr(exc, "status", None)
        is_missing = (
            exc_name in ("ResourceNotFoundError", "ResourceNotUniqueError")
            or status == 404
        )
        if not is_missing:
            raise
        global _dyn_client
        # #1523: hold the reload lock around the reset+rebuild so
        # concurrent 404s serialise on a single discovery pass instead
        # of each racing their own DynamicClient rebuild. _dyn() inside
        # _do() re-acquires the same lock via its double-checked lazy
        # init, so only the first caller pays the discovery cost.
        with _reload_lock:
            _dyn_client = None
        try:
            result = _do()
        except Exception:
            if mcp_discovery_reload_total is not None:
                try:
                    mcp_discovery_reload_total.labels(outcome="error").inc()
                except Exception:
                    pass
            raise
        if mcp_discovery_reload_total is not None:
            try:
                mcp_discovery_reload_total.labels(outcome="ok").inc()
            except Exception:
                pass
        log.info(
            "kube discovery miss (%s); reloaded DynamicClient and retried", exc_name
        )
        return result


def _to_dict(obj: Any) -> Any:
    if hasattr(obj, "to_dict"):
        return obj.to_dict()
    return obj


_REDACTED = "***REDACTED***"


def _redact_secret_payload(obj: Any, *, outer_kind: str | None = None) -> Any:
    """Replace .data / .stringData values on Secret objects with _REDACTED (#775).

    Triggers when either the object advertises ``kind == "Secret"`` OR the
    caller declares via ``outer_kind`` that it came from a list whose kind
    is Secret (#916). The dynamic-client list path strips per-item .kind
    from response items, so relying on obj.get('kind') alone silently
    bypasses redaction on every list_resources(kind='Secret') response.
    The keys themselves are retained (so callers can still see what fields
    exist) — only the base64-encoded payload is removed.
    Non-Secret kinds pass through unchanged.
    """
    if not isinstance(obj, dict):
        return obj
    _kind = obj.get("kind") or outer_kind
    if _kind != "Secret":
        return obj
    redacted = dict(obj)
    for field in ("data", "string_data", "stringData"):
        payload = redacted.get(field)
        if isinstance(payload, dict) and payload:
            redacted[field] = {k: _REDACTED for k in payload}
    return redacted


@contextlib.contextmanager
def _handler_span(tool: str, attributes: dict[str, Any] | None = None):
    """Open the outer ``mcp.handler`` SERVER span for a tool invocation.

    Also records the call against mcp_tool_calls_total /
    mcp_tool_duration_seconds (#851) with outcome=ok|error so
    operators can see per-tool rate and p95 latency alongside traces.
    """
    attrs: dict[str, Any] = {"mcp.server": "kubernetes", "mcp.tool": tool}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    with record_tool_call("kubernetes", tool):
        with start_span("mcp.handler", kind=SPAN_KIND_SERVER, attributes=attrs) as span:
            yield span


@contextlib.contextmanager
def _api_span(verb: str, resource: str, attributes: dict[str, Any] | None = None):
    """Open a child ``k8s.api.call`` span wrapping a Kubernetes API call.

    Also observes ``k8s_api_call_duration_seconds{verb, resource, outcome}``
    so operators can attribute latency specifically to the apiserver
    round-trip, separate from the outer ``mcp_tool_duration_seconds``
    (#1126). Records every call including failures so the histogram
    covers error-path latency as well.
    """
    attrs: dict[str, Any] = {"k8s.verb": verb, "k8s.resource": resource}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    import time as _t
    start = _t.monotonic()
    outcome = "ok"
    try:
        with start_span(
            "k8s.api.call", kind=SPAN_KIND_INTERNAL, attributes=attrs
        ) as span:
            try:
                yield span
            except BaseException:
                outcome = "error"
                raise
    finally:
        if k8s_api_call_duration_seconds is not None:
            try:
                k8s_api_call_duration_seconds.labels(
                    verb=verb, resource=resource, outcome=outcome,
                ).observe(_t.monotonic() - start)
            except Exception:
                pass


@mcp.tool()
def list_namespaces() -> list[str]:
    """List namespaces visible to the ServiceAccount."""
    with _handler_span("list_namespaces") as _h:
        try:
            core = client.CoreV1Api(_api())
            with _api_span("list", "Namespace"):
                # #1208: retry once on 401 (projected SA token rotation).
                resp = with_kube_retry(
                    lambda: core.list_namespace(
                        _request_timeout=_MCP_REQUEST_TIMEOUT_SECONDS,
                    )
                )
            return [ns.metadata.name for ns in resp.items]
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def list_resources(
    kind: str,
    namespace: str | None = None,
    api_version: str | None = None,
    label_selector: str | None = None,
    field_selector: str | None = None,
    limit: int | None = None,
    continue_token: str | None = None,
) -> dict:
    """List resources of a given kind, optionally scoped to a namespace.

    api_version disambiguates kinds served by multiple groups (e.g. Ingress).

    Pagination (#694): pass ``limit`` to cap the number of items returned
    in one response, and feed ``continue_token`` back from the previous
    response's ``continue`` field to fetch the next page. Returns a dict
    with ``items`` (list[dict]) and ``continue`` (the next token, or
    empty string when the list is exhausted).
    """
    with _handler_span(
        "list_resources",
        {"k8s.kind": kind, "k8s.namespace": namespace, "k8s.api_version": api_version},
    ) as _h:
        try:
            resource = _resolve(kind, api_version)
            kwargs: dict[str, Any] = {}
            if namespace:
                kwargs["namespace"] = namespace
            if label_selector:
                kwargs["label_selector"] = label_selector
            if field_selector:
                kwargs["field_selector"] = field_selector
            if limit is not None:
                if not isinstance(limit, int) or isinstance(limit, bool) or limit < 1:
                    raise ValueError("list_resources: 'limit' must be a positive int")
                # #1526: cap above MCP_LIST_LIMIT_MAX. Client-side
                # truncation happens anyway after the apiserver has
                # already built the oversized response body; coerce the
                # limit down at the edge so the apiserver / etcd don't
                # pay for bytes the caller will discard.
                if limit > _MCP_LIST_LIMIT_MAX:
                    log.info(
                        "list_resources: coercing limit %d down to "
                        "MCP_LIST_LIMIT_MAX=%d (#1526)",
                        limit, _MCP_LIST_LIMIT_MAX,
                    )
                    limit = _MCP_LIST_LIMIT_MAX
                kwargs["limit"] = limit
            if continue_token:
                kwargs["_continue"] = continue_token
            # Apply per-call network timeout (#778). The dynamic client
            # forwards _request_timeout through to the underlying urllib3
            # HTTP call, so a stalled apiserver cannot pin the handler
            # task indefinitely.
            kwargs["_request_timeout"] = _MCP_REQUEST_TIMEOUT_SECONDS
            with _api_span("list", kind, {"k8s.namespace": namespace}):
                # #1208: retry once on 401 so projected SA rotation
                # doesn't surface as a user-visible error.
                result = with_kube_retry(lambda: resource.get(**kwargs))
            items = getattr(result, "items", None) or []
            next_token = ""
            metadata = getattr(result, "metadata", None)
            if metadata is not None:
                next_token = getattr(metadata, "continue_", None) or getattr(metadata, "_continue", "") or ""
            # Redact Secret payloads by default (#775). If the caller
            # really wants secret material they must go through
            # read_secret_value, which is audited separately.
            # Pass the outer `kind` through so Secret items that lack
            # per-item .kind (dynamic-client list responses strip it) are
            # still redacted (#916).
            payload = {
                "items": [
                    _redact_secret_payload(_to_dict(item), outer_kind=kind)
                    for item in items
                ],
                "continue": next_token,
            }
            # Byte-cap the response (#778). Large list responses trim
            # trailing items rather than returning a single opaque
            # placeholder; the caller can page via `continue`.
            return _truncate_json(payload, tool="list_resources")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def get_resource(
    kind: str,
    name: str,
    namespace: str | None = None,
    api_version: str | None = None,
) -> dict:
    """Fetch a single resource by kind / namespace / name."""
    with _handler_span(
        "get_resource",
        {"k8s.kind": kind, "k8s.name": name, "k8s.namespace": namespace},
    ) as _h:
        try:
            resource = _resolve(kind, api_version)
            kwargs: dict[str, Any] = {"name": name}
            if namespace:
                kwargs["namespace"] = namespace
            # Per-call network timeout (#778).
            kwargs["_request_timeout"] = _MCP_REQUEST_TIMEOUT_SECONDS
            with _api_span("get", kind, {"k8s.name": name, "k8s.namespace": namespace}):
                # Redact Secret payload then enforce response-size cap
                # (#778) so a pathologically large CR body cannot OOM
                # the handler. #1208: retry once on 401.
                fetched = with_kube_retry(lambda: resource.get(**kwargs))
                obj = _redact_secret_payload(_to_dict(fetched))
                return _truncate_json(obj, tool="get_resource")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def describe(
    kind: str,
    name: str,
    namespace: str | None = None,
    api_version: str | None = None,
) -> dict:
    """Return a describe-equivalent view: the resource plus related events.

    Not a byte-for-byte match for `kubectl describe` (which has kind-specific
    formatters). This returns structured data that is easier for an agent to
    reason over.
    """
    with _handler_span(
        "describe",
        {"k8s.kind": kind, "k8s.name": name, "k8s.namespace": namespace},
    ) as _h:
        try:
            # Positive allow-match guard (#773). The Event field-selector
            # below is comma/equals delimited; rather than enumerate
            # forbidden metacharacters (exclusion posture regresses if
            # future client-go accepts new delimiters), assert that name
            # is a DNS-1123 subdomain and kind is a PascalCase identifier
            # — the grammars the apiserver itself validates.
            if not isinstance(name, str) or not _DNS1123_SUBDOMAIN_RE.fullmatch(name):
                raise ValueError(
                    f"describe: 'name' must be a DNS-1123 subdomain (got {name!r})"
                )
            if not isinstance(kind, str) or not _KIND_RE.fullmatch(kind):
                raise ValueError(
                    f"describe: 'kind' must be PascalCase [A-Z][A-Za-z0-9]* (got {kind!r})"
                )

            resource = _resolve(kind, api_version)
            kwargs: dict[str, Any] = {"name": name}
            if namespace:
                kwargs["namespace"] = namespace
            # Per-call network timeout (#778). #1641: route through
            # with_kube_retry so the documented timeout is honoured and
            # a stale 401 triggers a single config reload + retry on
            # parity with the other read paths.
            kwargs["_request_timeout"] = _MCP_REQUEST_TIMEOUT_SECONDS
            with _api_span("get", kind, {"k8s.name": name, "k8s.namespace": namespace}):
                obj = _redact_secret_payload(
                    _to_dict(with_kube_retry(lambda: resource.get(**kwargs)))
                )

            events: list[dict] = []
            # #1680: surface event-fetch failures to the caller via an
            # explicit envelope field. Without this, an empty `events`
            # list is ambiguous — the resource may genuinely have no
            # events, or the fetch may have failed (RBAC, timeout,
            # apiserver degradation). LLMs reasoning over describe
            # output cannot tell these apart and miss diagnostic
            # context (CrashLoopBackOff / ImagePullBackOff etc. that
            # show up in events but not on the Pod object). The field
            # is None on success and a short human-readable string on
            # failure.
            events_fetch_error: str | None = None
            try:
                core = client.CoreV1Api(_api())
                selector = f"involvedObject.name={name},involvedObject.kind={kind}"
                with _api_span("list", "Event", {"k8s.namespace": namespace}):
                    if namespace:
                        # #1641: wrap in with_kube_retry for timeout +
                        # 401 reload parity with describe()'s primary
                        # resource.get above.
                        ev_resp = with_kube_retry(
                            lambda: core.list_namespaced_event(
                                namespace=namespace, field_selector=selector,
                                _request_timeout=_MCP_REQUEST_TIMEOUT_SECONDS,
                            )
                        )
                    else:
                        # #1641: same wrapping as the namespaced branch.
                        ev_resp = with_kube_retry(
                            lambda: core.list_event_for_all_namespaces(
                                field_selector=selector,
                                _request_timeout=_MCP_REQUEST_TIMEOUT_SECONDS,
                            )
                        )
                events = [ev.to_dict() for ev in ev_resp.items]
            except ApiException as e:
                log.warning("failed to fetch events for %s/%s: %s", kind, name, e)
                # Surface status+reason so RBAC misconfigs (403 Forbidden)
                # are immediately visible without grep'ing pod logs.
                _status = getattr(e, "status", None)
                _reason = getattr(e, "reason", None) or "ApiException"
                events_fetch_error = (
                    f"{_status} {_reason}" if _status else str(_reason)
                )
            except Exception as e:
                # Degraded-apiserver or urllib3/HTTP errors must not nuke
                # the primary resource view — demote to a warning and
                # return the object with an empty events list so the
                # caller still sees what they asked for (#1029).
                log.warning(
                    "events fetch failed for %s/%s with non-ApiException %s: %s",
                    kind,
                    name,
                    type(e).__name__,
                    e,
                )
                events = []
                events_fetch_error = f"{type(e).__name__}: {e}"

            return _truncate_json(
                {
                    "object": obj,
                    "events": events,
                    "events_fetch_error": events_fetch_error,
                },
                tool="describe",
            )
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def read_secret_value(
    name: str,
    namespace: str,
    confirm: bool = False,
) -> dict:
    """Return the base64-encoded data payload of a Secret.

    Explicit-opt-in audited read (#775). Separate tool so that the
    default fetch paths (get_resource/list_resources/describe) redact
    Secret payloads and only this tool — invoked with an explicit
    ``confirm=True`` — exposes them. The span attributes mark the call
    as secret-bearing so OTel/alerting can raise on usage.
    """
    # Operator-level kill switch (#1207). Distinct from MCP_READ_ONLY
    # because secret-read can be a concern even when the tool server is
    # otherwise read-write — an operator may want apply/delete enabled
    # but Secret material off-limits. Checked first so we refuse
    # without touching the apiserver.
    if os.environ.get("MCP_K8S_READ_SECRETS_DISABLED", "").strip().lower() in {
        "1", "true", "yes", "on",
    }:
        raise PermissionError(
            "read_secret_value: refused because MCP_K8S_READ_SECRETS_DISABLED "
            "is set — the operator has disabled raw Secret payload reads on "
            "this tool server (#1207). Unset the env var or restart the pod "
            "without it to allow this tool."
        )
    if not isinstance(name, str) or not _DNS1123_SUBDOMAIN_RE.fullmatch(name):
        raise ValueError(
            f"read_secret_value: 'name' must be a DNS-1123 subdomain (got {name!r})"
        )
    if not isinstance(namespace, str) or not _DNS1123_SUBDOMAIN_RE.fullmatch(namespace):
        raise ValueError(
            f"read_secret_value: 'namespace' must be a DNS-1123 subdomain (got {namespace!r})"
        )
    if not confirm:
        raise ValueError(
            "read_secret_value: call requires confirm=True — this tool exposes "
            "raw Secret material into logs and memory; pass confirm=True only "
            "when the caller genuinely needs the payload."
        )
    _audit(
        "mcp-kubernetes", "read_secret_value",
        args={"name": name, "namespace": namespace},
        outcome="invoked",
    )
    with _handler_span(
        "read_secret_value",
        {"k8s.name": name, "k8s.namespace": namespace, "k8s.secret_read": True},
    ) as _h:
        try:
            core = client.CoreV1Api(_api())
            with _api_span("get", "Secret", {"k8s.name": name, "k8s.namespace": namespace}):
                # #1208: retry once on 401.
                sec = with_kube_retry(
                    lambda: core.read_namespaced_secret(
                        name=name, namespace=namespace,
                        _request_timeout=_MCP_REQUEST_TIMEOUT_SECONDS,
                    )
                )
            return {
                "name": name,
                "namespace": namespace,
                "type": getattr(sec, "type", None),
                "data": dict(getattr(sec, "data", None) or {}),
            }
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def logs(
    pod: str,
    namespace: str,
    container: str | None = None,
    tail_lines: int | None = 200,
    since_seconds: int | None = None,
    previous: bool = False,
) -> str:
    """Return pod logs."""
    # Defense-in-depth DNS-1123 guard (#1032). describe() and
    # read_secret_value() already enforce this shape; logs() previously
    # relied on the downstream client to reject malformed names, which
    # drifts by client-go version. Apply the same positive allow-match
    # at the tool boundary so every Pod/namespace string hitting the
    # apiserver from MCP has the same validated shape.
    if not isinstance(pod, str) or not _DNS1123_SUBDOMAIN_RE.fullmatch(pod):
        raise ValueError(
            f"logs: 'pod' must be a DNS-1123 subdomain (got {pod!r})"
        )
    if not isinstance(namespace, str) or not _DNS1123_SUBDOMAIN_RE.fullmatch(namespace):
        raise ValueError(
            f"logs: 'namespace' must be a DNS-1123 subdomain (got {namespace!r})"
        )
    if container is not None and (
        not isinstance(container, str) or not _DNS1123_SUBDOMAIN_RE.fullmatch(container)
    ):
        raise ValueError(
            f"logs: 'container' must be a DNS-1123 subdomain (got {container!r})"
        )
    with _handler_span(
        "logs",
        {"k8s.pod": pod, "k8s.namespace": namespace, "k8s.container": container},
    ) as _h:
        try:
            core = client.CoreV1Api(_api())
            kwargs: dict[str, Any] = {"name": pod, "namespace": namespace, "previous": previous}
            if container:
                kwargs["container"] = container
            # Hard-cap tail_lines at _LOGS_TAIL_LINES_MAX (#778) so even
            # when the caller supplies a multi-million-line tail the
            # apiserver fetch is bounded. None/unset falls back to the
            # backend SDK default.
            if tail_lines is None:
                effective_tail = 200
            else:
                if not isinstance(tail_lines, int) or isinstance(tail_lines, bool):
                    raise ValueError("logs: 'tail_lines' must be an int")
                if tail_lines < 1:
                    raise ValueError("logs: 'tail_lines' must be >= 1")
                effective_tail = min(tail_lines, _LOGS_TAIL_LINES_MAX)
            kwargs["tail_lines"] = effective_tail
            if since_seconds is not None:
                # Bounds-check since_seconds (#1210). Reject non-int /
                # <1 / >one week. Upper cap stops a caller from asking
                # for effectively unbounded history (which the apiserver
                # would then stream in full, pinning kubelet + apiserver).
                if not isinstance(since_seconds, int) or isinstance(since_seconds, bool):
                    raise ValueError("logs: 'since_seconds' must be an int")
                if since_seconds < 1 or since_seconds > 604800:
                    raise ValueError(
                        "logs: 'since_seconds' must be between 1 and 604800 "
                        "(one week) (#1210)"
                    )
                kwargs["since_seconds"] = since_seconds
            # Per-call network timeout (#778). Prevents a wedged pod
            # log stream from pinning the handler indefinitely.
            kwargs["_request_timeout"] = _MCP_REQUEST_TIMEOUT_SECONDS
            with _api_span("logs", "Pod", {"k8s.pod": pod, "k8s.namespace": namespace}):
                # #1208: retry once on 401.
                raw = with_kube_retry(
                    lambda: core.read_namespaced_pod_log(**kwargs)
                )
            # Byte-cap the log payload (#778). Even at a bounded line
            # count a single line can carry arbitrary bytes, so measure
            # the final string rather than trusting tail_lines alone.
            return _truncate_text(raw or "", tool="logs")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def apply(
    manifest: str,
    caller_id: str | None = None,
    dry_run: bool = False,
) -> list[dict]:
    """Server-side apply a YAML or JSON manifest (supports multi-doc YAML).

    ``caller_id`` is appended to the SSA field_manager as a sanitised
    suffix (``witwave-mcp-kubernetes:<caller_id>``) so two agents racing
    on the same resource surface distinct SSA conflict messages and
    audit trails (#776). Falls back to the AGENT_NAME env var, then
    the bare base manager when neither is supplied.

    Set ``dry_run=True`` for server-side dry-run (#854): the apiserver
    validates and resolves the apply as if it would commit, but skips
    persistence. The returned objects reflect the resolved post-apply
    state so LLM callers can inspect what WOULD change before running
    for real.
    """
    # Respect MCP_READ_ONLY even for server-side dry-run (#1123): a
    # dry-run still opens a write-path discovery and can mutate CR
    # defaulting/validation state in some CRDs, and — more importantly —
    # operators asking for read-only want a hard surface, not "well it
    # depends on the flag".
    _refuse_if_read_only("apply")
    field_manager = _resolve_field_manager(caller_id)
    _audit(
        "mcp-kubernetes", "apply",
        args={"manifest": manifest, "caller_id": caller_id,
              "field_manager": field_manager},
        caller=caller_id,
        dry_run=dry_run,
    )
    with _handler_span("apply", {"k8s.field_manager": field_manager, "k8s.dry_run": dry_run}) as _h:
        try:
            log.info("apply: field_manager=%s dry_run=%s", field_manager, dry_run)
            docs = [d for d in yaml.safe_load_all(manifest) if d]
            # Pre-resolve every doc up front so apiVersion/kind/metadata
            # validation happens before any write hits the cluster.
            prepared: list[tuple[Any, dict[str, Any], dict[str, Any]]] = []
            for doc in docs:
                api_version = doc.get("apiVersion")
                kind = doc.get("kind")
                if not api_version or not kind:
                    raise ValueError("manifest document missing apiVersion or kind")
                meta = doc.get("metadata") or {}
                name = meta.get("name")
                ns = meta.get("namespace")
                if not name:
                    raise ValueError(f"{kind} manifest missing metadata.name")

                resource = _resolve(kind, api_version)
                patch_kwargs: dict[str, Any] = {
                    "body": doc,
                    "name": name,
                    # The dynamic client serializes dict bodies as JSON, so
                    # advertise JSON to match the wire format. Supported by
                    # the Kubernetes API server since 1.30 (#695).
                    "content_type": "application/apply-patch+json",
                    "field_manager": field_manager,
                }
                if ns:
                    patch_kwargs["namespace"] = ns
                # Per-call network timeout (#778).
                patch_kwargs["_request_timeout"] = _MCP_REQUEST_TIMEOUT_SECONDS
                span_attrs = {
                    "k8s.name": name,
                    "k8s.namespace": ns,
                    "k8s.api_version": api_version,
                    "k8s.field_manager": field_manager,
                }
                prepared.append((resource, patch_kwargs, span_attrs))

            # #1525: multi-doc manifests were applied sequentially with no
            # preflight — a failure on doc N left docs 0..N-1 committed and
            # the cluster in a partial state invisible to the caller.
            # When not already a dry-run, run a server-side dry-run over
            # every doc first; only if every doc validates does the real
            # apply proceed. This narrows the partial-apply window to
            # "dry-run passed but real apply failed" (admission races,
            # quota, conflicts between the two phases), which callers can
            # distinguish by the presence of ``_applied_before_failure`` in
            # the error context — we re-raise with a suppressed inner
            # exception to preserve the original trace.
            if not dry_run:
                for resource, patch_kwargs, span_attrs in prepared:
                    dry_kwargs = dict(patch_kwargs)
                    dry_kwargs["dry_run"] = "All"
                    with _api_span(
                        "apply",
                        span_attrs.get("k8s.api_version", ""),
                        {**span_attrs, "k8s.dry_run": True,
                         "k8s.apply_phase": "preflight"},
                    ):
                        resource.patch(**dry_kwargs)

            results: list[dict] = []
            for resource, patch_kwargs, span_attrs in prepared:
                live_kwargs = dict(patch_kwargs)
                if dry_run:
                    # Server-side dry-run (#854, #917): the apiserver
                    # resolves the apply end-to-end (admission, defaulting,
                    # conflicts) but skips persistence. The dynamic client's
                    # kwarg→query-param translation for ``dry_run`` is
                    # version-dependent — some releases accept ``["All"]``,
                    # others silently drop it and persist the object. Use
                    # the explicit string form that every shipped kubernetes
                    # client translates to ``?dryRun=All`` on the wire.
                    live_kwargs["dry_run"] = "All"
                with _api_span(
                    "apply",
                    span_attrs.get("k8s.api_version", ""),
                    {**span_attrs, "k8s.dry_run": dry_run,
                     "k8s.apply_phase": "commit"},
                ):
                    try:
                        applied = resource.patch(**live_kwargs)
                    except Exception as _commit_exc:
                        # Attach count of already-committed docs to the
                        # error for operator triage; re-raise to preserve
                        # the original traceback.
                        _commit_exc._applied_before_failure = len(results)  # type: ignore[attr-defined]
                        raise
                results.append(_to_dict(applied))
            return results
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def delete(
    kind: str,
    name: str,
    namespace: str | None = None,
    api_version: str | None = None,
    propagation_policy: str = "Background",
    dry_run: bool = False,
) -> dict:
    """Delete a resource by kind / namespace / name.

    Set ``dry_run=True`` to validate the delete without actually
    removing the object (#854). The apiserver runs the full delete
    flow (finalizer accounting, cascade resolution) but skips
    persistence, so LLM callers can confirm cascade semantics before
    running for real.
    """
    _refuse_if_read_only("delete")
    _audit(
        "mcp-kubernetes", "delete",
        args={"kind": kind, "name": name, "namespace": namespace,
              "api_version": api_version, "propagation_policy": propagation_policy},
        dry_run=dry_run,
    )
    with _handler_span(
        "delete",
        {"k8s.kind": kind, "k8s.name": name, "k8s.namespace": namespace,
         "k8s.dry_run": dry_run},
    ) as _h:
        try:
            resource = _resolve(kind, api_version)
            body = client.V1DeleteOptions(propagation_policy=propagation_policy)
            kwargs: dict[str, Any] = {"name": name, "body": body}
            if namespace:
                kwargs["namespace"] = namespace
            if dry_run:
                # Explicit string form (#917) — list-of-strings form is
                # accepted by some kubernetes client releases and silently
                # dropped by others, risking a real delete when the caller
                # expected a dry-run.
                kwargs["dry_run"] = "All"
            # Per-call network timeout (#778).
            kwargs["_request_timeout"] = _MCP_REQUEST_TIMEOUT_SECONDS
            with _api_span("delete", kind, {"k8s.name": name, "k8s.namespace": namespace, "k8s.dry_run": dry_run}):
                return _to_dict(resource.delete(**kwargs))
        except Exception as exc:
            set_span_error(_h, exc)
            raise


def _get_info_doc() -> dict[str, Any]:
    """Build the /info document for the kubernetes tool server (#1122).

    Reports image version, kubernetes client version, enabled feature
    flags, and the registered tool list. Deliberately avoids any API
    call that would leak cluster state.
    """
    image_version = (
        os.environ.get("IMAGE_VERSION")
        or os.environ.get("IMAGE_TAG")
        or os.environ.get("VERSION")
        or "unknown"
    )
    try:
        import kubernetes as _k8s  # type: ignore
        kube_client_version = getattr(_k8s, "__version__", "unknown")
    except Exception:
        kube_client_version = "unavailable"

    # #1759: consult both MCP_READ_ONLY and MCP_KUBERNETES_READ_ONLY through
    # the shared _is_read_only() helper so /info matches what
    # _refuse_if_read_only() actually enforces. Previously this branch only
    # checked the global var, so MCP_KUBERNETES_READ_ONLY=true paths
    # reported features.read_only=false in /info while still refusing
    # mutations.
    read_only = _is_read_only()

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
        "server": "mcp-kubernetes",
        "image_version": image_version,
        "kube_client_version": kube_client_version,
        "features": {
            "read_only": read_only,
            "otel": bool(os.environ.get("OTEL_ENABLED")),
            "metrics": bool(os.environ.get("METRICS_ENABLED")),
            "token_reload_on_401": True,  # #1082
            "discovery_reload_on_404": True,  # #1083
        },
        "tools": tool_names,
    }


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    # Initialise OTel up-front; no-op unless OTEL_ENABLED is truthy (#637).
    init_otel_if_enabled(
        service_name=os.environ.get("OTEL_SERVICE_NAME") or "mcp-kubernetes",
    )

    # Dedicated Prometheus metrics listener on :METRICS_PORT (default 9000)
    # separate from the streamable-http MCP port (#643, #649). FastMCP owns
    # the main event loop so the metrics server runs in a daemon thread
    # with its own loop. Exposes default prom_client collectors (process
    # stats, GC) today; richer tool-call histograms are a follow-up once
    # FastMCP's middleware hooks stabilise.
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
                logger=logging.getLogger("mcp-kubernetes.metrics"),
            )
        except Exception as _e:  # pragma: no cover - defensive
            logging.getLogger(__name__).warning(
                "metrics listener failed to start — continuing without it: %r", _e
            )

    _load_kube_config()
    # Eagerly initialise the shared ApiClient and DynamicClient at startup
    # (#696). FastMCP streamable-http dispatches tool calls concurrently,
    # so the previous check-then-set in _api()/_dyn() could let two
    # simultaneous first requests each trigger a DynamicClient discovery
    # pass. _api() and _dyn() remain safe for repeat callers once
    # initialised because both globals are already populated by the time
    # mcp.run() begins accepting requests.
    _api()
    _dyn()
    # Streamable-HTTP transport so the container is reachable across pod
    # boundaries (#644). stdio mode (FastMCP's default) assumes a local
    # fork/exec client and can't be consumed from a separate pod's
    # backend container via `.claude/mcp.json` URL references.
    #
    # Wrap the FastMCP Starlette app with the shared bearer-token
    # middleware (#771) so an MCP_TOOL_AUTH_TOKEN can gate invocation.
    # Falls back to mcp.run() when the middleware cannot be imported
    # (e.g. bare dev checkout) so no deployment surface breaks.
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

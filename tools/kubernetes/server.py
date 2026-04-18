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

log = logging.getLogger("tools.kubernetes")

mcp = FastMCP("kubernetes")

FIELD_MANAGER = "nyx-mcp-kubernetes"

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


def _load_kube_config() -> None:
    try:
        config.load_incluster_config()
        log.info("loaded in-cluster kube config")
    except config.ConfigException:
        config.load_kube_config()
        log.info("loaded local kube config")


def _api() -> client.ApiClient:
    global _api_client
    if _api_client is None:
        _api_client = client.ApiClient()
    return _api_client


def _dyn() -> dynamic.DynamicClient:
    global _dyn_client
    if _dyn_client is None:
        _dyn_client = dynamic.DynamicClient(_api())
    return _dyn_client


def _resolve(kind: str, api_version: str | None = None):
    """Resolve a Kubernetes resource by kind, optionally disambiguated by apiVersion."""
    if api_version:
        return _dyn().resources.get(api_version=api_version, kind=kind)
    return _dyn().resources.get(kind=kind)


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


def _api_span(verb: str, resource: str, attributes: dict[str, Any] | None = None):
    """Open a child ``k8s.api.call`` span wrapping a Kubernetes API call."""
    attrs: dict[str, Any] = {"k8s.verb": verb, "k8s.resource": resource}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    return start_span("k8s.api.call", kind=SPAN_KIND_INTERNAL, attributes=attrs)


@mcp.tool()
def list_namespaces() -> list[str]:
    """List namespaces visible to the ServiceAccount."""
    with _handler_span("list_namespaces") as _h:
        try:
            core = client.CoreV1Api(_api())
            with _api_span("list", "Namespace"):
                resp = core.list_namespace()
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
                kwargs["limit"] = limit
            if continue_token:
                kwargs["_continue"] = continue_token
            with _api_span("list", kind, {"k8s.namespace": namespace}):
                result = resource.get(**kwargs)
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
            return {
                "items": [
                    _redact_secret_payload(_to_dict(item), outer_kind=kind)
                    for item in items
                ],
                "continue": next_token,
            }
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
            with _api_span("get", kind, {"k8s.name": name, "k8s.namespace": namespace}):
                # Redact Secret payload before returning (#775).
                return _redact_secret_payload(_to_dict(resource.get(**kwargs)))
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
            with _api_span("get", kind, {"k8s.name": name, "k8s.namespace": namespace}):
                obj = _redact_secret_payload(_to_dict(resource.get(**kwargs)))

            events: list[dict] = []
            try:
                core = client.CoreV1Api(_api())
                selector = f"involvedObject.name={name},involvedObject.kind={kind}"
                with _api_span("list", "Event", {"k8s.namespace": namespace}):
                    if namespace:
                        ev_resp = core.list_namespaced_event(
                            namespace=namespace, field_selector=selector
                        )
                    else:
                        ev_resp = core.list_event_for_all_namespaces(field_selector=selector)
                events = [ev.to_dict() for ev in ev_resp.items]
            except ApiException as e:
                log.warning("failed to fetch events for %s/%s: %s", kind, name, e)

            return {"object": obj, "events": events}
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
    with _handler_span(
        "read_secret_value",
        {"k8s.name": name, "k8s.namespace": namespace, "k8s.secret_read": True},
    ) as _h:
        try:
            core = client.CoreV1Api(_api())
            with _api_span("get", "Secret", {"k8s.name": name, "k8s.namespace": namespace}):
                sec = core.read_namespaced_secret(name=name, namespace=namespace)
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
    with _handler_span(
        "logs",
        {"k8s.pod": pod, "k8s.namespace": namespace, "k8s.container": container},
    ) as _h:
        try:
            core = client.CoreV1Api(_api())
            kwargs: dict[str, Any] = {"name": pod, "namespace": namespace, "previous": previous}
            if container:
                kwargs["container"] = container
            if tail_lines is not None:
                kwargs["tail_lines"] = tail_lines
            if since_seconds is not None:
                kwargs["since_seconds"] = since_seconds
            with _api_span("logs", "Pod", {"k8s.pod": pod, "k8s.namespace": namespace}):
                return core.read_namespaced_pod_log(**kwargs)
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
    suffix (``nyx-mcp-kubernetes:<caller_id>``) so two agents racing
    on the same resource surface distinct SSA conflict messages and
    audit trails (#776). Falls back to the AGENT_NAME env var, then
    the bare base manager when neither is supplied.

    Set ``dry_run=True`` for server-side dry-run (#854): the apiserver
    validates and resolves the apply as if it would commit, but skips
    persistence. The returned objects reflect the resolved post-apply
    state so LLM callers can inspect what WOULD change before running
    for real.
    """
    field_manager = _resolve_field_manager(caller_id)
    with _handler_span("apply", {"k8s.field_manager": field_manager, "k8s.dry_run": dry_run}) as _h:
        try:
            log.info("apply: field_manager=%s dry_run=%s", field_manager, dry_run)
            docs = [d for d in yaml.safe_load_all(manifest) if d]
            results: list[dict] = []
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
                if dry_run:
                    # Server-side dry-run: the apiserver resolves the apply
                    # end-to-end (admission, defaulting, conflicts) but
                    # skips persistence. Exact field name expected by the
                    # dynamic client's REST helper.
                    patch_kwargs["dry_run"] = ["All"]
                with _api_span(
                    "apply",
                    kind,
                    {
                        "k8s.name": name,
                        "k8s.namespace": ns,
                        "k8s.api_version": api_version,
                        "k8s.field_manager": field_manager,
                        "k8s.dry_run": dry_run,
                    },
                ):
                    applied = resource.patch(**patch_kwargs)
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
                kwargs["dry_run"] = ["All"]
            with _api_span("delete", kind, {"k8s.name": name, "k8s.namespace": namespace, "k8s.dry_run": dry_run}):
                return _to_dict(resource.delete(**kwargs))
        except Exception as exc:
            set_span_error(_h, exc)
            raise


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
        _app = require_bearer_token(_app)
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

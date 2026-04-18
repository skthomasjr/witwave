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

import logging
import os
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

log = logging.getLogger("tools.kubernetes")

mcp = FastMCP("kubernetes")

FIELD_MANAGER = "nyx-mcp-kubernetes"

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


def _handler_span(tool: str, attributes: dict[str, Any] | None = None):
    """Open the outer ``mcp.handler`` SERVER span for a tool invocation."""
    attrs: dict[str, Any] = {"mcp.server": "kubernetes", "mcp.tool": tool}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    return start_span("mcp.handler", kind=SPAN_KIND_SERVER, attributes=attrs)


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
) -> list[dict]:
    """List resources of a given kind, optionally scoped to a namespace.

    api_version disambiguates kinds served by multiple groups (e.g. Ingress).
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
            with _api_span("list", kind, {"k8s.namespace": namespace}):
                result = resource.get(**kwargs)
            items = getattr(result, "items", None) or []
            return [_to_dict(item) for item in items]
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
                return _to_dict(resource.get(**kwargs))
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
            # Reject values that would break the comma/equals-delimited
            # field selector syntax below. Kubernetes object names are
            # DNS-1123 labels (no commas, equals, or whitespace) and kinds
            # are PascalCase identifiers, so this only rejects invalid
            # inputs that could otherwise smuggle extra selector clauses.
            for _field, _value in (("name", name), ("kind", kind)):
                if any(c in _value for c in (",", "=")) or any(c.isspace() for c in _value):
                    raise ValueError(
                        f"describe: {_field!r} must not contain ',', '=', or whitespace"
                    )

            resource = _resolve(kind, api_version)
            kwargs: dict[str, Any] = {"name": name}
            if namespace:
                kwargs["namespace"] = namespace
            with _api_span("get", kind, {"k8s.name": name, "k8s.namespace": namespace}):
                obj = _to_dict(resource.get(**kwargs))

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
def apply(manifest: str) -> list[dict]:
    """Server-side apply a YAML or JSON manifest (supports multi-doc YAML)."""
    with _handler_span("apply") as _h:
        try:
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
                    "field_manager": FIELD_MANAGER,
                }
                if ns:
                    patch_kwargs["namespace"] = ns
                with _api_span(
                    "apply", kind, {"k8s.name": name, "k8s.namespace": ns, "k8s.api_version": api_version}
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
) -> dict:
    """Delete a resource by kind / namespace / name."""
    with _handler_span(
        "delete",
        {"k8s.kind": kind, "k8s.name": name, "k8s.namespace": namespace},
    ) as _h:
        try:
            resource = _resolve(kind, api_version)
            body = client.V1DeleteOptions(propagation_policy=propagation_policy)
            kwargs: dict[str, Any] = {"name": name, "body": body}
            if namespace:
                kwargs["namespace"] = namespace
            with _api_span("delete", kind, {"k8s.name": name, "k8s.namespace": namespace}):
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
    # Streamable-HTTP transport so the container is reachable across pod
    # boundaries (#644). stdio mode (FastMCP's default) assumes a local
    # fork/exec client and can't be consumed from a separate pod's
    # backend container via `.claude/mcp.json` URL references.
    mcp.run(
        transport="streamable-http",
        host="0.0.0.0",
        port=int(os.environ.get("MCP_PORT", "8000")),
    )

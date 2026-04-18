"""Helm MCP tool server.

Shells out to the `helm` CLI. Helm has no REST API and no Python SDK — the
only first-class programmatic surface is the Go SDK, so every Python wrapper
in the ecosystem ultimately calls `helm` as a subprocess. We do the same,
directly.

Runs against the cluster where this container is deployed. Helm picks up the
ServiceAccount token and API server via the standard in-cluster env vars; no
kubeconfig handling is done here.

Distributed tracing (#637): each tool handler opens an ``mcp.handler`` SERVER
span. Every `helm` subprocess invocation is wrapped in a ``helm.exec`` child
span with a ``helm.command`` attribute. OTel is a no-op when ``OTEL_ENABLED``
is unset, so non-tracing installs pay no runtime cost.
"""

from __future__ import annotations

import json
import logging
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

import yaml
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

log = logging.getLogger("tools.helm")

mcp = FastMCP("helm")


class HelmError(RuntimeError):
    """Raised when a helm CLI invocation fails."""


def _helm(args: list[str], parse_json: bool = False) -> Any:
    cmd = ["helm", *args]
    log.debug("exec: %s", " ".join(cmd))
    # helm.exec child span — captures subprocess latency independent of the
    # outer mcp.handler span, so operators can attribute time spent in the
    # CLI vs. in-process work.
    with start_span(
        "helm.exec",
        kind=SPAN_KIND_INTERNAL,
        attributes={"helm.command": args[0] if args else "", "helm.args": " ".join(args)},
    ) as _exec_span:
        try:
            proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
            if proc.returncode != 0:
                err = HelmError(
                    f"helm {' '.join(args)} exited {proc.returncode}: "
                    f"{(proc.stderr or proc.stdout).strip()}"
                )
                set_span_error(_exec_span, err)
                raise err
            if parse_json:
                out = proc.stdout.strip()
                return json.loads(out) if out else None
            return proc.stdout
        except HelmError:
            raise
        except Exception as exc:
            set_span_error(_exec_span, exc)
            raise


def _handler_span(tool: str, attributes: dict[str, Any] | None = None):
    """Open the outer ``mcp.handler`` SERVER span for a tool invocation."""
    attrs: dict[str, Any] = {"mcp.server": "helm", "mcp.tool": tool}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    return start_span("mcp.handler", kind=SPAN_KIND_SERVER, attributes=attrs)


def _write_values(values: dict | None) -> Path | None:
    if not values:
        return None
    fd, path = tempfile.mkstemp(suffix=".yaml", prefix="helm-values-")
    try:
        with os.fdopen(fd, "w") as f:
            yaml.safe_dump(values, f)
    except Exception:
        # safe_dump (or the fdopen/write) failed — tempfile.mkstemp already
        # created the on-disk file. Remove it so we do not leak orphaned
        # /tmp/helm-values-*.yaml files on the pod filesystem.
        try:
            os.unlink(path)
        except OSError:
            pass
        raise
    return Path(path)


def _ns_args(namespace: str | None, all_namespaces: bool = False) -> list[str]:
    if all_namespaces:
        return ["-A"]
    if namespace:
        return ["-n", namespace]
    return []


@mcp.tool()
def list_releases(namespace: str | None = None, all_namespaces: bool = False) -> list[dict]:
    """List Helm releases."""
    with _handler_span(
        "list_releases",
        {"helm.namespace": namespace, "helm.all_namespaces": all_namespaces},
    ) as _h:
        try:
            return _helm(
                ["list", "-o", "json", *_ns_args(namespace, all_namespaces)], parse_json=True
            ) or []
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def get_release(name: str, namespace: str) -> dict:
    """Return metadata + values + manifest for a release."""
    with _handler_span("get_release", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            values = get_values(name=name, namespace=namespace, all_values=True)
            manifest = get_manifest(name=name, namespace=namespace)
            hist = history(name=name, namespace=namespace, max_revisions=1)
            current = hist[-1] if hist else None
            return {
                "name": name,
                "namespace": namespace,
                "current_revision": current,
                "values": values,
                "manifest": manifest,
            }
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def get_values(name: str, namespace: str, all_values: bool = False) -> dict:
    """Return user-supplied values (or all computed values) for a release."""
    with _handler_span("get_values", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            args = ["get", "values", name, "-n", namespace, "-o", "json"]
            if all_values:
                args.append("-a")
            return _helm(args, parse_json=True) or {}
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def get_manifest(name: str, namespace: str) -> str:
    """Return the rendered manifest for a release."""
    with _handler_span("get_manifest", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            return _helm(["get", "manifest", name, "-n", namespace])
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def history(name: str, namespace: str, max_revisions: int = 10) -> list[dict]:
    """Return revision history for a release."""
    with _handler_span("history", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            return _helm(
                ["history", name, "-n", namespace, "--max", str(max_revisions), "-o", "json"],
                parse_json=True,
            ) or []
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def install(
    name: str,
    chart: str,
    namespace: str,
    values: dict | None = None,
    version: str | None = None,
    create_namespace: bool = False,
    repo: str | None = None,
    wait: bool = False,
    timeout: str | None = None,
) -> dict:
    """Install a chart as a new release.

    `chart` may be a chart reference (`repo/chart`), a local path, or a URL.
    If `repo` is set, it is passed as `--repo` (useful when not using a
    pre-added repo alias).
    """
    with _handler_span(
        "install",
        {"helm.release": name, "helm.chart": chart, "helm.namespace": namespace},
    ) as _h:
        try:
            args = ["install", name, chart, "-n", namespace, "-o", "json"]
            if version:
                args += ["--version", version]
            if repo:
                args += ["--repo", repo]
            if create_namespace:
                args.append("--create-namespace")
            if wait:
                args.append("--wait")
            if timeout:
                args += ["--timeout", timeout]

            vf = _write_values(values)
            try:
                if vf:
                    args += ["-f", str(vf)]
                return _helm(args, parse_json=True) or {}
            finally:
                if vf:
                    vf.unlink(missing_ok=True)
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def upgrade(
    name: str,
    chart: str,
    namespace: str,
    values: dict | None = None,
    version: str | None = None,
    install_if_missing: bool = False,
    repo: str | None = None,
    wait: bool = False,
    timeout: str | None = None,
    reset_values: bool = False,
    reuse_values: bool = False,
) -> dict:
    """Upgrade an existing release."""
    with _handler_span(
        "upgrade",
        {"helm.release": name, "helm.chart": chart, "helm.namespace": namespace},
    ) as _h:
        try:
            args = ["upgrade", name, chart, "-n", namespace, "-o", "json"]
            if install_if_missing:
                args.append("--install")
            if version:
                args += ["--version", version]
            if repo:
                args += ["--repo", repo]
            if wait:
                args.append("--wait")
            if timeout:
                args += ["--timeout", timeout]
            if reset_values:
                args.append("--reset-values")
            if reuse_values:
                args.append("--reuse-values")

            vf = _write_values(values)
            try:
                if vf:
                    args += ["-f", str(vf)]
                return _helm(args, parse_json=True) or {}
            finally:
                if vf:
                    vf.unlink(missing_ok=True)
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def rollback(name: str, namespace: str, revision: int, wait: bool = False) -> str:
    """Roll a release back to a prior revision.

    Helm's `rollback` does not support `-o json`; the raw CLI output is
    returned.
    """
    with _handler_span(
        "rollback",
        {"helm.release": name, "helm.namespace": namespace, "helm.revision": revision},
    ) as _h:
        try:
            args = ["rollback", name, str(revision), "-n", namespace]
            if wait:
                args.append("--wait")
            return _helm(args)
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def uninstall(name: str, namespace: str, keep_history: bool = False) -> dict:
    """Uninstall a release."""
    with _handler_span(
        "uninstall",
        {"helm.release": name, "helm.namespace": namespace},
    ) as _h:
        try:
            args = ["uninstall", name, "-n", namespace]
            if keep_history:
                args.append("--keep-history")
            out = _helm(args)
            return {"name": name, "namespace": namespace, "output": out.strip()}
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def repo_add(name: str, url: str) -> str:
    """Add a chart repository."""
    with _handler_span("repo_add", {"helm.repo": name}) as _h:
        try:
            return _helm(["repo", "add", name, url])
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def repo_update() -> str:
    """Update local chart repo indexes."""
    with _handler_span("repo_update") as _h:
        try:
            return _helm(["repo", "update"])
        except Exception as exc:
            set_span_error(_h, exc)
            raise


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    # Initialise OTel up-front; no-op unless OTEL_ENABLED is truthy (#637).
    init_otel_if_enabled(
        service_name=os.environ.get("OTEL_SERVICE_NAME") or "mcp-helm",
    )

    # Dedicated Prometheus metrics listener on :METRICS_PORT (default 9000)
    # separate from the streamable-http MCP port (#643, #650). See
    # tools/kubernetes/server.py for the rationale.
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
                logger=logging.getLogger("mcp-helm.metrics"),
            )
        except Exception as _e:  # pragma: no cover - defensive
            logging.getLogger(__name__).warning(
                "metrics listener failed to start — continuing without it: %r", _e
            )

    # Streamable-HTTP transport so the container is reachable across pod
    # boundaries (#644). stdio mode (FastMCP's default) assumes a local
    # fork/exec client and can't be consumed from a separate pod's
    # backend container via `.claude/mcp.json` URL references.
    mcp.run(
        transport="streamable-http",
        host="0.0.0.0",
        port=int(os.environ.get("MCP_PORT", "8000")),
    )

"""Helm MCP tool server.

Shells out to the `helm` CLI. Helm has no REST API and no Python SDK — the
only first-class programmatic surface is the Go SDK, so every Python wrapper
in the ecosystem ultimately calls `helm` as a subprocess. We do the same,
directly.

Runs against the cluster where this container is deployed. Helm picks up the
ServiceAccount token and API server via the standard in-cluster env vars; no
kubeconfig handling is done here.
"""

from __future__ import annotations

import json
import logging
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Any

import yaml
from mcp.server.fastmcp import FastMCP

log = logging.getLogger("tools.helm")

mcp = FastMCP("helm")


class HelmError(RuntimeError):
    """Raised when a helm CLI invocation fails."""


def _helm(args: list[str], parse_json: bool = False) -> Any:
    cmd = ["helm", *args]
    log.debug("exec: %s", " ".join(cmd))
    proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        raise HelmError(
            f"helm {' '.join(args)} exited {proc.returncode}: "
            f"{(proc.stderr or proc.stdout).strip()}"
        )
    if parse_json:
        out = proc.stdout.strip()
        return json.loads(out) if out else None
    return proc.stdout


def _write_values(values: dict | None) -> Path | None:
    if not values:
        return None
    fd, path = tempfile.mkstemp(suffix=".yaml", prefix="helm-values-")
    with os.fdopen(fd, "w") as f:
        yaml.safe_dump(values, f)
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
    return _helm(["list", "-o", "json", *_ns_args(namespace, all_namespaces)], parse_json=True) or []


@mcp.tool()
def get_release(name: str, namespace: str) -> dict:
    """Return metadata + values + manifest for a release."""
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


@mcp.tool()
def get_values(name: str, namespace: str, all_values: bool = False) -> dict:
    """Return user-supplied values (or all computed values) for a release."""
    args = ["get", "values", name, "-n", namespace, "-o", "json"]
    if all_values:
        args.append("-a")
    return _helm(args, parse_json=True) or {}


@mcp.tool()
def get_manifest(name: str, namespace: str) -> str:
    """Return the rendered manifest for a release."""
    return _helm(["get", "manifest", name, "-n", namespace])


@mcp.tool()
def history(name: str, namespace: str, max_revisions: int = 10) -> list[dict]:
    """Return revision history for a release."""
    return _helm(
        ["history", name, "-n", namespace, "--max", str(max_revisions), "-o", "json"],
        parse_json=True,
    ) or []


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


@mcp.tool()
def rollback(name: str, namespace: str, revision: int, wait: bool = False) -> str:
    """Roll a release back to a prior revision.

    Helm's `rollback` does not support `-o json`; the raw CLI output is
    returned.
    """
    args = ["rollback", name, str(revision), "-n", namespace]
    if wait:
        args.append("--wait")
    return _helm(args)


@mcp.tool()
def uninstall(name: str, namespace: str, keep_history: bool = False) -> dict:
    """Uninstall a release."""
    args = ["uninstall", name, "-n", namespace]
    if keep_history:
        args.append("--keep-history")
    out = _helm(args)
    return {"name": name, "namespace": namespace, "output": out.strip()}


@mcp.tool()
def repo_add(name: str, url: str) -> str:
    """Add a chart repository."""
    return _helm(["repo", "add", name, url])


@mcp.tool()
def repo_update() -> str:
    """Update local chart repo indexes."""
    return _helm(["repo", "update"])


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    mcp.run()

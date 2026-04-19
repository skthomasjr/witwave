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

import contextlib
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

try:
    from mcp_metrics import record_tool_call  # type: ignore
except Exception:  # pragma: no cover - defensive fallback
    from contextlib import contextmanager as _cm

    @_cm  # type: ignore
    def record_tool_call(*_a: Any, **_kw: Any):
        yield None

log = logging.getLogger("tools.helm")

mcp = FastMCP("helm")


class HelmError(RuntimeError):
    """Raised when a helm CLI invocation fails."""


# Process-level timeout for `helm` subprocess invocations (#857). Without this,
# a hung CLI (remote registry stall, stuck `--wait`, unreachable repo index)
# pins the FastMCP handler task until the pod is killed, leaking the coroutine
# and quietly dropping the client request. Default 300s is long enough for
# normal install/upgrade --wait paths on a healthy cluster and short enough
# that a wedged subprocess surfaces to operators.
_HELM_SUBPROCESS_TIMEOUT_SECONDS = float(
    os.environ.get("HELM_SUBPROCESS_TIMEOUT_SECONDS", "300")
)

# Prometheus counter for process-level timeouts (#857). Guarded so the server
# still runs on machines without prometheus_client installed.
try:
    import prometheus_client as _prom

    mcp_subprocess_timeouts_total = _prom.Counter(
        "mcp_subprocess_timeouts_total",
        "Total helm CLI subprocess invocations killed because they "
        "exceeded HELM_SUBPROCESS_TIMEOUT_SECONDS (#857).",
        ["tool", "command"],
    )
except Exception:  # pragma: no cover - metrics disabled
    mcp_subprocess_timeouts_total = None  # type: ignore


def _helm(
    args: list[str],
    parse_json: bool = False,
    stdin: str | None = None,
) -> Any:
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
            proc = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                check=False,
                timeout=_HELM_SUBPROCESS_TIMEOUT_SECONDS,
                input=stdin,
            )
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
        except subprocess.TimeoutExpired as exc:
            # subprocess.run has already killed the child and reaped it by
            # the time TimeoutExpired reaches us. Surface a HelmError so the
            # outer handler span records the failure uniformly.
            if mcp_subprocess_timeouts_total is not None:
                try:
                    mcp_subprocess_timeouts_total.labels(
                        tool="helm", command=(args[0] if args else ""),
                    ).inc()
                except Exception:
                    pass
            err = HelmError(
                f"helm {' '.join(args)} killed after "
                f"{_HELM_SUBPROCESS_TIMEOUT_SECONDS}s (HELM_SUBPROCESS_TIMEOUT_SECONDS)"
            )
            set_span_error(_exec_span, err)
            raise err from exc
        except HelmError:
            raise
        except Exception as exc:
            set_span_error(_exec_span, exc)
            raise


@contextlib.contextmanager
def _handler_span(tool: str, attributes: dict[str, Any] | None = None):
    """Open the outer ``mcp.handler`` SERVER span for a tool invocation.

    Also records the call against mcp_tool_calls_total /
    mcp_tool_duration_seconds (#851) with outcome=ok|error so
    operators can see per-tool rate and p95 latency alongside traces.
    """
    attrs: dict[str, Any] = {"mcp.server": "helm", "mcp.tool": tool}
    if attributes:
        attrs.update({k: v for k, v in attributes.items() if v is not None})
    with record_tool_call("helm", tool):
        with start_span("mcp.handler", kind=SPAN_KIND_SERVER, attributes=attrs) as span:
            yield span


def _values_to_yaml(values: dict | None) -> str | None:
    """Serialise a values dict for passing to ``helm --values=-`` on stdin.

    Preferred over :func:`_write_values` because it avoids writing secret
    material to the pod filesystem entirely (#1081). Callers should pass
    the returned string as ``stdin=`` to :func:`_helm` together with a
    ``["--values", "-"]`` argument.
    """
    if not values:
        return None
    return yaml.safe_dump(values)


# Tempfile prefix + directory policy for the legacy on-disk fallback
# (#1081). Operators deploying on a tmpfs-backed emptyDir can point
# ``HELM_VALUES_TMPDIR`` at it so the cleartext rendering never touches
# persistent disk; default behaviour preserves the historical /tmp path.
_HELM_VALUES_PREFIX = "helm-values-"
_HELM_VALUES_DIR = os.environ.get("HELM_VALUES_TMPDIR") or None


def _write_values(values: dict | None) -> Path | None:
    if not values:
        return None
    fd, path = tempfile.mkstemp(
        suffix=".yaml", prefix=_HELM_VALUES_PREFIX, dir=_HELM_VALUES_DIR
    )
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


def _sweep_orphan_values_files(max_age_seconds: int = 3600) -> int:
    """Remove stale ``helm-values-*.yaml`` tempfiles older than the cap.

    A coroutine cancellation between ``_write_values`` and the cleanup
    in ``finally`` can orphan the file; while the ``finally`` path is
    usually hit, a hard task cancel or SIGKILL can skip it. Running a
    janitor sweep at module import time keeps the pod filesystem from
    accumulating cleartext value renderings across restarts (#1081).

    Returns the number of files removed. Failures swallowed — this is a
    best-effort cleanup, not a correctness gate.
    """
    import time as _time
    removed = 0
    directory = _HELM_VALUES_DIR or tempfile.gettempdir()
    try:
        entries = os.listdir(directory)
    except OSError:
        return 0
    now = _time.time()
    for entry in entries:
        if not entry.startswith(_HELM_VALUES_PREFIX) or not entry.endswith(".yaml"):
            continue
        full = os.path.join(directory, entry)
        try:
            st = os.stat(full)
        except OSError:
            continue
        if now - st.st_mtime < max_age_seconds:
            continue
        try:
            os.unlink(full)
            removed += 1
        except OSError:
            pass
    if removed:
        log.info("helm values janitor removed %d orphaned tempfile(s)", removed)
    return removed


try:
    _sweep_orphan_values_files()
except Exception:  # pragma: no cover - best effort
    pass


def _reject_flag_like(**named: str | None) -> None:
    """Validate that each positional string argument does not begin with '-'
    so an LLM-supplied value can't inject a helm flag (#693). Empty/None
    values are allowed — caller may opt them out of the check by omitting
    the keyword.
    """
    for label, value in named.items():
        if value is None or value == "":
            continue
        if not isinstance(value, str):
            raise ValueError(
                f"helm: {label!r} must be a string (got {type(value).__name__})"
            )
        if value.startswith("-"):
            raise ValueError(
                f"helm: {label!r} must not start with '-' (got {value!r})"
            )


# Key substrings that mark a values-tree leaf as likely secret material
# (#774). Case-insensitive substring match — conservative and covers the
# common names that flow through Helm values: password, token, secret,
# apiKey, authToken, pullSecret, bearer, private_key, credential, etc.
_SECRET_KEY_HINTS = (
    "password",
    "passwd",
    "secret",
    "token",
    "apikey",
    "api_key",
    "auth",
    "bearer",
    "credential",
    "privatekey",
    "private_key",
    "pullsecret",
    "pull_secret",
    "dockerconfig",
    ".dockerconfigjson",
)
# Keys that contain a hint as a substring but are configuration references
# rather than credential leaves (#920). Suppressing these preserves useful
# diff/values output while keeping actual credential leaves redacted.
_SECRET_KEY_FALSE_POSITIVES = (
    "authmode",        # Helm/Grafana: auth backend mode string
    "authtype",        # generic: type of auth, not a credential
    "authmethod",      # same — method name, not a credential
    "authdomain",
    "authhost",
    "authurl",
    "authendpoint",
    "authissuer",
    "authaudience",
    "authprovider",
    "authclass",
    "secretkeyref",    # k8s: reference to a Secret, not a secret value
    "secretref",       # k8s: reference
    "secretname",      # k8s: name of a Secret, not its contents
    "tokenaudience",   # OIDC audience claim, not a token
    "tokenurl",        # OAuth token-endpoint URL
    "tokenpath",       # Vault-agent config path
    "tokenexpiry",
    "tokenlifetime",
    "tokenissuer",
    "credentialmode",
    "credentialtype",
    "credentialprovider",
)
_REDACTED = "***REDACTED***"


def _looks_like_secret_key(key: str) -> bool:
    k = key.lower()
    # Strip a few word separators so 'secret_key_ref' and 'secretKeyRef' both
    # collapse to 'secretkeyref' for the false-positive check.
    k_flat = k.replace("_", "").replace("-", "").replace(".", "")
    if any(fp in k_flat for fp in _SECRET_KEY_FALSE_POSITIVES):
        return False
    return any(hint in k for hint in _SECRET_KEY_HINTS)


def _redact_values(obj: Any) -> Any:
    """Recursively redact values whose keys match _SECRET_KEY_HINTS (#774).

    When a secret-named key holds a scalar (``password: hunter2``), the
    value is replaced with ``_REDACTED``. When it holds a container
    (``auth: {url, method, password}``), the container is recursed
    rather than wholesale-replaced so benign siblings (``url``,
    ``method``) stay visible to the LLM — prior behaviour forced
    operators into ``redact=False`` to see config, which is strictly
    worse security (#1033). Only scalar leaves directly under a
    secret-named parent key are masked.

    Leaves non-matching keys untouched. Lists/tuples are recursed into.
    The returned tree is a fresh structure — original input is not
    mutated.
    """
    if isinstance(obj, dict):
        out: dict[str, Any] = {}
        for k, v in obj.items():
            if isinstance(k, str) and _looks_like_secret_key(k):
                if isinstance(v, (dict, list, tuple)):
                    # Recurse so nested non-secret keys survive (#1033).
                    out[k] = _redact_values(v)
                else:
                    out[k] = _REDACTED
            else:
                out[k] = _redact_values(v)
        return out
    if isinstance(obj, list):
        return [_redact_values(v) for v in obj]
    if isinstance(obj, tuple):
        return tuple(_redact_values(v) for v in obj)
    return obj


def _redact_diff(diff_text: str) -> str:
    """Redact Secret data/stringData values inside a helm-diff output (#915).

    helm-diff emits unified diffs of rendered manifests; when a Secret's
    data/stringData is added/changed/removed, the before/after values
    appear inline (lines prefixed with ``+``/``-``). Parsing diff hunks
    back into YAML is unreliable, so we scan line-by-line with a small
    state machine: once we see a ``kind: Secret`` line we enter a
    'secret block' until the next ``kind:`` header or a blank document
    separator; while inside, we redact the *value* portion of
    ``data:``/``stringData:`` leaf lines and any indented leaf under
    those maps.

    Trades precision for safety: false-positives merely hide a legitimate
    change; false-negatives leak credentials. Keeping this here rather
    than in _redact_manifest because the input shape is textual diff,
    not a parseable manifest.
    """
    out_lines: list[str] = []
    in_secret = False
    in_data_map = False
    for line in diff_text.splitlines():
        # Explicitly skip unified-diff file-header + hunk-header lines
        # before any prefix-stripping so they never participate in state
        # transitions (#1078). These markers appear at column 0 with no
        # context prefix and cannot legitimately be YAML doc separators,
        # ``kind:`` headers, or ``data:`` leaves.
        if (
            line.startswith("--- ")
            or line.startswith("+++ ")
            or line.startswith("@@ ")
            or line.startswith("diff --git ")
            or line.startswith("index ")
        ):
            out_lines.append(line)
            continue

        # Strip the leading diff prefix for content inspection but keep
        # it for the emitted line.
        content = line
        prefix = ""
        if line[:1] in ("+", "-", " "):
            prefix = line[:1]
            content = line[1:]

        stripped = content.strip()

        # Reset state only on a standalone YAML doc separator.
        # Previously the check was ``stripped.startswith("---")`` which
        # also matched unified-diff file-headers (``--- a/…``, ``+++ b/…``)
        # and multi-line PEM bodies (``-----BEGIN CERTIFICATE-----``)
        # inside stringData, resetting the state machine mid-Secret and
        # leaking subsequent data leaf lines (#1028). ``+++ `` and
        # ``@@ `` hunk headers are not doc separators either — those
        # are now filtered above (#1078) before we get here.
        if stripped == "---":
            in_secret = False
            in_data_map = False
            out_lines.append(line)
            continue
        if stripped.startswith("kind:"):
            _kind_val = stripped.split(":", 1)[1].strip()
            in_secret = _kind_val == "Secret"
            in_data_map = False
            out_lines.append(line)
            continue

        if not in_secret:
            out_lines.append(line)
            continue

        # Inside a Secret block. Entering data:/stringData: map?
        if stripped in ("data:", "stringData:"):
            in_data_map = True
            out_lines.append(line)
            continue

        # Leaf inside data: map — indented key: value under data/stringData.
        if in_data_map and ":" in stripped and content.startswith(("  ", "\t")):
            indent = len(content) - len(content.lstrip())
            # Still inside the data/stringData map while indent > 0.
            # in_data_map is only cleared when a new ``kind:`` header or
            # a standalone ``---`` doc separator is seen (#1031) — the
            # previous "un-indented non-blank exits the map" heuristic
            # was load-bearing for blank-line safety but also caused
            # false exits on non-data lines inside the same Secret, and
            # blank lines left the flag asserted anyway. Scoping the
            # reset to doc/kind boundaries keeps the machine simple and
            # predictable.
            key, _, _value = content[indent:].partition(":")
            out_lines.append(f"{prefix}{' ' * indent}{key}: {_REDACTED}")
            continue

        out_lines.append(line)
    return "\n".join(out_lines)


def _redact_manifest(manifest: str) -> str:
    """Redact Secret data/stringData payloads inside a rendered manifest (#774).

    Parses each YAML doc; when kind == Secret, replaces data/stringData
    values with ``_REDACTED``. Non-Secret docs pass through unchanged.
    On YAML parse failure, returns a redacted placeholder — previously
    the raw manifest was returned "for visibility", which leaked Secret
    contents whenever helm emitted a malformed template (trailing tab,
    CRLF in a block scalar, pre-substitution helm partials, etc.)
    (#918). Operators still see the failure mode in the placeholder
    without the Secret payload.
    """
    try:
        docs = list(yaml.safe_load_all(manifest))
    except Exception as _parse_exc:
        return (
            "# manifest redacted: failed to parse as YAML "
            f"({type(_parse_exc).__name__}) — raw output suppressed to "
            "avoid leaking Secret contents (#918).\n"
        )
    out_docs: list[Any] = []
    for doc in docs:
        if isinstance(doc, dict) and doc.get("kind") == "Secret":
            for field in ("data", "stringData"):
                payload = doc.get(field)
                if isinstance(payload, dict):
                    doc[field] = {k: _REDACTED for k in payload}
        out_docs.append(doc)
    # safe_dump_all preserves doc separators.
    return yaml.safe_dump_all(out_docs, default_flow_style=False, sort_keys=False)


def _ns_args(namespace: str | None, all_namespaces: bool = False) -> list[str]:
    if all_namespaces:
        return ["-A"]
    if namespace:
        return ["-n", namespace]
    return []


@mcp.tool()
def list_releases(namespace: str | None = None, all_namespaces: bool = False) -> list[dict]:
    """List Helm releases."""
    # Centralised flag-injection guard (#772) — every tool validates any
    # string that flows into argv through _reject_flag_like, including this
    # one. list_releases used to skip the check because namespace is
    # Optional; the guard already tolerates None/"" so it's safe to call
    # unconditionally.
    _reject_flag_like(namespace=namespace)
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


def _get_values_impl(
    name: str,
    namespace: str,
    all_values: bool = False,
    redact: bool = True,
) -> dict:
    """Pure implementation for get_values — no handler span, no metrics.

    Extracted so `get_release` can compose these helpers without
    re-entering the MCP tool instrumentation surface (nested SERVER
    spans + double-counted `mcp_tool_calls_total`). See #1030.
    """
    _reject_flag_like(name=name, namespace=namespace)
    args = ["get", "values", name, "-n", namespace, "-o", "json"]
    if all_values:
        args.append("-a")
    values = _helm(args, parse_json=True) or {}
    if redact:
        values = _redact_values(values)
    return values


def _get_manifest_impl(name: str, namespace: str, redact: bool = True) -> str:
    """Pure implementation for get_manifest (#1030)."""
    _reject_flag_like(name=name, namespace=namespace)
    manifest = _helm(["get", "manifest", name, "-n", namespace])
    if redact:
        manifest = _redact_manifest(manifest)
    return manifest


def _history_impl(name: str, namespace: str, max_revisions: int = 10) -> list[dict]:
    """Pure implementation for history (#1030)."""
    _reject_flag_like(name=name, namespace=namespace)
    if not isinstance(max_revisions, int) or isinstance(max_revisions, bool):
        raise ValueError("helm: 'max_revisions' must be an int")
    # Reject negative values so str(max_revisions) can't produce a
    # leading "-" that helm would interpret as a flag (#772).
    if max_revisions < 1:
        raise ValueError("helm: 'max_revisions' must be >= 1")
    return _helm(
        ["history", name, "-n", namespace, "--max", str(max_revisions), "-o", "json"],
        parse_json=True,
    ) or []


@mcp.tool()
def get_release(name: str, namespace: str) -> dict:
    """Return metadata + values + manifest for a release."""
    # Validate up front even though the inner helpers also re-check.
    # Keeps the central guard pattern uniform across every tool entry
    # point (#772).
    _reject_flag_like(name=name, namespace=namespace)
    with _handler_span("get_release", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            # Call the private impls rather than the @mcp.tool-decorated
            # wrappers so we don't open nested SERVER spans or count
            # each sub-call as a separate `mcp_tool_calls_total`
            # increment (#1030).
            values = _get_values_impl(name=name, namespace=namespace, all_values=True)
            manifest = _get_manifest_impl(name=name, namespace=namespace)
            hist = _history_impl(name=name, namespace=namespace, max_revisions=1)
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
def get_values(
    name: str,
    namespace: str,
    all_values: bool = False,
    redact: bool = True,
) -> dict:
    """Return user-supplied values (or all computed values) for a release.

    Secret-looking leaves (password/token/apiKey/auth/credential/…) are
    redacted by default (#774) to stop secret material flowing into
    backend conversation.jsonl, memory, and OTel spans. Pass
    ``redact=False`` to opt out when the caller genuinely needs the raw
    values (e.g. a credential-rotation workflow); the opt-out must be
    explicit.
    """
    with _handler_span(
        "get_values",
        {"helm.release": name, "helm.namespace": namespace, "helm.redacted": redact},
    ) as _h:
        try:
            return _get_values_impl(
                name=name, namespace=namespace, all_values=all_values, redact=redact
            )
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def get_manifest(name: str, namespace: str, redact: bool = True) -> str:
    """Return the rendered manifest for a release.

    Secret resources' data/stringData are redacted by default (#774). Pass
    ``redact=False`` to retrieve the raw manifest when you explicitly need
    the secret payload (credential-rotation, debugging apiserver decode).
    """
    with _handler_span(
        "get_manifest",
        {"helm.release": name, "helm.namespace": namespace, "helm.redacted": redact},
    ) as _h:
        try:
            return _get_manifest_impl(name=name, namespace=namespace, redact=redact)
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def history(name: str, namespace: str, max_revisions: int = 10) -> list[dict]:
    """Return revision history for a release."""
    with _handler_span("history", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            return _history_impl(
                name=name, namespace=namespace, max_revisions=max_revisions
            )
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
    dry_run: bool = False,
) -> dict:
    """Install a chart as a new release.

    `chart` may be a chart reference (`repo/chart`), a local path, or a URL.
    If `repo` is set, it is passed as `--repo` (useful when not using a
    pre-added repo alias).

    Set ``dry_run=True`` to preview the install without touching the
    cluster — helm's client-side dry-run renders templates and returns
    the same JSON shape as a real install with empty runtime status.
    Useful for LLM-authored requests the operator wants to confirm
    before committing (#854).
    """
    _reject_flag_like(
        name=name,
        chart=chart,
        namespace=namespace,
        version=version,
        repo=repo,
        timeout=timeout,
    )
    with _handler_span(
        "install",
        {"helm.release": name, "helm.chart": chart, "helm.namespace": namespace,
         "helm.dry_run": dry_run},
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
            if dry_run:
                args.append("--dry-run")

            # Prefer stdin delivery of values so secret material never
            # touches the pod filesystem (#1081). Falls back to _helm's
            # stdin= kwarg when ``values`` is provided.
            values_yaml = _values_to_yaml(values)
            if values_yaml is not None:
                args += ["--values", "-"]
            return _helm(args, parse_json=True, stdin=values_yaml) or {}
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
    dry_run: bool = False,
) -> dict:
    """Upgrade an existing release.

    Set ``dry_run=True`` to preview the upgrade without touching the
    cluster (#854). Pair with :func:`diff` for a side-by-side view of
    rendered manifest changes before committing.
    """
    _reject_flag_like(
        name=name,
        chart=chart,
        namespace=namespace,
        version=version,
        repo=repo,
        timeout=timeout,
    )
    with _handler_span(
        "upgrade",
        {"helm.release": name, "helm.chart": chart, "helm.namespace": namespace,
         "helm.dry_run": dry_run},
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
            if dry_run:
                args.append("--dry-run")

            # Prefer stdin delivery of values so secret material never
            # touches the pod filesystem (#1081).
            values_yaml = _values_to_yaml(values)
            if values_yaml is not None:
                args += ["--values", "-"]
            return _helm(args, parse_json=True, stdin=values_yaml) or {}
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def diff(
    name: str,
    chart: str,
    namespace: str,
    values: dict | None = None,
    version: str | None = None,
    repo: str | None = None,
    context: int = 3,
    redact: bool = True,
) -> str:
    """Show a unified diff of what ``helm upgrade`` WOULD change (#854).

    Requires the `helm-diff` plugin to be installed in the tool image
    (`helm plugin install https://github.com/databus23/helm-diff`).
    When the plugin is absent, ``diff`` does NOT return a text message
    — it raises ``HelmError`` from the wrapped ``helm diff upgrade``
    invocation, mirroring every other helm CLI failure surface
    (#922 corrected the previous docstring which claimed the error
    was returned inline).

    When ``redact=True`` (the default), the output is passed through a
    Secret-aware diff redactor (#915) — Secret ``data``/``stringData``
    values are replaced with ``_REDACTED`` so the `diff() then upgrade()`
    workflow does not leak Secret payloads into conversation.jsonl,
    memory, or OTel spans. Pass ``redact=False`` only for short-lived
    operator-side tooling where the caller accepts the exposure.

    Returns the text diff from ``helm diff upgrade`` — consumers should
    treat an empty string as "no changes". Non-zero exit codes from
    the wrapped CLI bubble up as ``HelmError``.
    """
    _reject_flag_like(
        name=name, chart=chart, namespace=namespace, version=version, repo=repo
    )
    if not isinstance(context, int) or context < 0:
        raise ValueError("helm: 'context' must be a non-negative int")
    with _handler_span(
        "diff",
        {"helm.release": name, "helm.chart": chart, "helm.namespace": namespace},
    ) as _h:
        try:
            args = ["diff", "upgrade", name, chart, "-n", namespace,
                    "--context", str(context)]
            if version:
                args += ["--version", version]
            if repo:
                args += ["--repo", repo]
            # Prefer stdin delivery of values so secret material never
            # touches the pod filesystem (#1081).
            values_yaml = _values_to_yaml(values)
            if values_yaml is not None:
                args += ["--values", "-"]
            raw = _helm(args, stdin=values_yaml) or ""
            if redact and raw:
                try:
                    return _redact_diff(raw)
                except Exception as _redact_exc:
                    # Fail-closed: if redaction itself blows up, return
                    # a placeholder rather than leak the raw diff (#915).
                    log.warning(
                        "helm diff redaction failed (%s); suppressing output.",
                        _redact_exc,
                    )
                    return (
                        "# diff redacted: Secret-aware redactor raised "
                        f"{type(_redact_exc).__name__} — output "
                        "suppressed to avoid leaking Secret contents (#915).\n"
                    )
            return raw
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def rollback(name: str, namespace: str, revision: int, wait: bool = False) -> str:
    """Roll a release back to a prior revision.

    Helm's `rollback` does not support `-o json`; the raw CLI output is
    returned.
    """
    _reject_flag_like(name=name, namespace=namespace)
    # Type-validate revision as int so an LLM-supplied "-1"-style string
    # cannot flow into argv as a flag (#693).
    if not isinstance(revision, int) or isinstance(revision, bool):
        raise ValueError("helm: 'revision' must be an int")
    if revision < 0:
        raise ValueError("helm: 'revision' must be >= 0")
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
def uninstall(
    name: str,
    namespace: str,
    keep_history: bool = False,
    dry_run: bool = False,
) -> dict:
    """Uninstall a release.

    Set ``dry_run=True`` to preview which resources would be removed
    without actually deleting them (#854).
    """
    _reject_flag_like(name=name, namespace=namespace)
    with _handler_span(
        "uninstall",
        {"helm.release": name, "helm.namespace": namespace, "helm.dry_run": dry_run},
    ) as _h:
        try:
            args = ["uninstall", name, "-n", namespace]
            if keep_history:
                args.append("--keep-history")
            if dry_run:
                args.append("--dry-run")
            out = _helm(args)
            return {"name": name, "namespace": namespace, "output": out.strip()}
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def repo_add(name: str, url: str) -> str:
    """Add a chart repository."""
    _reject_flag_like(name=name, url=url)
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


def _get_info_doc() -> dict[str, Any]:
    """Build the /info document for the helm tool server (#1122).

    Light-weight — probes image env vars, shells out to ``helm version``
    once (cached via subprocess capture), detects the helm-diff plugin
    presence, and enumerates registered tools. Intentionally skips
    anything that could leak cluster state.
    """
    image_version = (
        os.environ.get("IMAGE_VERSION")
        or os.environ.get("IMAGE_TAG")
        or os.environ.get("VERSION")
        or "unknown"
    )
    helm_version: Any = "unknown"
    try:
        proc = subprocess.run(
            ["helm", "version", "--short"],
            capture_output=True, text=True, check=False, timeout=5,
        )
        if proc.returncode == 0:
            helm_version = proc.stdout.strip()
    except Exception:
        helm_version = "unavailable"

    helm_diff_present = False
    try:
        proc = subprocess.run(
            ["helm", "plugin", "list"],
            capture_output=True, text=True, check=False, timeout=5,
        )
        if proc.returncode == 0:
            helm_diff_present = any(
                line.split()[:1] == ["diff"] for line in proc.stdout.splitlines()
            )
    except Exception:
        helm_diff_present = False

    read_only = os.environ.get("MCP_READ_ONLY", "").strip().lower() in {
        "1", "true", "yes", "on",
    }

    # Enumerate tool handlers registered via @mcp.tool(). FastMCP stores
    # them on the internal tool manager; fall back to a static list
    # lookup if the attribute shape changes.
    try:
        tool_names = sorted(mcp._tool_manager._tools.keys())  # type: ignore[attr-defined]
    except Exception:
        tool_names = []

    return {
        "server": "mcp-helm",
        "image_version": image_version,
        "helm_version": helm_version,
        "helm_diff_present": helm_diff_present,
        "features": {
            "read_only": read_only,
            "otel": bool(os.environ.get("OTEL_ENABLED")),
            "metrics": bool(os.environ.get("METRICS_ENABLED")),
            "values_stdin": True,  # #1081
        },
        "tools": tool_names,
    }


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
    #
    # Bearer-token gate (#771): wrap the FastMCP Starlette app with
    # shared mcp_auth middleware so an MCP_TOOL_AUTH_TOKEN can gate
    # invocation. Falls back to mcp.run() when uvicorn/mcp_auth are
    # unavailable so a bare dev checkout still works.
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

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
import re
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

try:
    from mcp_audit import audit as _audit  # type: ignore
except Exception:  # pragma: no cover - defensive fallback
    def _audit(*_a: Any, **_kw: Any) -> None:  # type: ignore
        return None

log = logging.getLogger("tools.helm")

mcp = FastMCP("helm")


# Read-only / maintenance-mode gate (#1123). Mutating tools check this
# at the top of each handler and raise a HelmError with a clear message
# so the operator-visible reason is "read-only mode", not a confusing
# CLI exit. Evaluated per-call so toggling the env var on a running
# pod takes effect without restart.
_READ_ONLY_ENV_VARS = {"MCP_READ_ONLY", "MCP_HELM_READ_ONLY"}


def _is_read_only() -> bool:
    for name in _READ_ONLY_ENV_VARS:
        if os.environ.get(name, "").strip().lower() in {"1", "true", "yes", "on"}:
            return True
    return False


def _refuse_if_read_only(tool: str) -> None:
    if _is_read_only():
        raise HelmError(
            f"helm {tool}: refused because MCP_READ_ONLY is set (#1123). "
            "Unset the env var or restart the pod without it to allow mutations."
        )


class HelmError(RuntimeError):
    """Raised when a helm CLI invocation fails."""


# Inline credential redaction for HelmError messages (#1271). The shared
# `shared/redact.py` module is not copied into this tool container (see
# Dockerfile), so we duplicate the minimum-viable subset here: mask any
# `--set KEY=VALUE` / `--set-string KEY=VALUE` / `--set-file KEY=VALUE` /
# `--set-json KEY=VALUE` occurrences (the common vectors for leaking
# credentials passed as chart values) plus a small set of well-anchored
# secret shapes that helm itself or child processes might echo into
# stderr on a failed install/upgrade (bearer tokens, AWS keys, basic-auth
# URLs). The goal is defence-in-depth for error messages, not a full DLP
# pipeline — keep the pattern list short and well-anchored.
_REDACTED = "[REDACTED]"

_HELM_SET_FLAGS = ("--set", "--set-string", "--set-file", "--set-json")

# `--set foo.bar=secret` (space-separated)
_HELM_SET_SPACE_RE = re.compile(
    r"(--set(?:-string|-file|-json)?)(\s+)([^\s=]+)=([^\s]+)"
)
# `--set=foo.bar=secret` (equals-joined)
_HELM_SET_EQUALS_RE = re.compile(
    r"(--set(?:-string|-file|-json)?)=([^\s=]+)=([^\s]+)"
)
# Authorization: Bearer <token>
_BEARER_RE = re.compile(r"(?i)(bearer\s+)[A-Za-z0-9._\-]+")
# AWS access key IDs
_AWS_AKID_RE = re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")
# basic-auth credentials inside URLs: scheme://user:pass@host
_BASIC_AUTH_URL_RE = re.compile(r"([a-zA-Z][a-zA-Z0-9+.\-]*://)[^\s:/@]+:[^\s@]+@")


def _redact_helm_error_text(text: str, argv: list[str] | None = None) -> str:
    """Mask credentials likely to appear in helm stderr/stdout (#1271).

    Covers the `--set*=KEY=VALUE` argv echo path (the primary leak
    vector — helm often reprints the offending flag on error) plus a
    handful of secret shapes that can appear in transitive errors from
    chart hooks, registries, or cloud SDK init. ``argv`` is the
    original subprocess argv; when provided, any literal value that
    appeared in a `--set*` flag is masked wherever it reappears in the
    output (defensive against helm rewriting the flag before printing).
    """
    if not text:
        return text
    out = text
    out = _HELM_SET_SPACE_RE.sub(
        lambda m: f"{m.group(1)}{m.group(2)}{m.group(3)}={_REDACTED}", out
    )
    out = _HELM_SET_EQUALS_RE.sub(
        lambda m: f"{m.group(1)}={m.group(2)}={_REDACTED}", out
    )
    out = _BEARER_RE.sub(lambda m: f"{m.group(1)}{_REDACTED}", out)
    out = _AWS_AKID_RE.sub(_REDACTED, out)
    out = _BASIC_AUTH_URL_RE.sub(lambda m: f"{m.group(1)}{_REDACTED}@", out)

    if argv:
        # Walk argv and mask any value that came in via --set*. This
        # catches cases where stderr quotes the raw value without the
        # preceding flag (e.g. "error: invalid value 'hunter2'").
        i = 0
        while i < len(argv):
            tok = argv[i]
            value: str | None = None
            if tok in _HELM_SET_FLAGS and i + 1 < len(argv):
                kv = argv[i + 1]
                if "=" in kv:
                    value = kv.split("=", 1)[1]
                i += 2
                continue
            for flag in _HELM_SET_FLAGS:
                prefix = flag + "="
                if tok.startswith(prefix):
                    rest = tok[len(prefix):]
                    if "=" in rest:
                        value = rest.split("=", 1)[1]
                    break
            if value:
                # Only substitute non-trivial values to avoid masking
                # harmless short tokens that collide with real output.
                if len(value) >= 4:
                    out = out.replace(value, _REDACTED)
            i += 1
    return out


# Process-level timeout for `helm` subprocess invocations (#857, #778).
# Without this, a hung CLI (remote registry stall, stuck `--wait`,
# unreachable repo index) pins the FastMCP handler task until the pod is
# killed, leaking the coroutine and quietly dropping the client request.
# Default 300s is long enough for normal install/upgrade --wait paths on a
# healthy cluster and short enough that a wedged subprocess surfaces to
# operators.
#
# MCP_SUBPROCESS_TIMEOUT_SEC (#778) is the cross-tool knob; the legacy
# HELM_SUBPROCESS_TIMEOUT_SECONDS env is preserved for back-compat.
# Precedence: HELM_SUBPROCESS_TIMEOUT_SECONDS > MCP_SUBPROCESS_TIMEOUT_SEC
# > 300s default.
_HELM_SUBPROCESS_TIMEOUT_SECONDS = float(
    os.environ.get("HELM_SUBPROCESS_TIMEOUT_SECONDS")
    or os.environ.get("MCP_SUBPROCESS_TIMEOUT_SEC")
    or "300"
)

# Per-response byte cap on tool output (#778). Defends against a stuck
# `--wait`, an accidental full-history fetch, or a malicious chart that
# renders an enormous manifest: every string/JSON payload returned by
# a query tool is truncated to this many bytes before being handed
# back to the MCP client. 0 or negative disables the cap.
_MCP_RESPONSE_MAX_BYTES = int(
    os.environ.get("MCP_RESPONSE_MAX_BYTES") or str(8 * 1024 * 1024)
)


def _truncate_text(value: str, *, tool: str) -> str:
    """Cap ``value`` to MCP_RESPONSE_MAX_BYTES (UTF-8) with a visible marker.

    Returns the original string unchanged when the cap is disabled
    (``<= 0``) or the payload already fits. Otherwise returns a
    truncated body followed by a human-readable notice so the caller
    sees that truncation happened rather than silently receiving a
    partial response.
    """
    if not isinstance(value, str):
        return value
    cap = _MCP_RESPONSE_MAX_BYTES
    if cap <= 0:
        return value
    encoded = value.encode("utf-8", errors="replace")
    if len(encoded) <= cap:
        return value
    head = encoded[:cap].decode("utf-8", errors="replace")
    suffix = (
        f"\n\n# [mcp-helm:{tool}] response truncated: original payload "
        f"{len(encoded)} bytes exceeded MCP_RESPONSE_MAX_BYTES={cap}."
    )
    return head + suffix


def _truncate_json(value: Any, *, tool: str) -> Any:
    """Size-cap a JSON-able payload (#778).

    Serialises ``value`` to JSON to measure byte-length; when over the
    cap, wraps the truncated head in a dict that preserves structure
    so the caller can still parse the response. For list payloads we
    drop trailing items until the re-serialised length fits, which is
    the common shape for list_releases / history responses.
    """
    cap = _MCP_RESPONSE_MAX_BYTES
    if cap <= 0 or value is None:
        return value
    try:
        raw = json.dumps(value)
    except Exception:
        return value
    if len(raw.encode("utf-8", errors="replace")) <= cap:
        return value
    if isinstance(value, list):
        trimmed: list[Any] = []
        running = 2  # '[]'
        for item in value:
            try:
                chunk = json.dumps(item)
            except Exception:
                chunk = "null"
            if running + len(chunk) + 1 > cap:
                break
            trimmed.append(item)
            running += len(chunk) + 1
        return {
            "_truncated": True,
            "_original_length": len(value),
            "_returned_length": len(trimmed),
            "_cap_bytes": cap,
            "items": trimmed,
        }
    # Dict / scalar — return a placeholder rather than guess which keys
    # to drop; callers with large single-object responses can opt into
    # a higher MCP_RESPONSE_MAX_BYTES.
    return {
        "_truncated": True,
        "_cap_bytes": cap,
        "_note": (
            f"mcp-helm:{tool} response exceeded MCP_RESPONSE_MAX_BYTES "
            f"({cap}); raw payload suppressed. Raise the cap or narrow "
            "the query to retrieve it."
        ),
    }

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
    # Inner-work histogram for helm-CLI latency (#1126). Distinct from
    # mcp_tool_duration_seconds (the outer handler span) — operators
    # alert on this one to know 'helm subprocess is slow' vs. 'tool is
    # slow for some other reason'. Bucket range covers quick list/get
    # calls through long upgrade --wait runs.
    helm_subprocess_duration_seconds = _prom.Histogram(
        "helm_subprocess_duration_seconds",
        "Wall-clock duration of a helm CLI invocation (subprocess).",
        ["command", "outcome"],
        buckets=(
            0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0, 300.0,
        ),
    )
except Exception:  # pragma: no cover - metrics disabled
    mcp_subprocess_timeouts_total = None  # type: ignore
    helm_subprocess_duration_seconds = None  # type: ignore


def _helm(
    args: list[str],
    parse_json: bool = False,
    stdin: str | None = None,
) -> Any:
    cmd = ["helm", *args]
    log.debug("exec: %s", " ".join(cmd))
    command = args[0] if args else ""
    # helm.exec child span — captures subprocess latency independent of the
    # outer mcp.handler span, so operators can attribute time spent in the
    # CLI vs. in-process work.
    import time as _t  # local import to keep top-of-file clean
    _subp_start = _t.monotonic()
    _subp_outcome = "ok"
    with start_span(
        "helm.exec",
        kind=SPAN_KIND_INTERNAL,
        attributes={"helm.command": command, "helm.args": " ".join(args)},
    ) as _exec_span:
        try:
            # Stream stdout/stderr through a hard byte cap (#1204). The
            # previous `subprocess.run(capture_output=True)` let helm's
            # stdout buffer grow unbounded — a rogue `get manifest` on a
            # huge chart, or a wedged `--wait` that retries log output
            # forever, could pin the pod's memory. Cap at the smaller of
            # MCP_RESPONSE_MAX_BYTES*4 and 32MiB so the subprocess layer
            # is strictly larger than the response cap (leaves headroom
            # for JSON we'll still parse) but still bounded.
            _subp_cap = min(max(_MCP_RESPONSE_MAX_BYTES, 0) * 4, 32 * 1024 * 1024)
            if _subp_cap <= 0:
                _subp_cap = 32 * 1024 * 1024
            proc = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                stdin=subprocess.PIPE if stdin is not None else None,
            )
            import threading as _threading
            _stdout_buf = bytearray()
            _stderr_buf = bytearray()
            _stdout_truncated = [False]
            _stderr_truncated = [False]

            def _drain(stream, buf, trunc_flag):
                try:
                    while True:
                        chunk = stream.read(8192)
                        if not chunk:
                            break
                        remaining = _subp_cap - len(buf)
                        if remaining <= 0:
                            trunc_flag[0] = True
                            break
                        if len(chunk) > remaining:
                            buf.extend(chunk[:remaining])
                            trunc_flag[0] = True
                            break
                        buf.extend(chunk)
                except Exception:
                    pass

            t_out = _threading.Thread(
                target=_drain, args=(proc.stdout, _stdout_buf, _stdout_truncated),
                daemon=True,
            )
            t_err = _threading.Thread(
                target=_drain, args=(proc.stderr, _stderr_buf, _stderr_truncated),
                daemon=True,
            )
            t_out.start()
            t_err.start()

            try:
                if stdin is not None and proc.stdin is not None:
                    try:
                        proc.stdin.write(stdin.encode("utf-8"))
                    finally:
                        try:
                            proc.stdin.close()
                        except Exception:
                            pass
                proc.wait(timeout=_HELM_SUBPROCESS_TIMEOUT_SECONDS)
            finally:
                t_out.join(timeout=1.0)
                t_err.join(timeout=1.0)
                # #1365: close pipe FDs before declaring success so drain
                # threads can't linger on stuck reads past handler exit.
                # Daemon threads get reaped at process exit but hold pipe
                # FDs until then; explicitly close the streams.
                for _stream in (proc.stdout, proc.stderr, proc.stdin):
                    if _stream is not None:
                        try:
                            _stream.close()
                        except Exception:
                            pass
                # Log at WARN when drain threads are still alive after
                # join timeouts so operators can correlate FD counts.
                if t_out.is_alive() or t_err.is_alive():
                    log.warning(
                        "helm subprocess drain threads outlived handler "
                        "(stdout_alive=%s stderr_alive=%s); pipe FDs held "
                        "until process exit. (#1365)",
                        t_out.is_alive(), t_err.is_alive(),
                    )

            # If either stream was truncated, kill the process so we
            # don't leak a still-running helm invocation past the
            # handler return.
            if _stdout_truncated[0] or _stderr_truncated[0]:
                if proc.poll() is None:
                    try:
                        proc.kill()
                    except Exception:
                        pass
                    try:
                        proc.wait(timeout=2.0)
                    except Exception:
                        pass

            stdout_text = _stdout_buf.decode("utf-8", errors="replace")
            stderr_text = _stderr_buf.decode("utf-8", errors="replace")
            truncated = _stdout_truncated[0] or _stderr_truncated[0]
            returncode = proc.returncode if proc.returncode is not None else -1

            if returncode != 0:
                # #1271: redact credentials from helm stderr/stdout before
                # formatting them into the HelmError message. helm echoes
                # `--set key=value` flags in many error paths; without
                # redaction those values land in caller-visible errors and
                # downstream logs.
                safe_args = _redact_helm_error_text(" ".join(args), args)
                safe_body = _redact_helm_error_text(
                    (stderr_text or stdout_text).strip(), args
                )
                err = HelmError(
                    f"helm {safe_args} exited {returncode}: "
                    f"{safe_body}"
                    + (f" (output truncated at {_subp_cap} bytes, #1204)"
                       if truncated else "")
                )
                set_span_error(_exec_span, err)
                _subp_outcome = "error"
                raise err
            if parse_json:
                out = stdout_text.strip()
                if truncated:
                    # Can't trust JSON parse on a truncated stream; return
                    # the marker envelope instead.
                    return {
                        "_truncated": True,
                        "_cap_bytes": _subp_cap,
                        "_note": (
                            f"helm {' '.join(args)} output exceeded "
                            f"{_subp_cap} bytes; subprocess killed (#1204). "
                            "Narrow the query or raise the cap."
                        ),
                    }
                return json.loads(out) if out else None
            if truncated:
                stdout_text += (
                    f"\n\n# [mcp-helm] subprocess output truncated at "
                    f"{_subp_cap} bytes (#1204); process killed."
                )
            return stdout_text
        except subprocess.TimeoutExpired as exc:
            try:
                proc.kill()
                proc.wait(timeout=2.0)
            except Exception:
                pass
            _subp_outcome = "timeout"
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
            # #1271: redact credentials from the echoed argv — timeout
            # messages are surfaced to callers and logs the same way the
            # non-zero-exit path is.
            safe_args = _redact_helm_error_text(" ".join(args), args)
            err = HelmError(
                f"helm {safe_args} killed after "
                f"{_HELM_SUBPROCESS_TIMEOUT_SECONDS}s (HELM_SUBPROCESS_TIMEOUT_SECONDS)"
            )
            set_span_error(_exec_span, err)
            raise err from exc
        except HelmError:
            _subp_outcome = "error"
            raise
        except Exception as exc:
            set_span_error(_exec_span, exc)
            _subp_outcome = "error"
            raise
        finally:
            # Record every CLI invocation into the inner-work histogram
            # (#1126), including timeouts and non-zero exits, so p95/p99
            # is alertable regardless of outcome.
            if helm_subprocess_duration_seconds is not None:
                try:
                    helm_subprocess_duration_seconds.labels(
                        command=command, outcome=_subp_outcome,
                    ).observe(_t.monotonic() - _subp_start)
                except Exception:
                    pass


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

    Also rejects whitespace/control/non-printable characters (#1206):
    newline, carriage return, tab, NUL, and anything ``str.isprintable``
    returns False for. These can smuggle extra argv tokens, corrupt
    audit output, or break CLI parsers in surprising ways.
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
        # #1206: control / whitespace / non-printable characters.
        for _bad in ("\n", "\r", "\t", "\0"):
            if _bad in value:
                raise ValueError(
                    f"helm: {label!r} must not contain control characters "
                    f"(newline/CR/tab/NUL) (got {value!r})"
                )
        if not value.isprintable():
            raise ValueError(
                f"helm: {label!r} must contain only printable characters "
                f"(got {value!r})"
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
    # See #1203 — redact data/stringData on any secret-like kind, not
    # just the core Secret kind. Sealed Secrets, External Secrets, and
    # Vault Secret CRDs all carry credential material in the same
    # field shapes; a helm-rendered chart can emit any of them.
    _SECRET_KINDS = ("Secret", "SealedSecret", "ExternalSecret", "VaultSecret")
    out_docs: list[Any] = []
    for doc in docs:
        if isinstance(doc, dict) and doc.get("kind") in _SECRET_KINDS:
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
            result = _helm(
                ["list", "-o", "json", *_ns_args(namespace, all_namespaces)], parse_json=True
            ) or []
            return _truncate_json(result, tool="list_releases")
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
            payload = {
                "name": name,
                "namespace": namespace,
                "current_revision": current,
                "values": values,
                "manifest": _truncate_text(manifest, tool="get_release"),
            }
            return _truncate_json(payload, tool="get_release")
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
            result = _get_values_impl(
                name=name, namespace=namespace, all_values=all_values, redact=redact
            )
            return _truncate_json(result, tool="get_values")
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
            manifest = _get_manifest_impl(name=name, namespace=namespace, redact=redact)
            return _truncate_text(manifest, tool="get_manifest")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def history(name: str, namespace: str, max_revisions: int = 10) -> list[dict]:
    """Return revision history for a release."""
    with _handler_span("history", {"helm.release": name, "helm.namespace": namespace}) as _h:
        try:
            result = _history_impl(
                name=name, namespace=namespace, max_revisions=max_revisions
            )
            return _truncate_json(result, tool="history")
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
    _refuse_if_read_only("install")
    _reject_flag_like(
        name=name,
        chart=chart,
        namespace=namespace,
        version=version,
        repo=repo,
        timeout=timeout,
    )
    _audit(
        "mcp-helm", "install",
        args={"name": name, "chart": chart, "namespace": namespace,
              "version": version, "repo": repo, "values": values,
              "wait": wait, "timeout": timeout},
        dry_run=dry_run,
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
    _refuse_if_read_only("upgrade")
    _reject_flag_like(
        name=name,
        chart=chart,
        namespace=namespace,
        version=version,
        repo=repo,
        timeout=timeout,
    )
    _audit(
        "mcp-helm", "upgrade",
        args={"name": name, "chart": chart, "namespace": namespace,
              "version": version, "repo": repo, "values": values,
              "install_if_missing": install_if_missing, "wait": wait,
              "timeout": timeout, "reset_values": reset_values,
              "reuse_values": reuse_values},
        dry_run=dry_run,
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
                    return _truncate_text(_redact_diff(raw), tool="diff")
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
            return _truncate_text(raw, tool="diff")
        except Exception as exc:
            set_span_error(_h, exc)
            raise


def _kubectl_present() -> bool:
    """Probe whether kubectl is on PATH for diff_manifest (#1127).

    Only ``FileNotFoundError`` (PATH miss) is treated as 'not installed'
    (#1205); any other exception is surfaced so operators see the real
    failure rather than silently pretending kubectl is absent. Timeouts
    and permission errors are logged at WARN and surfaced as a distinct
    False return (with a logged reason) so the caller's downstream
    message to the LLM is still "kubectl not available" — the operator
    gets the diagnostic in pod logs.
    """
    try:
        proc = subprocess.run(
            ["kubectl", "version", "--client=true", "--output=yaml"],
            capture_output=True, text=True, check=False, timeout=5,
        )
        return proc.returncode == 0
    except FileNotFoundError:
        return False
    except subprocess.TimeoutExpired as exc:
        log.warning("kubectl presence probe timed out: %s", exc)
        return False
    except PermissionError as exc:
        log.warning("kubectl presence probe permission error: %s", exc)
        return False


@mcp.tool()
def diff_manifest(manifest: str, redact: bool = True) -> str:
    """Preview what applying a raw YAML manifest would change vs. live (#1127).

    Symmetric with ``kubernetes.apply(dry_run=True)``: takes a multi-doc
    YAML string and shells out to ``kubectl diff -f -`` to compute the
    server-side diff against the cluster. Returns the unified diff
    text so callers can reason about a change before committing —
    especially useful when the change is not a Helm release upgrade.

    Requires ``kubectl`` on PATH inside the container. When absent
    the tool raises a ``HelmError`` with a clear message rather than
    silently returning empty — deploy-time probes should detect the
    missing dependency via the ``/info`` surface (#1122) before a
    caller hits this path.

    When ``redact=True`` (the default), output is passed through the
    same Secret-aware diff redactor used by :func:`diff` so Secret
    ``data``/``stringData`` values are masked before returning.
    """
    if not isinstance(manifest, str) or not manifest.strip():
        raise ValueError("helm: 'manifest' must be a non-empty YAML string")
    if not _kubectl_present():
        raise HelmError(
            "helm diff_manifest: kubectl not found on PATH. This tool "
            "requires kubectl to be installed in the mcp-helm container. "
            "See /info response for the current helm container capabilities."
        )
    with _handler_span(
        "diff_manifest",
        {"helm.manifest_bytes": len(manifest)},
    ) as _h:
        try:
            # kubectl diff exits 0 (no diff) or 1 (diff present); other
            # non-zero codes are real errors. We capture both streams
            # and only raise on a real failure.
            proc = subprocess.run(
                ["kubectl", "diff", "-f", "-"],
                input=manifest,
                capture_output=True, text=True, check=False,
                timeout=_HELM_SUBPROCESS_TIMEOUT_SECONDS,
            )
            if proc.returncode not in (0, 1):
                err = HelmError(
                    f"kubectl diff exited {proc.returncode}: "
                    f"{(proc.stderr or proc.stdout).strip()}"
                )
                set_span_error(_h, err)
                raise err
            raw = proc.stdout or ""
            if redact and raw:
                try:
                    return _truncate_text(_redact_diff(raw), tool="diff_manifest")
                except Exception as _redact_exc:
                    log.warning(
                        "diff_manifest redaction failed (%s); suppressing output.",
                        _redact_exc,
                    )
                    return (
                        "# diff redacted: Secret-aware redactor raised "
                        f"{type(_redact_exc).__name__} — output suppressed "
                        "to avoid leaking Secret contents (#915).\n"
                    )
            return _truncate_text(raw, tool="diff_manifest")
        except subprocess.TimeoutExpired as exc:
            err = HelmError(
                f"kubectl diff killed after "
                f"{_HELM_SUBPROCESS_TIMEOUT_SECONDS}s "
                "(HELM_SUBPROCESS_TIMEOUT_SECONDS)"
            )
            set_span_error(_h, err)
            raise err from exc
        except Exception as exc:
            set_span_error(_h, exc)
            raise


@mcp.tool()
def rollback(name: str, namespace: str, revision: int, wait: bool = False) -> str:
    """Roll a release back to a prior revision.

    Helm's `rollback` does not support `-o json`; the raw CLI output is
    returned.
    """
    _refuse_if_read_only("rollback")
    _reject_flag_like(name=name, namespace=namespace)
    _audit(
        "mcp-helm", "rollback",
        args={"name": name, "namespace": namespace,
              "revision": revision, "wait": wait},
    )
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
    _refuse_if_read_only("uninstall")
    _reject_flag_like(name=name, namespace=namespace)
    _audit(
        "mcp-helm", "uninstall",
        args={"name": name, "namespace": namespace,
              "keep_history": keep_history},
        dry_run=dry_run,
    )
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


def _repo_url_allowlist() -> set[str]:
    """Parse MCP_HELM_REPO_URL_ALLOWLIST into a hostname set (#1202)."""
    raw = os.environ.get("MCP_HELM_REPO_URL_ALLOWLIST", "")
    return {h.strip().lower() for h in raw.split(",") if h.strip()}


def _repo_allow_any() -> bool:
    return os.environ.get("MCP_HELM_ALLOW_ANY_REPO", "").strip().lower() in {
        "1", "true", "yes", "on",
    }


@mcp.tool()
def repo_add(name: str, url: str) -> str:
    """Add a chart repository.

    URL is gated by ``MCP_HELM_REPO_URL_ALLOWLIST`` (comma-separated
    hostnames) + ``MCP_HELM_ALLOW_ANY_REPO`` (bool, default false) to
    stop an LLM-supplied registry from shipping chart code the operator
    never vetted (#1202). When the allow-list is empty and
    ``MCP_HELM_ALLOW_ANY_REPO`` is not truthy, ``repo_add`` fails
    closed. When the allow-list is populated, only URLs whose hostname
    is in the list are accepted.
    """
    _reject_flag_like(name=name, url=url)
    # #1202: validate the URL against the operator allow-list before
    # letting helm reach out to it.
    from urllib.parse import urlparse as _urlparse
    parsed = _urlparse(url)
    # #1369: refuse userinfo in URL (credentials in-line defeat the
    # allow-list since allow-list compares hostname only).
    if parsed.username is not None or parsed.password is not None:
        raise HelmError(
            f"helm repo_add: URL with userinfo is not accepted "
            "(embed credentials via helm's login flow instead). See #1369."
        )
    hostname = (parsed.hostname or "").lower().rstrip(".")
    # #1369: IDN / punycode normalisation so homograph hosts that
    # visually resemble allow-list entries can't slip past the string
    # compare. idna.encode is stricter than ascii-lowered .hostname.
    if hostname:
        try:
            import idna as _idna
            hostname = _idna.encode(hostname).decode("ascii")
        except Exception:
            # idna not installed or invalid name — reject rather than
            # fall through to the less-strict .lower() compare.
            raise HelmError(
                f"helm repo_add: hostname {parsed.hostname!r} failed IDN "
                "normalisation. See #1369."
            )
    if parsed.scheme not in ("http", "https", "oci"):
        raise HelmError(
            f"helm repo_add: URL scheme must be http/https/oci (got "
            f"{parsed.scheme!r}). See #1202."
        )
    if not hostname:
        raise HelmError(
            f"helm repo_add: URL must have a hostname (got {url!r}). "
            "See #1202."
        )
    allowlist = _repo_url_allowlist()
    allow_any = _repo_allow_any()
    if not allowlist and not allow_any:
        raise HelmError(
            "helm repo_add: refused because MCP_HELM_REPO_URL_ALLOWLIST "
            "is empty and MCP_HELM_ALLOW_ANY_REPO is not set. Operators "
            "must opt into each chart registry by listing its hostname "
            "in MCP_HELM_REPO_URL_ALLOWLIST (comma-separated), or set "
            "MCP_HELM_ALLOW_ANY_REPO=true to accept any host (not "
            "recommended). See #1202."
        )
    if allowlist and hostname not in allowlist:
        raise HelmError(
            f"helm repo_add: host {hostname!r} is not in "
            f"MCP_HELM_REPO_URL_ALLOWLIST. Allowed hosts: "
            f"{sorted(allowlist)}. See #1202."
        )
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

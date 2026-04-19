"""Durable privileged-op audit log for MCP tool servers (#1125).

Mutating tool invocations (``apply``, ``delete``, ``install``, ``upgrade``,
``rollback``, ``uninstall``) and privileged reads (``read_secret_value``)
already surface via OTel span attributes, but OTel is off by default in
most deployments and a collector outage can lose evidence. This module
writes a JSONL audit record to a local path on every privileged op so
an incident responder can reconstruct the mutation chain from disk
without depending on external observability plumbing.

Designed to be cheap and always-on:

- One line per op, newline-delimited JSON, append-only.
- fcntl-based locking via ``log_utils._append_log`` for multi-process
  safety and rotation — rotates at ``MAX_LOG_BYTES`` just like the
  conversation log, and keeps ``MAX_LOG_BACKUP_COUNT`` numbered
  backups.
- Never raises into the caller. A failure to write an audit line logs
  a warning but must not mask or block the tool invocation itself;
  the alternative ("fail the tool because audit is broken") would
  create an availability incident out of a logging incident.
- Path is configurable via ``MCP_AUDIT_LOG_PATH`` (default
  ``/home/tool/logs/audit.jsonl``). An unset / empty value disables
  the sink so a bare dev checkout does not try to open a directory
  that doesn't exist.
"""

from __future__ import annotations

import json
import logging
import os
import time
from typing import Any

try:
    from log_utils import _append_log  # type: ignore
except Exception:  # pragma: no cover - defensive fallback
    _append_log = None  # type: ignore

logger = logging.getLogger(__name__)

_DEFAULT_PATH = "/home/tool/logs/audit.jsonl"


def _audit_path() -> str | None:
    raw = os.environ.get("MCP_AUDIT_LOG_PATH")
    if raw is None:
        return _DEFAULT_PATH
    raw = raw.strip()
    if not raw:
        return None
    return raw


def audit(
    server: str,
    tool: str,
    *,
    outcome: str = "invoked",
    args: dict[str, Any] | None = None,
    caller: str | None = None,
    dry_run: bool | None = None,
    error: str | None = None,
) -> None:
    """Write one JSONL audit line.

    Args are redacted to keys only — a ``values`` dict for helm install
    could carry Secret material, so the audit log captures the shape
    (``{"values": "<dict len=N>"}``) not the contents.
    """
    path = _audit_path()
    if not path or _append_log is None:
        return
    record: dict[str, Any] = {
        "ts": time.time(),
        "server": server,
        "tool": tool,
        "outcome": outcome,
        "agent": caller or os.environ.get("AGENT_NAME") or "unknown",
        "pid": os.getpid(),
    }
    if dry_run is not None:
        record["dry_run"] = bool(dry_run)
    if args:
        record["args"] = _redact_args(args)
    if error is not None:
        record["error"] = str(error)[:500]
    try:
        _append_log(path, json.dumps(record, default=str))
    except Exception as exc:
        # Must never mask the original op. Log-and-continue.
        logger.warning("mcp_audit: failed to append audit line: %r", exc)


def _redact_args(args: dict[str, Any]) -> dict[str, Any]:
    """Shallow-redact arg dict for the audit line.

    Policy: record key presence + a shape/type marker, not contents.
    ``values`` / ``manifest`` / ``data`` / ``stringData`` / anything
    under a ``_SECRET_LIKE_KEYS`` set is replaced with a placeholder.
    """
    secret_like = {"values", "manifest", "data", "stringData", "string_data",
                   "token", "password", "secret"}
    out: dict[str, Any] = {}
    for k, v in args.items():
        if k in secret_like:
            if isinstance(v, dict):
                out[k] = f"<dict len={len(v)}>"
            elif isinstance(v, str):
                out[k] = f"<str len={len(v)}>"
            elif v is None:
                out[k] = None
            else:
                out[k] = f"<{type(v).__name__}>"
            continue
        if isinstance(v, (str, int, float, bool)) or v is None:
            out[k] = v
        elif isinstance(v, dict):
            out[k] = f"<dict len={len(v)}>"
        elif isinstance(v, (list, tuple)):
            out[k] = f"<{type(v).__name__} len={len(v)}>"
        else:
            out[k] = f"<{type(v).__name__}>"
    return out

"""Structural validation for Claude Agent SDK MCP server entries (#1051).

Extracted from ``backends/claude/executor.py`` so the validator can
be unit-tested without importing the full executor surface (Agents SDK,
OTel bootstrap, session store). The executor imports
:func:`validate_mcp_servers_shape` from this module and layers it after
:func:`_sanitize_mcp_servers` in the reload path.

The SDK consumes the dict entry-by-entry when spawning stdio children
or opening HTTP/SSE sessions. Previously, a malformed entry (missing
both ``command`` and ``url``; a ``command`` that isn't a string; a
malformed ``url``) landed in ``self._mcp_servers`` and the SDK silently
dropped it at startup — so the executor's view of "how many servers
are live" disagreed with reality. This validator catches that class
before the swap, so the executor's dict agrees with what the SDK will
actually attempt to start.

The check is intentionally shallow: it does not try to spawn the
subprocess or open the URL. That expense belongs to the SDK.
"""

from __future__ import annotations

from urllib.parse import urlparse


def validate_mcp_servers_shape(servers: dict) -> tuple[dict, list[tuple[str, str]]]:
    """Return ``(valid, rejected)`` for an already-sanitised server dict.

    * ``valid`` — a new dict containing only structurally sound entries.
    * ``rejected`` — list of ``(name, reason)`` tuples, one per dropped
      entry. Callers log at WARNING so operators see which keys were
      silently lost from their reload.

    Rules enforced per entry:

    1. Entry value must be a JSON object (``dict``).
    2. Exactly one of ``command`` (stdio transport) and ``url`` (HTTP /
       SSE transport) must be present.
    3. If ``command`` — must be a non-empty string (whitespace-only is
       rejected).
    4. If ``url`` — must be a non-empty string that ``urlparse`` can
       parse into a ``(scheme, netloc)`` pair. A URL without a scheme or
       without a host is structurally invalid for the MCP transport.
    """
    if not isinstance(servers, dict):
        return {}, []
    valid: dict = {}
    rejected: list[tuple[str, str]] = []
    for name, cfg in servers.items():
        if not isinstance(cfg, dict):
            rejected.append((str(name), "entry is not a JSON object"))
            continue
        has_cmd = "command" in cfg
        has_url = "url" in cfg
        if has_cmd and has_url:
            rejected.append((str(name), "entry has both 'command' and 'url' — pick one"))
            continue
        if not has_cmd and not has_url:
            rejected.append((str(name), "entry has neither 'command' nor 'url'"))
            continue
        if has_cmd:
            cmd = cfg["command"]
            if not isinstance(cmd, str) or not cmd.strip():
                rejected.append((str(name), "'command' must be a non-empty string"))
                continue
        else:
            url = cfg["url"]
            if not isinstance(url, str) or not url.strip():
                rejected.append((str(name), "'url' must be a non-empty string"))
                continue
            try:
                parsed = urlparse(url)
            except Exception as exc:  # pragma: no cover - defensive
                rejected.append((str(name), f"'url' did not parse: {exc}"))
                continue
            if not parsed.scheme or not parsed.netloc:
                rejected.append((str(name), "'url' missing scheme or host"))
                continue
        valid[name] = cfg
    return valid, rejected

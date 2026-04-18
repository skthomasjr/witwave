"""Shared MCP ``command`` allow-list (#711, #797).

Every backend's ``mcp.json`` can name a stdio subprocess to spawn
(``command`` + ``args``). Without validation, a mis-reviewed
``mcp.json`` can drop ``/bin/sh`` — or any attacker-chosen binary —
into the spawn path and achieve arbitrary code execution inside the
backend container. The allow-list caps that blast radius to a small
operator-curated set.

This module hosts the policy so each backend imports the same rule
rather than carrying a copy (and drifting apart). The policy is
configured via environment variables:

* ``MCP_ALLOWED_COMMANDS`` — comma-separated list of accepted entries.
  Each entry is either an absolute path
  (``/usr/local/bin/mcp-kubernetes``) or a bare basename
  (``mcp-kubernetes``). Empty defaults to a conservative read-only
  set covering the MCP tools shipped in this repo.
* ``MCP_ALLOWED_COMMAND_PREFIXES`` — comma-separated absolute-path
  prefixes. Commands resolving to a real path that begins with one
  of these prefixes are accepted even if the basename isn't
  explicitly in the allow-list. Default
  ``/home/agent/mcp-bin/,/usr/local/bin/``.

The primary entrypoint is :func:`mcp_command_allowed(command)` which
returns ``(ok, reason)``. ``reason`` is a short stable enum label —
``non_string``, ``empty``, ``absolute_not_on_prefix``,
``basename_not_allowed``, ``absolute_prefix``, ``basename_allowed``
— safe for Prometheus metric labels.

Tests live in ``harness/test_mcp_command_allowlist.py``.
"""

from __future__ import annotations

import os

DEFAULT_MCP_ALLOWED_COMMANDS = "mcp-kubernetes,mcp-helm,uv,uvx"
DEFAULT_MCP_ALLOWED_COMMAND_PREFIXES = "/home/agent/mcp-bin/,/usr/local/bin/"


def _load_env_frozenset(var: str, default: str) -> frozenset[str]:
    return frozenset(
        t.strip() for t in os.environ.get(var, default).split(",") if t.strip()
    )


def _load_env_tuple(var: str, default: str) -> tuple[str, ...]:
    return tuple(t.strip() for t in os.environ.get(var, default).split(",") if t.strip())


def mcp_command_allowed(
    command: object,
    *,
    allowed: frozenset[str] | None = None,
    prefixes: tuple[str, ...] | None = None,
) -> tuple[bool, str]:
    """Decide whether ``command`` is admissible as an MCP stdio entry.

    The ``allowed`` and ``prefixes`` overrides exist for tests — in
    production the env vars seed the module-level defaults.
    """
    if allowed is None:
        allowed = _load_env_frozenset("MCP_ALLOWED_COMMANDS", DEFAULT_MCP_ALLOWED_COMMANDS)
    if prefixes is None:
        prefixes = _load_env_tuple(
            "MCP_ALLOWED_COMMAND_PREFIXES", DEFAULT_MCP_ALLOWED_COMMAND_PREFIXES
        )

    if not isinstance(command, str):
        return False, "non_string"
    cmd = command.strip()
    if not cmd:
        return False, "empty"
    if cmd.startswith("/"):
        # Absolute paths MUST resolve under an allowed prefix. The
        # basename fallback used to accept any /tmp/attacker/python
        # whose basename happened to be in the allow-list, turning the
        # allow-list into a per-file bypass (#862). Absolute paths now
        # require an explicit prefix match.
        for prefix in prefixes:
            if cmd.startswith(prefix):
                return True, "absolute_prefix"
        return False, "absolute_not_on_prefix"
    # Non-absolute path — allowed only when the bare basename matches.
    if cmd in allowed or os.path.basename(cmd) in allowed:
        return True, "basename_allowed"
    return False, "basename_not_allowed"

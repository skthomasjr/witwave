"""gemini PreToolUse/PostToolUse hook policy facade (#631).

This module is the gemini-flavoured companion to ``backends/claude/hooks.py``. It
re-exports the backend-agnostic engine from ``shared/hooks_engine.py`` and
registers a gemini-specific ``a2_hooks_config_errors_total`` reporter so
YAML parse/validation errors land on the counter with
``backend="gemini"``.

Scope caveat — gemini does not yet have native tool-calling support in
its executor (that's feature #640). This module lands the *infrastructure*
— engine wiring, config loader, and watcher hooks — so that when #640
brings a tool-call path, it can call :func:`evaluate_pre_tool_use` without
further plumbing. Until then the engine is effectively a no-op for gemini.

See ``shared/hooks_engine.py`` for the full matching semantics and the
``hooks.yaml`` schema.
"""
from __future__ import annotations

import os

from hooks_engine import (  # noqa: F401  (re-exported public API)
    BASELINE_RULES,
    DECISION_ALLOW,
    DECISION_DENY,
    DECISION_WARN,
    HookState,
    Rule,
    evaluate_pre_tool_use,
    load_extension_rules,
    set_config_error_reporter,
)


HOOKS_CONFIG_PATH = os.environ.get(
    "HOOKS_CONFIG_PATH", "/home/agent/.gemini/hooks.yaml"
)
HOOKS_BASELINE_ENABLED = os.environ.get("HOOKS_BASELINE_ENABLED", "true").lower() not in (
    "0",
    "false",
    "no",
    "off",
)


def _bump_config_error(reason: str) -> None:
    """Increment ``a2_hooks_config_errors_total{reason}`` for the gemini backend.

    Mirrors the claude reporter pattern. Any failure here is swallowed so
    malformed YAML can never crash the rule parser.
    """
    try:
        from metrics import a2_hooks_config_errors_total
        if a2_hooks_config_errors_total is None:
            return
        labels = {
            "agent": os.environ.get("AGENT_OWNER", os.environ.get("AGENT_NAME", "a2-gemini")),
            "agent_id": os.environ.get("AGENT_ID", "gemini"),
            "backend": "gemini",
        }
        a2_hooks_config_errors_total.labels(**labels, reason=reason).inc()
    except Exception:  # pragma: no cover — metrics must never break hook parsing
        pass


# Register the gemini reporter at import time so the shared engine routes
# config errors to the gemini-labelled counter whenever this module is the
# active consumer. Re-installation on subsequent imports is a no-op.
set_config_error_reporter(_bump_config_error)


def load_hooks_config_sync() -> list:
    """Synchronous wrapper around ``load_extension_rules`` for ``asyncio.to_thread``.

    Mirrors claude's ``_load_hooks_config_sync`` so the executor's watcher
    implementation can stay structurally identical across backends.
    """
    return load_extension_rules(HOOKS_CONFIG_PATH)

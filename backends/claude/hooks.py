"""Claude Agent SDK PreToolUse/PostToolUse hook policy engine (#467).

The cross-backend rule vocabulary (``Rule``, decision constants, baseline
predicate set, YAML extension loader, and evaluator) lives in
``shared/hooks_engine.py`` (#631). This module is now the Claude-specific
facade: it re-exports the engine's public API (so existing imports like
``from hooks import BASELINE_RULES, evaluate_pre_tool_use`` keep working)
and registers a claude-flavoured ``backend_hooks_config_errors_total`` reporter
so YAML parse/validation errors keep landing on the existing counter with
the correct ``backend="claude"`` label.

See ``shared/hooks_engine.py`` for the full matching semantics and the
``hooks.yaml`` schema documentation. The rest of this file exists so that
``claude``'s executor doesn't need to know where the engine lives.

Two layers of policy (unchanged by the refactor):

1. **Baseline** — a fixed list of deny rules that ship with the executor and
   block the most obvious dangerous shell patterns. Disabled via the
   ``HOOKS_BASELINE_ENABLED=false`` environment variable when an operator
   has explicit permissive intent.

2. **Extensions** — opt-in per-agent rules loaded from
   ``/home/agent/.claude/hooks.yaml`` (path configurable via
   ``HOOKS_CONFIG_PATH``). The file is hot-reloaded by
   :class:`AgentExecutor.hooks_config_watcher`.

The Claude Agent SDK accepts hooks via ``ClaudeAgentOptions.hooks``; for a
PreToolUse deny, the hook callable returns
``{"hookSpecificOutput": {"hookEventName": "PreToolUse",
"permissionDecision": "deny", "permissionDecisionReason": "<why>"}}``.
"""
from __future__ import annotations

import logging
import os

logger = logging.getLogger(__name__)

# Guard the shared hooks_engine import so a parse/syntax/import error in
# shared/hooks_engine.py does NOT crash-loop the entire claude backend
# (#1050). Mirrors the pattern codex adopted in #938. On failure we log
# at WARNING, fall back to an empty baseline (BASELINE_RULES = []), and
# supply no-op replacements for the public symbols the rest of the
# backend imports. The hooks layer then becomes permissive — the SDK
# permission prompts and other controls remain — but the process stays
# up so operators can observe the error and ship a fix.
try:
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
except Exception as _hooks_engine_import_exc:  # noqa: BLE001 — documented single-fail path
    logger.warning(
        "claude: failed to import shared hooks_engine: %r — baseline DISABLED, "
        "hook evaluator permissive; backend is up but operator must fix "
        "shared/hooks_engine.py. See #1050.",
        _hooks_engine_import_exc,
    )
    BASELINE_RULES: list = []  # type: ignore[no-redef]
    DECISION_ALLOW = "allow"  # type: ignore[assignment]
    DECISION_DENY = "deny"  # type: ignore[assignment]
    DECISION_WARN = "warn"  # type: ignore[assignment]

    class HookState:  # type: ignore[no-redef]
        """Fallback stub when hooks_engine import fails (#1050)."""
        def __init__(self, *args, **kwargs) -> None:
            self.rules: list = []

    class Rule:  # type: ignore[no-redef]
        """Fallback stub when hooks_engine import fails (#1050)."""
        pass

    def evaluate_pre_tool_use(tool_name, tool_input, rules):  # type: ignore[no-redef]
        return (DECISION_ALLOW, None)

    def load_extension_rules(path: str):  # type: ignore[no-redef]
        return []

    def set_config_error_reporter(fn) -> None:  # type: ignore[no-redef]
        return None

    # Bump backend_hooks_config_errors_total{reason='baseline_import'} so
    # the failure surfaces in dashboards the same way as a YAML parse error.
    try:
        from metrics import backend_hooks_config_errors_total
        if backend_hooks_config_errors_total is not None:
            backend_hooks_config_errors_total.labels(
                agent=os.environ.get("AGENT_OWNER", os.environ.get("AGENT_NAME", "claude")),
                agent_id=os.environ.get("AGENT_ID", "claude"),
                backend="claude",
                reason="baseline_import",
            ).inc()
    except Exception:
        # Metric emission must never mask the underlying import failure.
        pass


HOOKS_CONFIG_PATH = os.environ.get("HOOKS_CONFIG_PATH", "/home/agent/.claude/hooks.yaml")
HOOKS_BASELINE_ENABLED = os.environ.get("HOOKS_BASELINE_ENABLED", "true").lower() not in (
    "0",
    "false",
    "no",
    "off",
)


def _bump_config_error(reason: str) -> None:
    """Increment ``backend_hooks_config_errors_total{reason}`` for the claude backend (#623).

    Imported lazily so this module stays test-friendly when metrics are not
    wired up. The ``reason`` value is a closed enum — see ``metrics.py`` for
    the canonical list. Labels are resolved from process env so the reporter
    stays decoupled from the executor's ``_LABELS`` dict.
    """
    try:
        from metrics import backend_hooks_config_errors_total
        if backend_hooks_config_errors_total is None:
            return
        labels = {
            "agent": os.environ.get("AGENT_OWNER", os.environ.get("AGENT_NAME", "claude")),
            "agent_id": os.environ.get("AGENT_ID", "claude"),
            "backend": "claude",
        }
        backend_hooks_config_errors_total.labels(**labels, reason=reason).inc()
    except Exception:  # pragma: no cover — metrics must never break hook parsing
        pass


# Register the claude reporter with the shared engine. Import-time side
# effect, executed exactly once when ``backends/claude/hooks.py`` is first loaded.
set_config_error_reporter(_bump_config_error)


def load_hooks_config_sync() -> list:
    """Synchronous wrapper around ``load_extension_rules`` for ``asyncio.to_thread``.

    Mirrors gemini's ``load_hooks_config_sync`` so the executor's watcher
    implementation can stay structurally identical across backends (#798).
    """
    return load_extension_rules(HOOKS_CONFIG_PATH)

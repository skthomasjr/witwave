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
            "agent": os.environ.get("AGENT_OWNER", os.environ.get("AGENT_NAME", "a2-claude")),
            "agent_id": os.environ.get("AGENT_ID", "claude"),
            "backend": "claude",
        }
        backend_hooks_config_errors_total.labels(**labels, reason=reason).inc()
    except Exception:  # pragma: no cover — metrics must never break hook parsing
        pass


# Register the claude reporter with the shared engine. Import-time side
# effect, executed exactly once when ``backends/claude/hooks.py`` is first loaded.
set_config_error_reporter(_bump_config_error)

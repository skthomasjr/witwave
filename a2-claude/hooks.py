"""Claude Agent SDK PreToolUse/PostToolUse hook policy engine (#467).

Two layers:

1. **Baseline** — a fixed list of deny rules that ship with the executor and
   block the most obvious dangerous shell patterns (``rm -rf /``, recursive
   writes to host paths, ``git push --force`` against ``main``/``master``,
   ``curl | sh`` style pipe-to-shell, ``chmod 777``, etc.). The baseline is
   intentionally conservative — it should almost never trip in normal agent
   workflows — and can be disabled via the ``HOOKS_BASELINE_ENABLED=false``
   environment variable when an operator has explicit permissive intent.

2. **Extensions** — opt-in per-agent rules loaded from
   ``/home/agent/.claude/hooks.yaml`` (path configurable via
   ``HOOKS_CONFIG_PATH``). The file is hot-reloaded by
   :class:`AgentExecutor.hooks_config_watcher`, mirroring the existing
   ``mcp.json`` / ``CLAUDE.md`` watcher pattern.

PostToolUse hooks always emit a structured JSONL audit row to a separate
``tool-audit.jsonl`` log via :func:`log_tool_audit` in ``executor``.  This
gives operators a forensic trail of every tool call the agent made — the
baseline cannot be opted out of for PostToolUse because audit is a
transparency guarantee, not a policy choice.

The Claude Agent SDK accepts hooks via ``ClaudeAgentOptions.hooks``, a dict
mapping event name to a list of ``HookMatcher`` objects. Each ``HookMatcher``
takes a tool-name matcher string (``"*"`` = all tools) and a list of async
callables. The callables receive the hook input dict plus a ``HookContext``
and must return a dict conforming to ``SyncHookJSONOutput``.

For PreToolUse, a deny is signalled with::

    {"hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny",
        "permissionDecisionReason": "<human-readable reason>",
    }}

An allow is signalled by returning ``{}`` (or omitting ``permissionDecision``).

``hooks.yaml`` schema (all fields optional except ``name`` and one of
``deny_if_match`` / ``warn_if_match``)::

    extensions:
      - name: block-private-key-writes
        tool: "Write"              # exact tool name; "*" or omit = any tool
        deny_if_match: "BEGIN PRIVATE KEY"
        reason: "refusing to write private keys"
      - name: warn-on-webfetch
        tool: "WebFetch"
        warn_if_match: ".*"
        reason: "network call"

Patterns are Python regexes matched against the JSON-serialised ``tool_input``
payload, which is a cheap way to cover argument strings across all tool
shapes without per-tool argument plumbing.
"""
from __future__ import annotations

import json
import logging
import os
import re
from dataclasses import dataclass, field
from typing import Any

import yaml

logger = logging.getLogger(__name__)

# Decision vocabulary — strings instead of enums so YAML-loaded rules can
# round-trip cleanly and hook callables can return the SDK-native literals
# without conversion gymnastics.
DECISION_ALLOW = "allow"
DECISION_DENY = "deny"
DECISION_WARN = "warn"


@dataclass(frozen=True)
class Rule:
    """One evaluation rule for a PreToolUse hook.

    ``tool`` is matched exactly against ``tool_input['tool_name']`` or is the
    literal ``"*"`` / ``None`` to apply to every tool. ``pattern`` is a
    compiled regex applied to the JSON-serialised tool input; ``action``
    decides what happens on a match.
    """

    name: str
    tool: str | None  # None or "*" => any tool
    pattern: re.Pattern[str]
    action: str  # DECISION_DENY or DECISION_WARN
    reason: str
    source: str  # "baseline" or "extension"


# Baseline rules. Each entry is kept very narrow to minimise false positives
# — the aim is to catch obvious "blast-radius" commands, not to build a
# general-purpose sandbox. Operators who need more can layer extensions.
#
# Patterns are applied to a JSON string of the tool_input, so e.g. a Bash
# command of ``rm -rf /tmp/foo`` is matched against ``"command": "rm -rf
# /tmp/foo"``. The regexes are conservative enough to avoid matching
# legitimate inputs that merely mention the dangerous string.
_BASELINE_RULES_RAW: list[dict[str, str]] = [
    {
        "name": "baseline-rm-rf-root",
        "tool": "Bash",
        # Matches rm -rf / and rm -rf /* but not rm -rf /tmp/foo. The negative
        # lookahead after the slash rejects anything that continues with
        # another path component character.
        "pattern": r"\brm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+(?:/|~|--no-preserve-root\b)(?![A-Za-z0-9._-])",
        "reason": "rm -rf targeting root or home — refusing by baseline policy.",
    },
    {
        "name": "baseline-git-force-push-main",
        "tool": "Bash",
        # Force push to main/master — covers -f, --force, and --force-with-lease
        # followed by the branch name.
        "pattern": r"\bgit\s+push\s+[^\n]*?(?:--force(?:-with-lease)?|-f\b)[^\n]*?\b(?:main|master)\b",
        "reason": "git push --force to main/master — refusing by baseline policy.",
    },
    {
        "name": "baseline-curl-pipe-shell",
        "tool": "Bash",
        # curl | sh / curl | bash — classic supply-chain anti-pattern.
        "pattern": r"\b(?:curl|wget)\b[^\n]*\|\s*(?:sh|bash|zsh|ksh)\b",
        "reason": "curl/wget piped to a shell — refusing by baseline policy.",
    },
    {
        "name": "baseline-chmod-777",
        "tool": "Bash",
        "pattern": r"\bchmod\s+(?:-[a-zA-Z]+\s+)*777\b",
        "reason": "chmod 777 — refusing by baseline policy (world-writable is almost never intended).",
    },
    {
        "name": "baseline-dd-device",
        "tool": "Bash",
        # dd of=/dev/... — catastrophic when mistyped.
        "pattern": r"\bdd\s+[^\n]*?\bof=/dev/(?:sd|nvme|disk|hd)[a-z0-9]+",
        "reason": "dd to a block device — refusing by baseline policy.",
    },
]


def _compile_baseline() -> list[Rule]:
    rules: list[Rule] = []
    for raw in _BASELINE_RULES_RAW:
        try:
            rules.append(
                Rule(
                    name=raw["name"],
                    tool=raw.get("tool"),
                    pattern=re.compile(raw["pattern"]),
                    action=DECISION_DENY,
                    reason=raw["reason"],
                    source="baseline",
                )
            )
        except re.error as exc:
            # A bad baseline regex is a bug in this file, not a runtime
            # condition — log loudly but do not crash the executor.
            logger.error("Baseline hook rule %r has invalid regex: %s", raw.get("name"), exc)
    return rules


BASELINE_RULES: list[Rule] = _compile_baseline()


def _parse_extension_rule(raw: dict[str, Any]) -> Rule | None:
    """Parse one hooks.yaml entry. Returns ``None`` if the entry is invalid."""
    name = raw.get("name")
    if not isinstance(name, str) or not name.strip():
        logger.warning("hooks.yaml: skipping entry with missing/empty name: %r", raw)
        return None
    tool = raw.get("tool")
    if tool is not None and not isinstance(tool, str):
        logger.warning("hooks.yaml: rule %r has non-string tool %r — skipping.", name, tool)
        return None

    deny_pat = raw.get("deny_if_match")
    warn_pat = raw.get("warn_if_match")
    if deny_pat and warn_pat:
        logger.warning(
            "hooks.yaml: rule %r has both deny_if_match and warn_if_match — preferring deny.", name
        )
        warn_pat = None
    if not deny_pat and not warn_pat:
        logger.warning("hooks.yaml: rule %r has neither deny_if_match nor warn_if_match — skipping.", name)
        return None

    action = DECISION_DENY if deny_pat else DECISION_WARN
    pat_source = deny_pat if deny_pat else warn_pat
    if not isinstance(pat_source, str):
        logger.warning("hooks.yaml: rule %r pattern is not a string — skipping.", name)
        return None
    try:
        pattern = re.compile(pat_source)
    except re.error as exc:
        logger.warning("hooks.yaml: rule %r has invalid regex %r: %s — skipping.", name, pat_source, exc)
        return None

    reason = raw.get("reason") or f"blocked by extension rule {name!r}"
    if not isinstance(reason, str):
        reason = str(reason)

    return Rule(
        name=name,
        tool=tool if tool and tool != "*" else None,
        pattern=pattern,
        action=action,
        reason=reason,
        source="extension",
    )


def load_extension_rules(path: str) -> list[Rule]:
    """Read hooks.yaml at *path* and return the parsed extension rules.

    Missing file → empty list (normal; hooks.yaml is optional). Malformed YAML
    or unparseable individual rules log a warning and are skipped; the
    executor keeps running with whatever valid rules were loaded.
    """
    if not os.path.exists(path):
        return []
    try:
        with open(path) as f:
            data = yaml.safe_load(f) or {}
    except Exception as exc:
        logger.warning("Failed to load hooks.yaml from %s: %s", path, exc)
        return []

    if not isinstance(data, dict):
        logger.warning("hooks.yaml at %s is not a mapping — ignoring.", path)
        return []

    raw_list = data.get("extensions") or []
    if not isinstance(raw_list, list):
        logger.warning("hooks.yaml at %s has non-list `extensions` — ignoring.", path)
        return []

    parsed: list[Rule] = []
    for raw in raw_list:
        if not isinstance(raw, dict):
            logger.warning("hooks.yaml: skipping non-mapping entry %r", raw)
            continue
        rule = _parse_extension_rule(raw)
        if rule is not None:
            parsed.append(rule)
    return parsed


def _tool_matches(rule: Rule, tool_name: str) -> bool:
    return rule.tool is None or rule.tool == tool_name


def _input_haystack(tool_input: dict[str, Any]) -> str:
    """JSON-serialise ``tool_input`` into a single string for regex matching.

    Uses ``default=str`` so non-serialisable values (e.g. callables that
    might appear in exotic MCP tool payloads) do not abort the evaluation.
    """
    try:
        return json.dumps(tool_input, default=str, ensure_ascii=False)
    except Exception:  # pragma: no cover — json with default=str is extremely permissive
        return repr(tool_input)


def evaluate_pre_tool_use(
    tool_name: str,
    tool_input: dict[str, Any],
    rules: list[Rule],
) -> tuple[str, Rule | None]:
    """Evaluate *rules* against a tool call. Returns ``(decision, matched_rule)``.

    A deny rule short-circuits immediately. Warn rules are collected silently
    and the *first* matching warn rule is returned so the caller can record
    a single representative audit entry. If nothing matches, returns
    ``(DECISION_ALLOW, None)``.
    """
    haystack = _input_haystack(tool_input)
    first_warn: Rule | None = None
    for rule in rules:
        if not _tool_matches(rule, tool_name):
            continue
        if not rule.pattern.search(haystack):
            continue
        if rule.action == DECISION_DENY:
            return DECISION_DENY, rule
        if rule.action == DECISION_WARN and first_warn is None:
            first_warn = rule
    if first_warn is not None:
        return DECISION_WARN, first_warn
    return DECISION_ALLOW, None


@dataclass
class HookState:
    """Live, mutable view of the current rule set.

    Held by the :class:`AgentExecutor` and mutated in place by the config
    watcher. The hook callables capture this object by reference at
    ``_make_options()`` time, so every tool call sees the latest rules
    without rebuilding the SDK options.
    """

    baseline_enabled: bool = True
    baseline: list[Rule] = field(default_factory=list)
    extensions: list[Rule] = field(default_factory=list)

    def active_rules(self) -> list[Rule]:
        if self.baseline_enabled:
            return list(self.baseline) + list(self.extensions)
        return list(self.extensions)

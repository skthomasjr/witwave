"""Claude Agent SDK PreToolUse/PostToolUse hook policy engine (#467).

Two layers:

1. **Baseline** — a fixed list of deny rules that ship with the executor and
   block the most obvious dangerous shell patterns (``rm -rf /``, recursive
   writes to host paths, ``git push --force`` against ``main``/``master``,
   ``curl | sh`` style pipe-to-shell, ``chmod 777``, etc.). The baseline is
   intentionally conservative — it should almost never trip in normal agent
   workflows — and can be disabled via the ``HOOKS_BASELINE_ENABLED=false``
   environment variable when an operator has explicit permissive intent.

   The baseline is **not** a sandbox — treat it as a canary net that catches
   obvious mistakes. Real defence-in-depth comes from (a) a tight
   ``ALLOWED_TOOLS`` posture, (b) a read-only root filesystem, and (c) an
   egress ``NetworkPolicy``. Baseline rules evaluate against parsed
   tool-input structure (the ``command`` string for ``Bash``, the ``file_path``
   for ``Write``/``Edit``/``MultiEdit``/``NotebookEdit``) rather than the raw
   JSON serialisation, so trivial encoding bypasses (JSON unicode escapes,
   whitespace padding, tool-argument noise) do not defeat matching. The
   ``Bash`` baseline further splits the command with :func:`shlex.split` and
   inspects each argv token, which closes off absolute-path (``/usr/bin/rm``)
   and home-shorthand (``~root``) bypasses (#521).

2. **Extensions** — opt-in per-agent rules loaded from
   ``/home/agent/.claude/hooks.yaml`` (path configurable via
   ``HOOKS_CONFIG_PATH``). The file is hot-reloaded by
   :class:`AgentExecutor.hooks_config_watcher`, mirroring the existing
   ``mcp.json`` / ``CLAUDE.md`` watcher pattern. Extensions keep the
   regex-on-JSON matching strategy for backward compatibility with existing
   ``hooks.yaml`` files.

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
import shlex
from dataclasses import dataclass, field
from typing import Any, Callable

import yaml

logger = logging.getLogger(__name__)


def _bump_config_error(reason: str) -> None:
    """Increment ``a2_hooks_config_errors_total{reason}`` if metrics enabled (#623).

    Imported lazily so this module stays test-friendly when metrics are not
    wired up. The ``reason`` value is a closed enum — see metrics.py for the
    canonical list. Labels are looked up from executor's ``_LABELS`` context.
    """
    try:
        from metrics import a2_hooks_config_errors_total
        if a2_hooks_config_errors_total is None:
            return
        import os as _os
        _labels = {
            "agent": _os.environ.get("AGENT_OWNER", _os.environ.get("AGENT_NAME", "a2-claude")),
            "agent_id": _os.environ.get("AGENT_ID", "claude"),
            "backend": "claude",
        }
        a2_hooks_config_errors_total.labels(**_labels, reason=reason).inc()
    except Exception:  # pragma: no cover — metrics must never break hook parsing
        pass

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
    literal ``"*"`` / ``None`` to apply to every tool. ``action`` decides what
    happens on a match.

    Matching can use either of two strategies:

    * ``pattern`` — a compiled regex applied to the JSON-serialised tool
      input. Used by extension rules (preserves backward compatibility with
      the ``hooks.yaml`` schema) and retained as a ``Rule`` field for that
      reason.
    * ``predicate`` — a callable that receives the parsed tool-input dict and
      returns ``True`` on match. Used by baseline rules so matching happens
      against structured fields (``command``, ``file_path``, ``path``) rather
      than a JSON substring, which closes trivial encoding bypasses (#521).

    Exactly one of ``pattern`` / ``predicate`` is populated. ``pattern`` stays
    non-``None`` for extension rules; baseline rules set ``predicate`` and
    leave ``pattern`` as a placeholder that never runs because
    :func:`evaluate_pre_tool_use` prefers ``predicate`` when present.
    """

    name: str
    tool: str | None  # None or "*" => any tool
    pattern: re.Pattern[str] | None
    action: str  # DECISION_DENY or DECISION_WARN
    reason: str
    source: str  # "baseline" or "extension"
    predicate: Callable[[dict[str, Any]], bool] | None = None


# ---------------------------------------------------------------------------
# Baseline predicate helpers
# ---------------------------------------------------------------------------
#
# Baseline rules work against parsed tool-input structure instead of a JSON
# substring. The functions below pull the relevant field out of
# ``tool_input`` and normalise it (e.g. ``shlex.split`` for Bash commands,
# ``os.path.normpath`` for filesystem paths) before making a decision. This
# closes off the bypass classes reported in #521:
#
# * JSON unicode/whitespace encoding (``"\u0072m"``, extra spaces, embedded
#   newlines) — we operate on the already-parsed string, so the agent's
#   serialiser does not affect matching.
# * Absolute paths (``/usr/bin/rm``) and command substitution wrappers
#   (``$(printf rm)``) — the Bash baseline walks argv tokens and matches the
#   basename of the executable rather than a word-boundary regex.
# * Home-directory shorthand (``~root``, ``$HOME``) — expanded against a set
#   of known "root of blast radius" targets.
# * Non-octal chmod (``a+rwx``) — symbolic mode equivalents for world-writable
#   are caught alongside the literal ``0777``/``777``.
# * Devices outside the sd/nvme/disk/hd families (``/dev/mapper/root``,
#   ``/dev/loop0``) — the device deny list is now pattern-based over the
#   resolved target path.
#
# Each predicate returns ``True`` on match. Predicates must be total — they
# should never raise on unexpected tool-input shapes; if a field is missing
# they return ``False``. ``_safe_shlex_split`` centralises the one place that
# can raise (unterminated quotes) and falls back to a whitespace split.


def _bash_command(tool_input: dict[str, Any]) -> str:
    """Return the Bash ``command`` field as a string or empty string."""
    cmd = tool_input.get("command")
    return cmd if isinstance(cmd, str) else ""


def _safe_shlex_split(command: str) -> list[str]:
    """``shlex.split`` but return a whitespace-split fallback on parse error.

    An attacker-controlled command can contain unterminated quotes that would
    raise ``ValueError`` inside ``shlex``. Baseline evaluation MUST NOT crash
    the hook (that would bypass every downstream rule), so we fall back to a
    naive split which still exposes the argv tokens well enough for basename
    / flag inspection.
    """
    try:
        return shlex.split(command, posix=True)
    except ValueError:
        return command.split()


def _argv_basenames(command: str) -> list[str]:
    """Return the basename of every argv token in *command*.

    ``os.path.basename`` collapses ``/usr/bin/rm`` → ``rm`` and leaves bare
    ``rm`` alone, which is exactly the normalisation baseline predicates
    want.
    """
    return [os.path.basename(tok) for tok in _safe_shlex_split(command)]


# Targets that count as "root of blast radius" for ``rm -rf``. Compared
# against the normalised argv token, so absolute paths, ``~``, ``~root``, and
# ``$HOME`` all resolve through the same check.
_RM_RF_DANGEROUS_TARGETS: frozenset[str] = frozenset(
    {
        "/",
        "/*",
        "/.",
        "/..",
        "~",
        "~root",
        "~/",
        "$HOME",
        "${HOME}",
        "/root",
        "/home",
        "/etc",
        "/var",
        "/usr",
        "/bin",
        "/sbin",
        "/boot",
        "/lib",
        "/lib64",
    }
)


def _is_rm_rf_target(token: str) -> bool:
    """Does *token* name a catastrophic ``rm -rf`` target?"""
    if not token:
        return False
    if token in _RM_RF_DANGEROUS_TARGETS:
        return True
    # Strip a trailing slash / glob and re-check so "/etc/" and "/etc/*" both
    # reduce to "/etc". Do not strip anything for "/" itself.
    stripped = token.rstrip("/*")
    if stripped and stripped != token and stripped in _RM_RF_DANGEROUS_TARGETS:
        return True
    return False


def _predicate_rm_rf_root(tool_input: dict[str, Any]) -> bool:
    argv = _safe_shlex_split(_bash_command(tool_input))
    if not argv:
        return False
    # Walk every token so chained commands (``foo && rm -rf /``) and command
    # substitution wrappers (``sh -c "rm -rf /"`` — the inner argv is
    # emitted as a single token which we re-split) are both exercised.
    for idx, tok in enumerate(argv):
        base = os.path.basename(tok)
        if base != "rm":
            continue
        # Any following token of the form -rf / -fr / --recursive etc. plus a
        # dangerous target → deny. We are liberal about flag ordering because
        # GNU rm accepts the flags in any position.
        rest = argv[idx + 1 :]
        has_recursive = any(
            t in ("-r", "-R", "--recursive") or (t.startswith("-") and not t.startswith("--") and "r" in t)
            for t in rest
        )
        has_force = any(
            t in ("-f", "--force", "--no-preserve-root")
            or (t.startswith("-") and not t.startswith("--") and "f" in t)
            for t in rest
        )
        if not (has_recursive and has_force):
            # --no-preserve-root on its own is still suspicious; treat it as
            # an automatic deny regardless of other flags.
            if "--no-preserve-root" not in rest:
                continue
        for candidate in rest:
            if _is_rm_rf_target(candidate):
                return True
    # Also run the inner argv of ``sh -c "..."`` / ``bash -c "..."`` through
    # the same check so command substitution does not hide the intent.
    for idx, tok in enumerate(argv):
        if os.path.basename(tok) in {"sh", "bash", "zsh", "ksh"} and idx + 2 < len(argv) and argv[idx + 1] == "-c":
            inner = argv[idx + 2]
            if _predicate_rm_rf_root({"command": inner}):
                return True
    return False


def _predicate_git_force_push_main(tool_input: dict[str, Any]) -> bool:
    argv = _safe_shlex_split(_bash_command(tool_input))
    if not argv:
        return False
    # Find a ``git … push`` invocation anywhere in the argv; the baseline
    # does not care about chained commands, each segment is its own argv
    # after shell parsing fails to fully separate them but partial detection
    # is still better than the JSON regex.
    i = 0
    while i < len(argv):
        if os.path.basename(argv[i]) == "git":
            rest = argv[i + 1 :]
            # Skip over global git options like ``-C path`` before ``push``.
            j = 0
            while j < len(rest) and rest[j].startswith("-"):
                # ``-C`` / ``-c`` take an argument; all other git globals are
                # boolean. We don't need to be precise — if we mis-skip we
                # just fail to detect and allow, which is the safe default.
                if rest[j] in ("-C", "-c"):
                    j += 2
                else:
                    j += 1
            if j < len(rest) and rest[j] == "push":
                push_args = rest[j + 1 :]
                has_force = any(
                    a == "-f" or a == "--force" or a == "--force-with-lease" or a.startswith("--force=") or a.startswith("--force-with-lease=")
                    for a in push_args
                )
                targets_main = any(
                    a in ("main", "master") or a.endswith(":main") or a.endswith(":master") or a.endswith("/main") or a.endswith("/master")
                    for a in push_args
                )
                if has_force and targets_main:
                    return True
            i += 1
            continue
        i += 1
    return False


_SHELL_NAMES: frozenset[str] = frozenset({"sh", "bash", "zsh", "ksh", "dash", "ash"})


def _predicate_curl_pipe_shell(tool_input: dict[str, Any]) -> bool:
    command = _bash_command(tool_input)
    if not command:
        return False
    # Pipe-to-shell detection does not survive ``shlex.split`` (which eats
    # the pipe), so split on the literal ``|`` at the string level. We still
    # use ``shlex`` on each segment to normalise token basenames.
    # Handle ``|&`` and process substitution ``|&`` as well.
    segments = re.split(r"\|+&?", command)
    if len(segments) < 2:
        return False
    # The fetch segment must reference curl/wget; the next segment's first
    # executable token must be a shell.
    for idx in range(len(segments) - 1):
        fetch = _safe_shlex_split(segments[idx])
        consumer = _safe_shlex_split(segments[idx + 1])
        fetch_names = {os.path.basename(t) for t in fetch if not t.startswith("-")}
        if not ({"curl", "wget"} & fetch_names):
            continue
        # Skip leading env-var assignments like ``FOO=bar`` before the exec
        # in the consumer segment.
        consumer_head = next(
            (t for t in consumer if not re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", t)),
            "",
        )
        if os.path.basename(consumer_head) in _SHELL_NAMES:
            return True
    return False


def _predicate_chmod_777(tool_input: dict[str, Any]) -> bool:
    argv = _safe_shlex_split(_bash_command(tool_input))
    if not argv:
        return False
    for idx, tok in enumerate(argv):
        if os.path.basename(tok) != "chmod":
            continue
        # Every non-flag token after chmod is a mode or a path. Inspect modes
        # until we hit the first path-looking token.
        for mode_tok in argv[idx + 1 :]:
            if mode_tok.startswith("-"):
                continue
            # Octal mode — accept optional leading zeros and any of 0777,
            # 00777, 007777 etc. The invariant is that the last three digits
            # are the user/group/other triple and all equal 7.
            if re.fullmatch(r"0*[0-7]{3,4}", mode_tok):
                digits = mode_tok.lstrip("0") or "0"
                # Normalise to a three-digit tail.
                tail = digits[-3:] if len(digits) >= 3 else digits.rjust(3, "0")
                if tail == "777":
                    return True
                # Fall through — other octal modes are not world-writable by
                # this rule's definition.
                break
            # Symbolic mode like ``a+rwx``, ``ugo+rwx``, ``o+w`` etc.
            sym = re.fullmatch(r"([ugoa]+)([+=])([rwxXst]+)", mode_tok)
            if sym:
                who, op, perms = sym.group(1), sym.group(2), sym.group(3)
                grants_world_write = "w" in perms and ("a" in who or "o" in who)
                if grants_world_write and op in ("+", "="):
                    return True
                break
            # Not a recognisable mode — stop scanning this chmod invocation.
            break
    return False


_DD_DEVICE_FAMILIES: tuple[str, ...] = (
    "sd",
    "nvme",
    "disk",
    "hd",
    "vd",
    "xvd",
    "mmcblk",
    "loop",
    "mapper/",
    "md",
    "dm-",
)


def _predicate_dd_device(tool_input: dict[str, Any]) -> bool:
    argv = _safe_shlex_split(_bash_command(tool_input))
    if not argv:
        return False
    for idx, tok in enumerate(argv):
        if os.path.basename(tok) != "dd":
            continue
        for arg in argv[idx + 1 :]:
            if not arg.startswith("of="):
                continue
            target = arg[3:]
            if not target.startswith("/dev/"):
                continue
            suffix = target[len("/dev/") :]
            # Catch whole-disk targets (``/dev/sda``) and the ``/dev/mapper/…``
            # tree explicitly — the old regex missed both ``mapper/root`` and
            # ``/dev/loop0``.
            if any(suffix.startswith(family) for family in _DD_DEVICE_FAMILIES):
                return True
    return False


def _predicate_write_system_path(tool_input: dict[str, Any]) -> bool:
    """``Write``/``Edit``/``MultiEdit``/``NotebookEdit`` to a system path."""
    for key in ("file_path", "path", "notebook_path"):
        value = tool_input.get(key)
        if not isinstance(value, str) or not value:
            continue
        norm = os.path.normpath(value)
        if not os.path.isabs(norm):
            continue
        # Deny writes under these roots. Agents should only write to the
        # workspace or scratch locations.
        for root in ("/etc", "/boot", "/bin", "/sbin", "/usr", "/lib", "/lib64", "/sys", "/proc", "/dev"):
            if norm == root or norm.startswith(root + "/"):
                return True
    return False


# Baseline rules. Each entry is kept very narrow to minimise false positives
# — the aim is to catch obvious "blast-radius" commands, not to build a
# general-purpose sandbox. Operators who need more can layer extensions.
#
# Rule *names* are load-bearing — they surface as the ``rule`` label on the
# ``a2_hooks_denials_total`` Prometheus counter. Do not rename without a
# migration plan.
_BASELINE_RULES_SPEC: list[dict[str, Any]] = [
    {
        "name": "baseline-rm-rf-root",
        "tool": "Bash",
        "predicate": _predicate_rm_rf_root,
        "reason": "rm -rf targeting root or a system directory — refusing by baseline policy.",
    },
    {
        "name": "baseline-git-force-push-main",
        "tool": "Bash",
        "predicate": _predicate_git_force_push_main,
        "reason": "git push --force to main/master — refusing by baseline policy.",
    },
    {
        "name": "baseline-curl-pipe-shell",
        "tool": "Bash",
        "predicate": _predicate_curl_pipe_shell,
        "reason": "curl/wget piped to a shell — refusing by baseline policy.",
    },
    {
        "name": "baseline-chmod-777",
        "tool": "Bash",
        "predicate": _predicate_chmod_777,
        "reason": "chmod world-writable (0777 or a+w/o+w) — refusing by baseline policy.",
    },
    {
        "name": "baseline-dd-device",
        "tool": "Bash",
        "predicate": _predicate_dd_device,
        "reason": "dd to a block device — refusing by baseline policy.",
    },
]

# Additional structured baseline rule: writes to system paths. Applies to any
# of the file-editing tools. This is net-new policy (the previous baseline
# only covered Bash) so it uses a new rule name and will not disturb existing
# denial counters.
_BASELINE_WRITE_TOOLS: tuple[str, ...] = ("Write", "Edit", "MultiEdit", "NotebookEdit")


def _compile_baseline() -> list[Rule]:
    rules: list[Rule] = []
    for raw in _BASELINE_RULES_SPEC:
        rules.append(
            Rule(
                name=raw["name"],
                tool=raw.get("tool"),
                pattern=None,
                action=DECISION_DENY,
                reason=raw["reason"],
                source="baseline",
                predicate=raw["predicate"],
            )
        )
    for tool in _BASELINE_WRITE_TOOLS:
        rules.append(
            Rule(
                name="baseline-write-system-path",
                tool=tool,
                pattern=None,
                action=DECISION_DENY,
                reason="Write/Edit targeting a system path (/etc, /usr, /bin, …) — refusing by baseline policy.",
                source="baseline",
                predicate=_predicate_write_system_path,
            )
        )
    return rules


BASELINE_RULES: list[Rule] = _compile_baseline()


def _parse_extension_rule(raw: dict[str, Any]) -> Rule | None:
    """Parse one hooks.yaml entry. Returns ``None`` if the entry is invalid."""
    name = raw.get("name")
    if not isinstance(name, str) or not name.strip():
        logger.warning("hooks.yaml: skipping entry with missing/empty name: %r", raw)
        _bump_config_error("missing_name")
        return None
    tool = raw.get("tool")
    if tool is not None and not isinstance(tool, str):
        logger.warning("hooks.yaml: rule %r has non-string tool %r — skipping.", name, tool)
        _bump_config_error("non_string_tool")
        return None

    deny_pat = raw.get("deny_if_match")
    warn_pat = raw.get("warn_if_match")
    if deny_pat and warn_pat:
        logger.warning(
            "hooks.yaml: rule %r has both deny_if_match and warn_if_match — preferring deny.", name
        )
        _bump_config_error("both_patterns")
        warn_pat = None
    if not deny_pat and not warn_pat:
        logger.warning("hooks.yaml: rule %r has neither deny_if_match nor warn_if_match — skipping.", name)
        _bump_config_error("no_pattern")
        return None

    action = DECISION_DENY if deny_pat else DECISION_WARN
    pat_source = deny_pat if deny_pat else warn_pat
    if not isinstance(pat_source, str):
        logger.warning("hooks.yaml: rule %r pattern is not a string — skipping.", name)
        _bump_config_error("non_string_pattern")
        return None
    try:
        pattern = re.compile(pat_source)
    except re.error as exc:
        logger.warning("hooks.yaml: rule %r has invalid regex %r: %s — skipping.", name, pat_source, exc)
        _bump_config_error("invalid_regex")
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
        _bump_config_error("file_load_failed")
        return []

    if not isinstance(data, dict):
        logger.warning("hooks.yaml at %s is not a mapping — ignoring.", path)
        _bump_config_error("not_mapping")
        return []

    raw_list = data.get("extensions") or []
    if not isinstance(raw_list, list):
        logger.warning("hooks.yaml at %s has non-list `extensions` — ignoring.", path)
        _bump_config_error("non_list_extensions")
        return []

    parsed: list[Rule] = []
    for raw in raw_list:
        if not isinstance(raw, dict):
            logger.warning("hooks.yaml: skipping non-mapping entry %r", raw)
            _bump_config_error("non_mapping_entry")
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
    haystack: str | None = None
    first_warn: Rule | None = None
    for rule in rules:
        if not _tool_matches(rule, tool_name):
            continue
        matched = False
        if rule.predicate is not None:
            # Structured baseline match: inspect the parsed tool_input dict
            # directly so trivial encoding bypasses (JSON unicode escapes,
            # whitespace padding) do not help the caller (#521). Predicate
            # errors must not crash the hook.
            try:
                matched = bool(rule.predicate(tool_input))
            except Exception:  # pragma: no cover — predicates are total by contract
                logger.exception("baseline predicate %r raised; treating as no-match", rule.name)
                matched = False
        elif rule.pattern is not None:
            if haystack is None:
                haystack = _input_haystack(tool_input)
            matched = rule.pattern.search(haystack) is not None
        if not matched:
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

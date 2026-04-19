"""Env-var interpolation for scheduler prompt bodies (#473).

The scheduler runners (heartbeat, jobs, tasks, triggers, continuations) build
prompt bodies from operator-authored ``.md`` files. Those files are shared
across dev/staging/prod today, so environment-specific values (region,
dashboard host, deployment tag) have to be either hardcoded or hand-edited
per environment — neither ergonomic nor safe against drift.

This module extends the ``{{env.VAR}}`` convention already used by
``harness/webhooks.py`` to scheduler prompt bodies, with two safety knobs:

* ``PROMPT_ENV_ENABLED`` (default ``false``) — master toggle. Fail-safe: when
  unset, prompt interpolation is a no-op and the body passes through
  verbatim. Operators opt in explicitly.

* ``PROMPT_ENV_ALLOWLIST`` — comma-separated env-var **prefixes** that are
  permitted to be interpolated. Any ``{{env.VAR}}`` reference outside the
  allowlist is substituted with an empty string and a warning is logged
  once per (missing-var, body-hash). Default empty — when enabled but
  without an allowlist, the module logs a loud warning at first use and
  still substitutes empty to preserve the fail-closed shape.

The regex matches exactly what ``harness/webhooks.py`` emits so operators
only need one mental model. Missing vars become empty strings (same as the
webhook shape). Scope is intentionally body-only — frontmatter
interpolation, trigger inbound payloads, and structured-field interpolation
are explicitly out of scope.
"""

from __future__ import annotations

import fnmatch
import logging
import os
import re

logger = logging.getLogger(__name__)

_ENV_VAR_RE = re.compile(r"\{\{env\.(\w+)\}\}")

# Re-arm counters (#1035). The original implementation latched the
# warning flags True after the first emission so sustained misconfigs
# logged exactly one line per process lifetime and went silent. We now
# re-emit every N occurrences so operators keep seeing the signal.
_warned_vars: dict[str, int] = {}
_warned_no_allowlist_count = 0
_PROMPT_ENV_REARM_EVERY = int(os.environ.get("PROMPT_ENV_WARN_REARM_EVERY", "500"))

# Optional Prometheus counter surface (#1089). Callers set this to a
# CounterVec with labels (var, result); leaving None keeps the module
# dependency-free.
substitutions_total = None  # type: ignore[assignment]


def _config() -> tuple[bool, list[str]]:
    """Read current env-var config. Done on every call so hot-reload is free."""
    enabled = (os.environ.get("PROMPT_ENV_ENABLED", "") or "").strip().lower() in (
        "1",
        "true",
        "yes",
        "on",
    )
    allowlist_raw = os.environ.get("PROMPT_ENV_ALLOWLIST", "") or ""
    # Split on commas; strip whitespace; drop empties. Each entry is a prefix
    # OR a glob (supports ``*`` and ``?``). Example: ``NYX_*,DEPLOY_ENV``.
    allow = [p.strip() for p in allowlist_raw.split(",") if p.strip()]
    return enabled, allow


def _var_allowed(name: str, allowlist: list[str]) -> bool:
    if not allowlist:
        return False
    for pattern in allowlist:
        # Bare prefix (no glob char) → substring prefix match.
        if "*" not in pattern and "?" not in pattern:
            if name.startswith(pattern):
                return True
        else:
            if fnmatch.fnmatchcase(name, pattern):
                return True
    return False


def resolve_prompt_env(text: str) -> str:
    """Interpolate ``{{env.VAR}}`` references in *text*.

    Returns the original text when ``PROMPT_ENV_ENABLED`` is false. Otherwise
    substitutes allow-listed references with their env-var values (empty
    string when the var itself is unset) and leaves non-allowlisted
    references replaced by empty string with a warning logged once per var.

    Intended call site: each runner's prompt construction path, before
    ``Message(prompt=...)``. Returning a plain string (not a ``(text, errors)``
    tuple) keeps the integration one-liner so runners don't need to grow a
    failure-handling branch.
    """
    global _warned_no_allowlist_count
    enabled, allowlist = _config()
    if not enabled:
        return text
    if not allowlist:
        if _warned_no_allowlist_count % _PROMPT_ENV_REARM_EVERY == 0:
            logger.warning(
                "PROMPT_ENV_ENABLED=true but PROMPT_ENV_ALLOWLIST is empty — every "
                "{{env.VAR}} reference in prompt bodies will be substituted with "
                "an empty string. Set PROMPT_ENV_ALLOWLIST=<comma-separated prefixes "
                "or globs> to enable interpolation. (warn count=%d)",
                _warned_no_allowlist_count + 1,
            )
        _warned_no_allowlist_count += 1

    def _sub(m: re.Match) -> str:
        name = m.group(1)
        if not _var_allowed(name, allowlist):
            count = _warned_vars.get(name, 0)
            if count % _PROMPT_ENV_REARM_EVERY == 0:
                logger.warning(
                    "prompt env interpolation: %r is not on PROMPT_ENV_ALLOWLIST; "
                    "substituting empty string (miss count=%d)",
                    name, count + 1,
                )
            _warned_vars[name] = count + 1
            _bump(name, "denied")
            return ""
        value = os.environ.get(name, "")
        _bump(name, "hit" if value else "missing")
        return value

    return _ENV_VAR_RE.sub(_sub, text)


def _bump(var: str, result: str) -> None:
    """Increment the optional Prometheus counter surface (#1089).

    No-op when ``substitutions_total`` hasn't been wired so this module
    stays dependency-free outside the harness process.
    """
    if substitutions_total is None:
        return
    try:
        substitutions_total.labels(var=var, result=result).inc()
    except Exception:
        pass

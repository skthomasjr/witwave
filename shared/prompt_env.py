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

# Rate-limit warning spam per (var_name) — log once per process lifetime.
_warned_vars: set[str] = set()
_warned_no_allowlist = False


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
    global _warned_no_allowlist
    enabled, allowlist = _config()
    if not enabled:
        return text
    if not allowlist and not _warned_no_allowlist:
        logger.warning(
            "PROMPT_ENV_ENABLED=true but PROMPT_ENV_ALLOWLIST is empty — every "
            "{{env.VAR}} reference in prompt bodies will be substituted with "
            "an empty string. Set PROMPT_ENV_ALLOWLIST=<comma-separated prefixes "
            "or globs> to enable interpolation."
        )
        _warned_no_allowlist = True

    def _sub(m: re.Match) -> str:
        name = m.group(1)
        if not _var_allowed(name, allowlist):
            if name not in _warned_vars:
                logger.warning(
                    "prompt env interpolation: %r is not on PROMPT_ENV_ALLOWLIST; "
                    "substituting empty string",
                    name,
                )
                _warned_vars.add(name)
            return ""
        return os.environ.get(name, "")

    return _ENV_VAR_RE.sub(_sub, text)

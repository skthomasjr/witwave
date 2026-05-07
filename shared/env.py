"""Boundary-safe parsers for runtime configuration env vars.

Every component reads booleans, ints, and floats from the environment, and
every component had been doing so wrong in slightly different ways.
``bool(os.environ.get("X"))`` is the most-common offender — it returns True
for any non-empty string, so ``X=false`` evaluates to True. ``int(os.environ.
get("X"))`` raises ``TypeError`` when the var is unset (None can't be
``int()``-cast). Centralising the parses here gives every component the same
contract and a single place to keep the truthy/falsy vocabulary aligned.

Contract:

- ``parse_bool_env`` accepts a small, case-insensitive vocabulary on each
  side. Truthy: ``1``, ``true``, ``yes``, ``on``, ``y``, ``t``. Falsy: ``0``,
  ``false``, ``no``, ``off``, ``n``, ``f``, ``""``. Anything else raises
  ``ValueError`` so a typo (``METRICS_ENABLED=trrue``) surfaces loudly
  instead of silently flipping to the default.

- ``parse_int_env`` and ``parse_float_env`` return the named default when
  the var is unset or empty; raise ``ValueError`` on a non-numeric string
  (same loud-failure posture).

These helpers do not log — callers may surface the value or the default
themselves once the application logger is configured. Logging at module
import time tends to bypass structured-JSON handlers that are wired in
``main()``.
"""

from __future__ import annotations

import os

__all__ = ["parse_bool_env", "parse_int_env", "parse_float_env"]


_TRUTHY = {"1", "true", "yes", "on", "y", "t"}
_FALSY = {"0", "false", "no", "off", "n", "f", ""}


def parse_bool_env(name: str, default: bool = False) -> bool:
    """Read a boolean flag from the environment.

    Returns ``default`` when the variable is unset. Recognises a small,
    case-insensitive vocabulary on each side; raises ``ValueError`` on
    anything outside the vocabulary so typos fail loudly. The empty
    string is treated as falsy (matches kubectl/helm convention where
    ``X=""`` means "explicitly off").
    """
    raw = os.environ.get(name)
    if raw is None:
        return default
    normalised = raw.strip().lower()
    if normalised in _TRUTHY:
        return True
    if normalised in _FALSY:
        return False
    raise ValueError(
        f"environment variable {name}={raw!r} is not a recognised boolean; "
        f"expected one of {sorted(_TRUTHY | _FALSY)} (case-insensitive)"
    )


def parse_int_env(name: str, default: int) -> int:
    """Read an integer from the environment with a default fallback."""
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        return int(raw.strip())
    except ValueError as exc:
        raise ValueError(f"environment variable {name}={raw!r} is not a valid integer") from exc


def parse_float_env(name: str, default: float) -> float:
    """Read a float from the environment with a default fallback."""
    raw = os.environ.get(name)
    if raw is None or raw.strip() == "":
        return default
    try:
        return float(raw.strip())
    except ValueError as exc:
        raise ValueError(f"environment variable {name}={raw!r} is not a valid float") from exc

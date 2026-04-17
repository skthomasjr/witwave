"""Shared input validation helpers used across backends.

Currently exposes ``parse_max_tokens``, factored out of the duplicated
``max_tokens`` parsing blocks that previously lived in
``a2-gemini/main.py`` (MCP ``tools/call`` handler) and
``a2-gemini/executor.py`` (``AgentExecutor.execute``). See risk #537
(companion issues #460, #428).

The helper is intentionally pure: no I/O, no shared state, no
concurrency concerns. It accepts a raw value, validates it as a
positive integer, and emits a warning log when the value is missing a
valid positive integer. Invalid or non-positive inputs are dropped
(returned as ``None``) so callers can fall back to their own defaults.

a2-claude and a2-codex carry analogous parsing blocks; adopting this
helper there is a follow-up and deliberately out of scope for #537.
"""

from __future__ import annotations

import logging
from typing import Any, Optional

__all__ = ["parse_max_tokens"]


def parse_max_tokens(
    raw: Any,
    *,
    logger: logging.Logger,
    source: str,
    session_id: Optional[str] = None,
) -> Optional[int]:
    """Parse a raw ``max_tokens`` value into a positive ``int`` or ``None``.

    Parameters
    ----------
    raw:
        The raw value from request arguments or metadata. ``None`` is a
        no-op and returns ``None`` without logging.
    logger:
        The caller's logger, used to emit warnings for non-positive or
        invalid inputs. Kept as a parameter so log records are attributed
        to the calling module rather than ``shared.validation``.
    source:
        Short human-readable label identifying the call site in the log
        message (e.g. ``"MCP tools/call"`` or ``"A2A metadata"``).
    session_id:
        Optional session identifier. When provided, it is included in
        the warning messages to aid debugging; omit for contexts where
        no session has been established yet.

    Returns
    -------
    Optional[int]
        The parsed positive integer, or ``None`` if ``raw`` is ``None``,
        not coercible to ``int``, or not strictly positive.
    """
    if raw is None:
        return None

    prefix = f"{source} (session={session_id!r})" if session_id else source

    try:
        parsed = int(raw)
    except (ValueError, TypeError):
        logger.warning("%s: invalid max_tokens %r; ignoring.", prefix, raw)
        return None

    if parsed <= 0:
        logger.warning(
            "%s: max_tokens=%s is non-positive; ignoring.", prefix, parsed
        )
        return None

    return parsed

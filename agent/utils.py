"""Shared utilities for the autonomous agent."""

import re

import yaml

_FRONTMATTER_RE = re.compile(r"^---\s*\n(.*?)\n---\s*\n?(.*)", re.DOTALL)


def parse_frontmatter(raw: str) -> tuple[dict[str, str], str]:
    """Parse YAML-like frontmatter from a markdown string.

    Returns a tuple of ``(fields, body)`` where *fields* is a dict mapping each
    frontmatter key to its string value (leading/trailing whitespace and
    surrounding quotes stripped), and *body* is the content that follows the
    closing ``---`` delimiter, stripped of leading and trailing whitespace.  If
    no frontmatter block is detected the returned dict is empty and *body* is
    the original *raw* string unchanged.
    """
    match = _FRONTMATTER_RE.match(raw)
    if not match:
        return {}, raw

    parsed = yaml.safe_load(match.group(1)) or {}
    fields: dict[str, str] = {k: str(v) for k, v in parsed.items() if v is not None}

    return fields, match.group(2).strip()

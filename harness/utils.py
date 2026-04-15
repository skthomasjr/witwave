"""Shared utilities for the autonomous agent."""

import re

import yaml

_DURATION_RE = re.compile(r"^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$")


def parse_duration(value: str) -> float:
    """Parse a human-readable duration string into total seconds.

    Supports: 30s, 15m, 1h, 1h30m, 1h30m45s (and combinations thereof).
    Raises ValueError if the format is unrecognized.
    """
    value = value.strip()
    m = _DURATION_RE.match(value)
    if not m or not any(m.groups()):
        raise ValueError(f"Unrecognized duration format: {value!r}. Expected e.g. '30s', '15m', '1h', '1h30m'.")
    hours = int(m.group(1) or 0)
    minutes = int(m.group(2) or 0)
    seconds = int(m.group(3) or 0)
    return hours * 3600 + minutes * 60 + seconds

def parse_consensus(value) -> list[str]:
    """Parse the ``consensus`` frontmatter field into a list of glob patterns.

    Only a YAML list is accepted. Absent or empty list means disabled.
    Any non-list value is ignored with an empty list returned.

      consensus: []                    # disabled (default)
      consensus: ["*"]                 # all backends
      consensus: ["claude", "codex*"]  # specific backends (glob supported)
    """
    if isinstance(value, list):
        return [str(p) for p in value if p]
    return []


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

    parsed = yaml.safe_load(match.group(1))
    if not isinstance(parsed, dict):
        parsed = {}
    fields: dict[str, str] = {k: str(v) if v is not None else "" for k, v in parsed.items()}

    return fields, match.group(2).strip()


def parse_frontmatter_raw(raw: str) -> tuple[dict, str]:
    """Like parse_frontmatter but returns field values uncoerced (preserving lists, bools, ints, etc.)."""
    match = _FRONTMATTER_RE.match(raw)
    if not match:
        return {}, raw
    parsed = yaml.safe_load(match.group(1))
    if not isinstance(parsed, dict):
        parsed = {}
    return parsed, match.group(2).strip()

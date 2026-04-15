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

from dataclasses import dataclass


@dataclass
class ConsensusEntry:
    """One participant in a consensus fan-out."""
    backend: str
    model: str | None = None


def parse_consensus(value) -> list[ConsensusEntry]:
    """Parse the ``consensus`` frontmatter field into a list of ConsensusEntry objects.

    Only a YAML list of objects is accepted. Absent or empty list means disabled.

      consensus: []                                  # disabled (default)
      consensus:
        - backend: "*"                               # all backends, default model
        - backend: "claude"
          model: "claude-opus-4-6"
        - backend: "codex*"                          # glob — matches codex, codex-fast, etc.
    """
    if not isinstance(value, list):
        return []
    entries = []
    for item in value:
        if isinstance(item, dict) and item.get("backend"):
            entries.append(ConsensusEntry(
                backend=str(item["backend"]),
                model=str(item["model"]) if item.get("model") else None,
            ))
    return entries


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

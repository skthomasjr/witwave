"""Direct unit tests for harness/utils.parse_duration (#1700).

Covers the duration grammar (`Xs`, `Ym`, `Zh`, and combinations
thereof) used by webhooks.py (delay), continuations.py (delay), and
tasks.py (window-duration). Previously covered only transitively
via test_continuations.py.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_utils_duration.py
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

from utils import parse_duration  # noqa: E402


# ----- single-unit forms -----


@pytest.mark.parametrize(
    "raw,want",
    [
        ("0s", 0.0),
        ("1s", 1.0),
        ("30s", 30.0),
        ("90s", 90.0),
        ("3600s", 3600.0),
    ],
)
def test_seconds_only(raw, want):
    assert parse_duration(raw) == want


@pytest.mark.parametrize(
    "raw,want",
    [
        ("1m", 60.0),
        ("15m", 900.0),
        ("60m", 3600.0),
    ],
)
def test_minutes_only(raw, want):
    assert parse_duration(raw) == want


@pytest.mark.parametrize(
    "raw,want",
    [
        ("1h", 3600.0),
        ("2h", 7200.0),
        ("24h", 86400.0),
    ],
)
def test_hours_only(raw, want):
    assert parse_duration(raw) == want


# ----- combined forms -----


def test_hours_minutes():
    assert parse_duration("1h30m") == 5400.0


def test_hours_minutes_seconds():
    assert parse_duration("1h30m45s") == 5445.0


def test_minutes_seconds():
    assert parse_duration("5m30s") == 330.0


# ----- whitespace tolerance -----


def test_leading_trailing_whitespace_stripped():
    assert parse_duration("  30s  ") == 30.0


# ----- invalid inputs -----


@pytest.mark.parametrize(
    "raw",
    [
        "",                    # empty
        "  ",                  # whitespace only
        "30",                  # no unit
        "30x",                 # unknown unit
        "x30s",                # unit before number
        "abc",                 # non-numeric
        "1.5h",                # decimals not supported
        "1d",                  # days not supported
        "-30s",                # negative not supported
        "30 s",                # internal whitespace
        "1h 30m",              # internal whitespace between groups
    ],
)
def test_invalid_raises_value_error(raw):
    with pytest.raises(ValueError):
        parse_duration(raw)


# ----- contract: returns float -----


def test_returns_numeric_type():
    """parse_duration is annotated `-> float`; in practice it returns
    int since the body uses integer arithmetic (Python promotes to
    float automatically when callers do float arithmetic on it).
    Both types satisfy the call-site contract — accept either, but
    pin the value to a numeric type so a future refactor that returns
    a string or None breaks the test."""
    result = parse_duration("1m")
    assert isinstance(result, (int, float))
    assert result == 60


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

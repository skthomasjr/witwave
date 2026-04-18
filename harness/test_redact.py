"""Unit tests for shared/redact.py pattern matrix (#958).

Covers:
- should_redact() env-var parsing (truthy / falsy / default)
- Each regex pattern has at least one positive sample that redacts
  and one adjacent non-match that stays untouched
- authorization_header preserves the prefix (only the bearer value
  is replaced)
- high-entropy catch-all fires last so shape-specific matches are
  not stomped
- pass-through when LOG_REDACT is disabled (happy path for default
  deployments)
- ReDoS wall-clock bound: redact_text on a worst-case 64 KiB
  input must complete under 0.5s on a developer laptop. Tight
  regex bounds mean the bound is conservative; the point is to
  surface drift toward catastrophic backtracking patterns in a
  future edit.
"""

from __future__ import annotations

import importlib
import sys
import time
from pathlib import Path

import pytest

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


def _reload(monkeypatch, log_redact: str = "true"):
    monkeypatch.setenv("LOG_REDACT", log_redact)
    import redact as _r  # type: ignore
    importlib.reload(_r)
    return _r


# ----- should_redact ---------------------------------------------


@pytest.mark.parametrize("raw,expected", [
    ("true", True), ("TRUE", True), ("1", True),
    ("yes", True), ("on", True), ("True", True),
    ("false", False), ("0", False), ("no", False),
    ("", False), ("  ", False), ("maybe", False),
])
def test_should_redact_truthy_parsing(monkeypatch, raw, expected):
    monkeypatch.setenv("LOG_REDACT", raw)
    import redact as _r  # type: ignore
    importlib.reload(_r)
    assert _r.should_redact() is expected


def test_should_redact_default_is_false(monkeypatch):
    monkeypatch.delenv("LOG_REDACT", raising=False)
    import redact as _r  # type: ignore
    importlib.reload(_r)
    assert _r.should_redact() is False


# ----- redact_text pattern matrix --------------------------------


@pytest.mark.parametrize("sample,should_change", [
    # AWS access keys
    ("AKIAIOSFODNN7EXAMPLE", True),
    ("ASIAZZZZZZZZZZZZZZZZ", True),
    # GitHub tokens
    ("ghp_" + "a" * 40, True),
    ("github_pat_" + "a" * 30, True),
    # Slack
    ("xoxb-1234567890-abcdefghijk", True),
    # OpenAI + Anthropic
    ("sk-abcdefghijklmnop", True),
    ("sk-ant-abcdefghijklmnop", True),
    # JWT three segments
    ("aaaaaaaaaaaa.bbbbbbbbbbbb.cccccccccccc", True),
    # Credit card (16 digits grouped)
    ("4111 1111 1111 1111", True),
    # SSN
    ("123-45-6789", True),
    # Email
    ("user@example.com", True),
])
def test_redact_text_matches_sensitive_samples(monkeypatch, sample, should_change):
    r = _reload(monkeypatch)
    out = r.redact_text(f"prefix {sample} suffix")
    if should_change:
        assert sample not in out, f"{sample!r} should have been redacted; got {out!r}"
        assert r._REDACTED in out
    else:
        assert out == f"prefix {sample} suffix"


@pytest.mark.parametrize("benign", [
    "hello world",
    "this is plain text with numbers 42 and 100",
    "short token abc",  # too short for high-entropy rule
    "not-an-email-user@example",  # missing TLD
])
def test_redact_text_passes_benign_strings(monkeypatch, benign):
    r = _reload(monkeypatch)
    assert r.redact_text(benign) == benign


def test_redact_text_authorization_header_preserves_prefix(monkeypatch):
    r = _reload(monkeypatch)
    out = r.redact_text("Authorization: Bearer abcdef12345")
    # The 'Authorization: ' portion is kept so log readers can see a
    # header was present; only the bearer value is wiped.
    assert out.lower().startswith("authorization:")
    assert "abcdef12345" not in out
    assert r._REDACTED in out


def test_redact_text_passthrough_when_disabled(monkeypatch):
    r = _reload(monkeypatch, log_redact="false")
    # Sensitive-looking sample round-trips untouched when LOG_REDACT is off.
    assert r.redact_text("AKIAIOSFODNN7EXAMPLE") == "AKIAIOSFODNN7EXAMPLE"
    # Empty input short-circuits regardless of LOG_REDACT.
    assert r.redact_text("") == ""


def test_redact_text_empty_short_circuits(monkeypatch):
    r = _reload(monkeypatch)
    assert r.redact_text("") == ""


def test_redact_text_high_entropy_fires_last(monkeypatch):
    """A string that matches BOTH a shape-specific pattern and the
    generic high-entropy catch-all should be fully redacted but not
    double-redacted (no '[REDACTED][REDACTED]' substring)."""
    r = _reload(monkeypatch)
    out = r.redact_text("token=AKIAIOSFODNN7EXAMPLEJUNK")
    assert r._REDACTED in out
    assert r._REDACTED + r._REDACTED not in out


# ----- ReDoS wall-clock bound ------------------------------------


def test_redact_text_worst_case_wall_clock_bound(monkeypatch):
    """Drift guard against catastrophic-backtracking regex edits.

    Feeds a 64 KiB mixed payload through redact_text. Current
    patterns all have explicit length bounds, so this completes in
    tens of milliseconds; any future regex without a bound would
    explode well past the 0.5s ceiling and trip this test.
    """
    r = _reload(monkeypatch)
    # Mixed: repeated near-matches for several patterns to exercise
    # the regex machine without giving any one pattern a clean hit.
    payload = (
        "a" * 32 + "! " + "sk-" + "x" * 1000 + " @ "
        + "AKIA" + "Q" * 15 + " then "
        + "ghp_" + "0" * 100 + " noise "
    ) * 64
    assert len(payload) >= 64 * 1024 // 4  # non-trivial input
    start = time.monotonic()
    _ = r.redact_text(payload)
    elapsed = time.monotonic() - start
    assert elapsed < 0.5, (
        f"redact_text took {elapsed:.3f}s on a worst-case 64 KiB input — "
        f"a recent pattern edit may have introduced catastrophic backtracking."
    )

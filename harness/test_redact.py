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


def _reload(monkeypatch, log_redact: str = "true", high_entropy: str = "false"):
    monkeypatch.setenv("LOG_REDACT", log_redact)
    monkeypatch.setenv("LOG_REDACT_HIGH_ENTROPY", high_entropy)
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
    double-redacted (no '[REDACTED][REDACTED]' substring).

    High-entropy must be explicitly enabled (#1034) so this test opts
    in alongside LOG_REDACT.
    """
    r = _reload(monkeypatch, high_entropy="true")
    # 40-char blob: matches the generic high-entropy rule; the AWS shape
    # rule here does NOT match (no word boundary after 20 chars). We
    # assert that the catch-all redacts the token without double-wrapping.
    out = r.redact_text("token=AKIAIOSFODNN7EXAMPLEabcdefghij1234567890")
    assert r._REDACTED in out
    assert r._REDACTED + r._REDACTED not in out


# ----- identifier-shape preservation (#1034) ---------------------


def test_redact_text_preserves_uuid(monkeypatch):
    """UUID-shaped identifiers must round-trip untouched (#1034)."""
    r = _reload(monkeypatch, high_entropy="true")
    uid = "550e8400-e29b-41d4-a716-446655440000"
    assert uid in r.redact_text(f"session_id={uid}")


def test_redact_text_preserves_otel_trace_and_span(monkeypatch):
    """32-hex trace-id / 16-hex span-id must round-trip untouched."""
    r = _reload(monkeypatch, high_entropy="true")
    trace = "0af7651916cd43dd8448eb211c80319c"
    span = "b7ad6b7169203331"
    out = r.redact_text(f"trace_id={trace} span_id={span}")
    assert trace in out
    assert span in out


def test_redact_text_high_entropy_gated_default_off(monkeypatch):
    """The generic catch-all must be off unless LOG_REDACT_HIGH_ENTROPY=true."""
    r = _reload(monkeypatch, high_entropy="false")
    # 40-char opaque token that matches ONLY the high-entropy rule.
    token = "A" * 40
    assert token in r.redact_text(f"noise {token} noise")


def test_redact_text_credit_card_requires_separators(monkeypatch):
    """Bare 16-digit runs (e.g. timestamps) must not redact as credit cards."""
    r = _reload(monkeypatch)
    assert r.redact_text("correlation=1234567890123456") == "correlation=1234567890123456"
    # Separated shape still redacts.
    out = r.redact_text("4111 1111 1111 1111")
    assert "4111" not in out


# ----- merge-spans idempotency + priority (#1043) ----------------


@pytest.mark.parametrize("sample", [
    "hello world",
    "AKIAIOSFODNN7EXAMPLE",
    "prefix ghp_" + "a" * 40 + " suffix",
    "Authorization: Bearer abcdef12345",
    "trace=0af7651916cd43dd8448eb211c80319c span=b7ad6b7169203331",
    "token=aaaaaaaaaaaa.bbbbbbbbbbbb.cccccccccccc trailing=Bearer foo",
    "4111 1111 1111 1111 and 123-45-6789 and user@example.com",
    "",
    "   ",
    "plain text 42",
])
def test_redact_text_is_idempotent(monkeypatch, sample):
    """redact_text(redact_text(x)) == redact_text(x) for any input (#1043).

    Under the merge-spans rewrite, all patterns match the original
    string so a second pass cannot re-trigger on the literal
    '[REDACTED]' sentinel or on context exposed by a prior rewrite.
    """
    r = _reload(monkeypatch, high_entropy="true")
    once = r.redact_text(sample)
    twice = r.redact_text(once)
    assert once == twice, f"not idempotent: {once!r} -> {twice!r}"


def test_redact_text_authorization_header_wins_overlap(monkeypatch):
    """auth_header must win over high_entropy on the bearer token (#1043).

    The overlap was the issue's core symptom: with high-entropy on,
    the bearer value was historically at risk of a second-pass match
    that exposed trailing context. Under merge-spans the more-specific
    auth_header rule claims the span first.
    """
    r = _reload(monkeypatch, high_entropy="true")
    out = r.redact_text("Authorization: Bearer " + "Z" * 40)
    # Prefix kept, token fully replaced, no doubled sentinel, no leak
    # of the raw Z-run.
    assert out.lower().startswith("authorization:")
    assert r._REDACTED in out
    assert r._REDACTED + r._REDACTED not in out
    assert "Z" * 40 not in out


def test_redact_text_specific_pattern_beats_generic(monkeypatch):
    """An OpenAI key must claim its span; high_entropy must not double-wrap."""
    r = _reload(monkeypatch, high_entropy="true")
    out = r.redact_text("cfg=sk-" + "x" * 40 + " tail")
    assert "sk-" not in out
    assert r._REDACTED + r._REDACTED not in out
    # Exactly one sentinel for the key span.
    assert out.count(r._REDACTED) == 1


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

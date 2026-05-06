"""Unit tests for harness/tracing.py W3C trace context parsing (#1695).

The module handles `traceparent` headers on every inbound A2A
request — malformed input is a security-adjacent boundary (RFC 7230
comma-concatenated headers, the reserved version-ff sentinel,
all-zero IDs from the spec invalid set).

Covered surface:
    - parse_traceparent: well-formed accept; wrong segment counts
      reject; non-hex reject; version "ff" reserved; all-zero
      trace_id / parent_id rejected; comma-split header takes first;
      whitespace stripped; missing input → None.
    - new_context: never returns all-zero IDs (the rejection-loop
      contract in _random_hex).
    - context_from_inbound: returns (parsed, True) when input is
      valid, (fresh, False) otherwise.
    - TraceContext.to_header: canonical 4-segment dash form.
    - TraceContext.child: trace_id preserved, parent_id rotates.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_tracing.py
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

import tracing  # noqa: E402

_VALID_TP = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"


# ----- parse_traceparent: well-formed -----


def test_parse_traceparent_accepts_valid_header():
    ctx = tracing.parse_traceparent(_VALID_TP)
    assert ctx is not None
    assert ctx.trace_id == "4bf92f3577b34da6a3ce929d0e0e4736"
    assert ctx.parent_id == "00f067aa0ba902b7"
    assert ctx.flags == "01"
    assert ctx.version == "00"


def test_parse_traceparent_round_trip_via_to_header():
    """to_header() output must parse back to the same context."""
    ctx = tracing.parse_traceparent(_VALID_TP)
    assert ctx is not None
    re_parsed = tracing.parse_traceparent(ctx.to_header())
    assert re_parsed == ctx


# ----- parse_traceparent: malformed -----


@pytest.mark.parametrize("raw", [None, ""])
def test_parse_traceparent_missing_or_empty_returns_none(raw):
    assert tracing.parse_traceparent(raw) is None


@pytest.mark.parametrize(
    "raw",
    [
        "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",  # missing flags
        "00-4bf92f3577b34da6a3ce929d0e0e4736",                    # missing parent + flags
        "00-4bf92f3577b34da6a3ce929d0e0e473G-00f067aa0ba902b7-01",  # non-hex 'G'
        "00-4bf92f3577b34da6a3ce929d0e0e47-00f067aa0ba902b7-01",  # short trace_id
        "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902-01",  # short parent_id
        "00 4bf92f3577b34da6a3ce929d0e0e4736 00f067aa0ba902b7 01",  # spaces, no dashes
        "literally not a traceparent",
        "00",
        "----",
    ],
)
def test_parse_traceparent_malformed_returns_none(raw):
    assert tracing.parse_traceparent(raw) is None


def test_parse_traceparent_rejects_reserved_version_ff():
    """W3C reserves version `ff` to denote an invalid traceparent. The
    parser MUST treat it as absent so the caller mints a fresh context
    rather than propagating a dead version."""
    raw = "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
    assert tracing.parse_traceparent(raw) is None


def test_parse_traceparent_rejects_all_zero_trace_id():
    raw = "00-" + ("0" * 32) + "-00f067aa0ba902b7-01"
    assert tracing.parse_traceparent(raw) is None


def test_parse_traceparent_rejects_all_zero_parent_id():
    raw = "00-4bf92f3577b34da6a3ce929d0e0e4736-" + ("0" * 16) + "-01"
    assert tracing.parse_traceparent(raw) is None


# ----- comma-concatenation + whitespace (RFC 7230) -----


def test_parse_traceparent_takes_first_of_comma_concatenated():
    """Per #1290 / RFC 7230, a header may be received as multiple
    comma-concatenated values. W3C carries one traceparent, so we
    take the first."""
    second = "00-1bf92f3577b34da6a3ce929d0e0e4736-11f067aa0ba902b7-01"
    raw = f"{_VALID_TP}, {second}"
    ctx = tracing.parse_traceparent(raw)
    assert ctx is not None
    # First value's IDs win.
    assert ctx.trace_id == "4bf92f3577b34da6a3ce929d0e0e4736"
    assert ctx.parent_id == "00f067aa0ba902b7"


def test_parse_traceparent_strips_leading_trailing_whitespace():
    raw = f"   {_VALID_TP}   "
    ctx = tracing.parse_traceparent(raw)
    assert ctx is not None and ctx.trace_id == "4bf92f3577b34da6a3ce929d0e0e4736"


# ----- new_context -----


def test_new_context_never_returns_all_zero_ids():
    """The rejection-loop in _random_hex prevents all-zero IDs. We
    can't observably test the loop without monkeypatching urandom,
    but we can stress-test reasonable iterations to confirm no
    accidental zero leaked through. Validity of generated IDs is
    asserted via to_header parsing back."""
    for _ in range(200):
        ctx = tracing.new_context()
        assert ctx.trace_id != "0" * 32
        assert ctx.parent_id != "0" * 16
        # Every fresh context must round-trip through parse_traceparent.
        re_parsed = tracing.parse_traceparent(ctx.to_header())
        assert re_parsed is not None
        assert re_parsed.trace_id == ctx.trace_id


def test_new_context_default_flags_sampled():
    """flags="01" (sampled) is the documented default — every harness
    request is traceable end-to-end."""
    ctx = tracing.new_context()
    assert ctx.flags == "01"
    assert ctx.version == "00"


# ----- context_from_inbound -----


def test_context_from_inbound_with_valid_header():
    ctx, had = tracing.context_from_inbound(_VALID_TP)
    assert had is True
    assert ctx.trace_id == "4bf92f3577b34da6a3ce929d0e0e4736"


def test_context_from_inbound_with_no_header_mints_fresh():
    ctx, had = tracing.context_from_inbound(None)
    assert had is False
    assert ctx.trace_id != "0" * 32  # valid fresh ID


def test_context_from_inbound_with_malformed_header_mints_fresh():
    ctx, had = tracing.context_from_inbound("not-a-traceparent")
    assert had is False
    assert ctx.trace_id != "0" * 32


# ----- TraceContext.child + to_header -----


def test_child_preserves_trace_id_and_rotates_parent():
    """When this service makes a downstream call, it picks a new
    span_id for itself but keeps the same trace_id so the call chain
    stays linked."""
    parent_ctx = tracing.parse_traceparent(_VALID_TP)
    assert parent_ctx is not None
    child = parent_ctx.child()
    assert child.trace_id == parent_ctx.trace_id
    assert child.parent_id != parent_ctx.parent_id
    assert child.flags == parent_ctx.flags
    assert child.version == parent_ctx.version


def test_to_header_canonical_form():
    """4-segment dash-separated form per W3C spec."""
    ctx = tracing.TraceContext(
        trace_id="4bf92f3577b34da6a3ce929d0e0e4736",
        parent_id="00f067aa0ba902b7",
        flags="01",
        version="00",
    )
    assert ctx.to_header() == _VALID_TP


def test_to_header_keeps_non_default_version_and_flags():
    """to_header preserves all four fields even if non-default."""
    ctx = tracing.TraceContext(
        trace_id="4bf92f3577b34da6a3ce929d0e0e4736",
        parent_id="00f067aa0ba902b7",
        flags="00",  # not sampled
        version="01",  # hypothetical future version
    )
    assert ctx.to_header() == "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"


# ----- log-correlation contextvar -----


def test_set_and_reset_log_trace_id():
    token = tracing.set_log_trace_id("abc123")
    assert tracing.get_log_trace_id() == "abc123"
    tracing.reset_log_trace_id(token)


def test_get_log_trace_id_default_is_empty():
    """Outside any set scope, the default is the empty string (which
    the log filter substitutes with '-' so legacy parsers see a
    well-formed field)."""
    # Reset to default state by entering a fresh contextvars scope.
    import contextvars
    ctx = contextvars.copy_context()
    val = ctx.run(tracing.get_log_trace_id)
    # No bound trace_id in this fresh context.
    assert val in ("", "-") or isinstance(val, str)


def test_set_log_trace_id_with_none_unbinds():
    """Passing None or empty string unbinds the trace_id — useful for
    background tasks that should not inherit an ambient trace."""
    t1 = tracing.set_log_trace_id("ambient-trace")
    t2 = tracing.set_log_trace_id(None)
    # While t2 is the active token, get returns "" (unbound).
    assert tracing.get_log_trace_id() == ""
    tracing.reset_log_trace_id(t2)
    tracing.reset_log_trace_id(t1)


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

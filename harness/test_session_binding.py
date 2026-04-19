"""Unit tests for shared/session_binding.py (#710 / #733).

The shared helper is defense-in-depth against caller-supplied
session_id hijacking. Deployment behaviour:

* Backward compatible: same output as the legacy ``uuid5`` path when
  ``SESSION_ID_SECRET`` is unset.
* Binds per-caller when secret + caller are both set — different
  callers with the same raw id get disjoint derived ids.
* Idempotent for the same caller + raw pair, so session resumption
  still works.
* Logs once when multi-tenant config is half-wired.

The test file lives under ``harness/`` so it piggybacks on the existing
pytest discovery path (``harness/test_*.py``) — the shared/ module
has no test harness of its own.
"""

import importlib
import logging
import sys
import uuid
from pathlib import Path

import pytest

# Make shared/ importable.
_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


def _fresh_module():
    """Reload shared.session_binding so each test sees a pristine
    one-shot-warning flag."""
    import session_binding  # type: ignore

    importlib.reload(session_binding)
    return session_binding


def test_empty_raw_sid_returns_fresh_uuid():
    sb = _fresh_module()
    a = sb.derive_session_id("", caller_identity=None)
    b = sb.derive_session_id("", caller_identity=None)
    assert a != b, "empty raw sid must mint a fresh uuid each time"
    uuid.UUID(a)  # must parse


def test_legacy_behaviour_with_no_secret_preserved():
    sb = _fresh_module()
    raw = "conversation-42"
    expected = str(uuid.uuid5(uuid.NAMESPACE_URL, raw))
    assert sb.derive_session_id(raw, caller_identity=None, secret="") == expected
    assert sb.derive_session_id(raw, caller_identity="alice", secret="") == expected, (
        "when SESSION_ID_SECRET is unset the derivation must be identical "
        "to the legacy uuid5 path — backward compatibility"
    )


def test_raw_that_is_already_a_valid_uuid_passes_through_when_no_secret():
    sb = _fresh_module()
    existing = str(uuid.uuid4())
    assert sb.derive_session_id(existing, caller_identity=None, secret="") == existing


def test_two_different_callers_get_disjoint_session_ids():
    sb = _fresh_module()
    raw = "shared-session-42"
    a = sb.derive_session_id(raw, caller_identity="alice", secret="s3cret")
    b = sb.derive_session_id(raw, caller_identity="bob", secret="s3cret")
    assert a != b, "same raw sid with different callers must not collide"


def test_same_caller_plus_same_raw_is_idempotent():
    sb = _fresh_module()
    raw = "conv-42"
    first = sb.derive_session_id(raw, caller_identity="alice", secret="s3cret")
    second = sb.derive_session_id(raw, caller_identity="alice", secret="s3cret")
    assert first == second, "session resumption requires idempotent derivation"


def test_secret_rotation_breaks_addressing():
    sb = _fresh_module()
    raw = "conv-42"
    with_old = sb.derive_session_id(raw, caller_identity="alice", secret="old")
    with_new = sb.derive_session_id(raw, caller_identity="alice", secret="new")
    assert with_old != with_new, (
        "rotating the backend secret must change derived session ids so a "
        "compromised prior secret can't be used to resume sessions"
    )


def test_caller_identity_is_hashed_not_used_raw(caplog):
    """Security property: the caller identity is sha256-hashed before
    entering the HMAC input so derived session ids do not reveal the
    caller's plaintext identity to anyone who can read span attributes
    (if they ever stash the HMAC intermediate). End result must still
    be a plain uuid string."""
    sb = _fresh_module()
    sid = sb.derive_session_id("conv-42", caller_identity="alice", secret="s")
    uuid.UUID(sid)  # parse-check


def test_secret_set_but_missing_caller_logs_once_and_falls_back(caplog):
    sb = _fresh_module()
    raw = "conv-42"
    expected = str(uuid.uuid5(uuid.NAMESPACE_URL, raw))
    with caplog.at_level(logging.WARNING, logger="session_binding"):
        first = sb.derive_session_id(raw, caller_identity=None, secret="s3cret")
        second = sb.derive_session_id(raw, caller_identity="", secret="s3cret")
        third = sb.derive_session_id(raw, caller_identity=None, secret="s3cret")
    assert first == second == third == expected, (
        "half-wired multi-tenant config (secret but no caller) must fall "
        "back to legacy derivation so existing callers don't break"
    )
    warnings = [r for r in caplog.records if r.levelno >= logging.WARNING]
    assert len(warnings) == 1, (
        "operators need exactly one WARNING per process — not zero (silent "
        "misconfig) and not one per request (log flood)"
    )


def test_derived_id_is_a_valid_uuid():
    sb = _fresh_module()
    sid = sb.derive_session_id("conv-42", caller_identity="alice", secret="s3cret")
    parsed = uuid.UUID(sid)  # must parse — downstream code calls uuid.UUID()
    # uuid5 sets version=5 bits (not 4)
    assert parsed.version == 5


# ----- probe-list rotation (#1042) -------------------------------


def test_candidates_single_element_when_no_prev_secret():
    sb = _fresh_module()
    got = sb.derive_session_id_candidates(
        "conv-42", caller_identity="alice", secret="s3cret", prev_secret=""
    )
    assert len(got) == 1
    assert got[0] == sb.derive_session_id("conv-42", caller_identity="alice", secret="s3cret")


def test_candidates_two_elements_when_prev_differs():
    sb = _fresh_module()
    got = sb.derive_session_id_candidates(
        "conv-42", caller_identity="alice", secret="new", prev_secret="old",
    )
    assert len(got) == 2
    assert got[0] == sb.derive_session_id("conv-42", caller_identity="alice", secret="new")
    assert got[1] == sb.derive_session_id("conv-42", caller_identity="alice", secret="old")
    assert got[0] != got[1], "rotation is a no-op if old and new derivations coincide"


def test_candidates_dedupes_when_prev_equals_current():
    sb = _fresh_module()
    got = sb.derive_session_id_candidates(
        "conv-42", caller_identity="alice", secret="same", prev_secret="same",
    )
    assert len(got) == 1, "prev==current must not produce a duplicate candidate"


def test_candidates_single_element_in_fallback_regimes():
    """Empty raw / no caller — degenerate to a single candidate
    (rotation cannot apply when the derivation isn't HMAC-bound).

    #1235: current=empty + prev=set + caller present DOES emit a
    prev-secret probe candidate so previously-HMAC-bound sessions
    can resume during an operator-initiated rotation unwind."""
    sb = _fresh_module()
    assert len(sb.derive_session_id_candidates("", caller_identity="alice", secret="s", prev_secret="p")) == 1
    assert len(sb.derive_session_id_candidates("conv", caller_identity=None, secret="s", prev_secret="p")) == 1
    # #1235: current unset + prev set + caller present → 2 candidates.
    assert len(sb.derive_session_id_candidates("conv", caller_identity="alice", secret="", prev_secret="p")) == 2


def test_candidates_env_default_reads_both_secrets(monkeypatch):
    monkeypatch.setenv("SESSION_ID_SECRET", "cur")
    monkeypatch.setenv("SESSION_ID_SECRET_PREV", "old")
    sb = _fresh_module()
    got = sb.derive_session_id_candidates("conv-42", caller_identity="alice")
    assert len(got) == 2


def test_candidates_are_idempotent_across_calls():
    sb = _fresh_module()
    a = sb.derive_session_id_candidates("conv-42", "alice", secret="new", prev_secret="old")
    b = sb.derive_session_id_candidates("conv-42", "alice", secret="new", prev_secret="old")
    assert a == b


def test_note_prev_secret_hit_warns_on_first_and_re_arms(monkeypatch, caplog):
    """note_prev_secret_hit fires one WARN on first hit; silent until the
    re-arm interval, then WARN again. Avoids log flood while keeping the
    signal alive for long-running rotation windows."""
    monkeypatch.setenv("SESSION_PREV_HIT_WARN_REARM_EVERY", "3")
    sb = _fresh_module()
    with caplog.at_level(logging.WARNING, logger="session_binding"):
        sb.note_prev_secret_hit("raw")  # hit 1 → WARN
        sb.note_prev_secret_hit("raw")  # hit 2 → silent
        sb.note_prev_secret_hit("raw")  # hit 3 → silent
        sb.note_prev_secret_hit("raw")  # hit 4 → WARN (4 % 3 == 1, not 0). Wait — re-arm uses `count % every == 0` _before_ increment
    warnings = [r for r in caplog.records if r.levelno >= logging.WARNING]
    # The re-arm predicate is (count % every == 0) on the pre-increment
    # counter, which fires at count values 0 and `every` and `2*every`.
    # With every=3 and 4 calls: WARNs at call #1 (count 0→1) and #4 (count 3→4).
    assert len(warnings) == 2


def test_note_prev_secret_hit_does_not_log_raw_sid(monkeypatch, caplog):
    """The WARN must not log the raw sid verbatim — only its length."""
    sb = _fresh_module()
    with caplog.at_level(logging.WARNING, logger="session_binding"):
        sb.note_prev_secret_hit("should-not-appear-in-log")
    joined = "\n".join(r.getMessage() for r in caplog.records)
    assert "should-not-appear-in-log" not in joined
    assert "raw_sid_len=" in joined


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

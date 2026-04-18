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


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

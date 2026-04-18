"""Unit tests for ``shared.conversations.auth_disabled_escape_hatch`` (#965).

The escape hatch decides whether an empty ``CONVERSATIONS_AUTH_TOKEN``
fails closed or silently disables auth. It is read from every /mcp and
/api/traces gate on every backend after #961 brought codex and gemini
into parity with claude. A silent regression in the parse semantics
(say, accepting "False" because of a lower() drift) would re-open the
cross-backend auth gap, and none of the backends currently have a
unit test asserting the semantics. This module lives under harness/
alongside the other shared-surface tests (test_mcp_command_allowlist,
test_session_binding) so pytest discovers them without extra wiring.
"""

from __future__ import annotations

import importlib
import os
import sys
from pathlib import Path

import pytest

# Make shared/ importable (mirrors harness/test_session_binding.py setup).
_SHARED = Path(__file__).resolve().parents[1] / "shared"
if str(_SHARED) not in sys.path:
    sys.path.insert(0, str(_SHARED))


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch) -> None:
    """Drop CONVERSATIONS_AUTH_DISABLED before every test."""
    monkeypatch.delenv("CONVERSATIONS_AUTH_DISABLED", raising=False)


def _load():
    mod = importlib.import_module("conversations")
    return mod.auth_disabled_escape_hatch


def test_unset_is_false() -> None:
    """Absent env var must keep auth required."""
    assert _load()() is False


@pytest.mark.parametrize("val", ["1", "true", "TRUE", "True", "yes", "YES", "on", "On", " true "])
def test_accepted_truthy_values(monkeypatch: pytest.MonkeyPatch, val: str) -> None:
    monkeypatch.setenv("CONVERSATIONS_AUTH_DISABLED", val)
    assert _load()() is True


@pytest.mark.parametrize("val", ["0", "false", "FALSE", "no", "off", "", "2", "enabled", "disable"])
def test_rejected_values_stay_fail_closed(monkeypatch: pytest.MonkeyPatch, val: str) -> None:
    """Anything outside the documented token set must keep auth required — the
    regression this guards against is a future contributor extending the set
    informally ("disable", "enabled") and silently reopening the auth gate."""
    monkeypatch.setenv("CONVERSATIONS_AUTH_DISABLED", val)
    assert _load()() is False


def test_whitespace_stripped(monkeypatch: pytest.MonkeyPatch) -> None:
    """Surrounding whitespace is tolerated so a trailing newline from env
    interpolation in K8s manifests does not defeat the opt-in."""
    monkeypatch.setenv("CONVERSATIONS_AUTH_DISABLED", "\ttrue\n")
    assert _load()() is True


def test_env_mutation_is_observed_per_call(monkeypatch: pytest.MonkeyPatch) -> None:
    """Escape hatch must be read on every call rather than cached at import
    time; otherwise hot-reconfigure (test-only) paths would see a stale value."""
    fn = _load()
    monkeypatch.setenv("CONVERSATIONS_AUTH_DISABLED", "true")
    assert fn() is True
    monkeypatch.setenv("CONVERSATIONS_AUTH_DISABLED", "false")
    assert fn() is False


def test_caller_bool_result_type() -> None:
    """Callers use the value in ``if auth_disabled_escape_hatch():`` which
    assumes a strict bool; guard against accidental "truthy-string" returns."""
    os.environ["CONVERSATIONS_AUTH_DISABLED"] = "true"
    try:
        assert isinstance(_load()(), bool)
    finally:
        del os.environ["CONVERSATIONS_AUTH_DISABLED"]

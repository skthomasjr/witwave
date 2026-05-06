"""Coverage for shared/env.py boundary parsers.

Single source of truth for the truthy/falsy vocabulary, so the table is
deliberately exhaustive — adding a new accepted spelling is a one-line
change here, but a regression breaking ``METRICS_ENABLED=false`` is a
silent feature flip in production.
"""

from __future__ import annotations

import os

import pytest
from env import parse_bool_env, parse_float_env, parse_int_env

# ---------------------------------------------------------------------------
# parse_bool_env — truthy / falsy vocabulary
# ---------------------------------------------------------------------------

@pytest.mark.parametrize(
    "raw",
    ["1", "true", "TRUE", "True", "yes", "YES", "on", "ON", "y", "Y", "t", "T"],
)
def test_parse_bool_env_truthy(monkeypatch, raw):
    monkeypatch.setenv("FLAG", raw)
    assert parse_bool_env("FLAG") is True


@pytest.mark.parametrize(
    "raw",
    ["0", "false", "FALSE", "False", "no", "NO", "off", "OFF", "n", "N", "f", "F", ""],
)
def test_parse_bool_env_falsy(monkeypatch, raw):
    monkeypatch.setenv("FLAG", raw)
    assert parse_bool_env("FLAG") is False


def test_parse_bool_env_unset_uses_default(monkeypatch):
    monkeypatch.delenv("FLAG", raising=False)
    assert parse_bool_env("FLAG") is False
    assert parse_bool_env("FLAG", default=True) is True


def test_parse_bool_env_whitespace_around_value(monkeypatch):
    """Leading/trailing whitespace tolerated — kubectl/helm sometimes add it."""
    monkeypatch.setenv("FLAG", "  true  ")
    assert parse_bool_env("FLAG") is True
    monkeypatch.setenv("FLAG", "\tfalse\n")
    assert parse_bool_env("FLAG") is False


@pytest.mark.parametrize("raw", ["trrue", "yep", "nope", "enabled", "disabled", "2"])
def test_parse_bool_env_unknown_raises(monkeypatch, raw):
    """Typos and "almost-correct" spellings fail loudly, not silently."""
    monkeypatch.setenv("FLAG", raw)
    with pytest.raises(ValueError, match="not a recognised boolean"):
        parse_bool_env("FLAG")


# ---------------------------------------------------------------------------
# parse_int_env / parse_float_env
# ---------------------------------------------------------------------------

def test_parse_int_env_unset_uses_default(monkeypatch):
    monkeypatch.delenv("PORT", raising=False)
    assert parse_int_env("PORT", default=8000) == 8000


def test_parse_int_env_empty_uses_default(monkeypatch):
    monkeypatch.setenv("PORT", "")
    assert parse_int_env("PORT", default=9000) == 9000


def test_parse_int_env_valid(monkeypatch):
    monkeypatch.setenv("PORT", "1234")
    assert parse_int_env("PORT", default=0) == 1234


def test_parse_int_env_invalid_raises(monkeypatch):
    monkeypatch.setenv("PORT", "not-a-number")
    with pytest.raises(ValueError, match="not a valid integer"):
        parse_int_env("PORT", default=0)


def test_parse_float_env_valid(monkeypatch):
    monkeypatch.setenv("TIMEOUT", "1.5")
    assert parse_float_env("TIMEOUT", default=0.0) == 1.5


def test_parse_float_env_unset_uses_default(monkeypatch):
    monkeypatch.delenv("TIMEOUT", raising=False)
    assert parse_float_env("TIMEOUT", default=2.5) == 2.5


def test_parse_float_env_invalid_raises(monkeypatch):
    monkeypatch.setenv("TIMEOUT", "soon")
    with pytest.raises(ValueError, match="not a valid float"):
        parse_float_env("TIMEOUT", default=0.0)


# ---------------------------------------------------------------------------
# Process env isolation guard
# ---------------------------------------------------------------------------

def test_module_import_does_not_read_env():
    """Helpers should only read env when called, not at import time —
    callers control when bad env crashes them.
    """
    # Import-time read would have already happened when this module loaded;
    # the surface check is that re-importing with a bad value doesn't
    # immediately raise.
    os.environ["FLAG"] = "garbage"
    try:
        import importlib

        import env  # shared/env.py — top-level on PYTHONPATH

        importlib.reload(env)  # would raise here if import-time read
    finally:
        os.environ.pop("FLAG", None)

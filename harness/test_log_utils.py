"""Direct unit tests for shared/log_utils.py (#1701).

Covers the contract behaviors:
  - newline / CR sanitisation (#1317): caller-supplied "\n" inside
    line content is escaped to "\\n" so JSONL frames stay parseable
    by tail-followers.
  - basic append: writes the line plus exactly one newline; multiple
    appends produce one frame per call.
  - log rotation: when file exceeds MAX_LOG_BYTES the file rotates
    to <path>.1 and a fresh empty file appears at <path>.
  - per-dir lock isolation (#1201): two distinct dirs have distinct
    lock objects (probes for dir A don't serialise behind probes
    for dir B).
  - writability probe success / failure: successful probe sets state
    to -1 (skip); failed probe sets non-negative state and re-warns
    after _WRITABILITY_REARM_EVERY appends.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_log_utils.py
"""

from __future__ import annotations

import importlib
import os
import sys
import threading
from pathlib import Path
from unittest.mock import patch

import pytest

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))


@pytest.fixture
def lu(monkeypatch, tmp_path):
    """Reload log_utils with predictable env so MAX_LOG_BYTES /
    rotation thresholds are deterministic per test."""
    monkeypatch.setenv("MAX_LOG_BYTES", "1024")  # small so rotation tests fire fast
    monkeypatch.setenv("MAX_LOG_BACKUP_COUNT", "1")
    monkeypatch.setenv("LOG_WRITABILITY_REARM_EVERY", "5")
    import log_utils

    importlib.reload(log_utils)
    # Reset the module-level state dicts so tests don't bleed.
    log_utils._WRITABILITY_STATE.clear()
    log_utils._dir_locks.clear()
    return log_utils


# ----- newline + CR sanitisation (#1317) -----


def test_append_log_escapes_embedded_newlines(lu, tmp_path):
    """A caller passing a string with embedded '\n' must NOT split
    the JSONL frame. Embedded newlines are escaped to '\\n' (literal
    backslash + n); tail-followers then see one frame per write."""
    path = str(tmp_path / "log.jsonl")
    lu._append_log(path, '{"key": "line1\nline2"}')
    content = Path(path).read_text()
    # Exactly one terminal newline character.
    assert content.count("\n") == 1
    # Embedded newline replaced by backslash-n literal.
    assert "line1\\nline2" in content


def test_append_log_escapes_embedded_carriage_returns(lu, tmp_path):
    path = str(tmp_path / "log.jsonl")
    lu._append_log(path, '{"x": "a\rb"}')
    content = Path(path).read_text()
    assert "a\\rb" in content
    # No raw \r in the file.
    assert "\r" not in content


# ----- basic append + multiple frames -----


def test_append_log_writes_one_frame_per_call(lu, tmp_path):
    path = str(tmp_path / "log.jsonl")
    lu._append_log(path, "first")
    lu._append_log(path, "second")
    lu._append_log(path, "third")
    lines = Path(path).read_text().splitlines()
    assert lines == ["first", "second", "third"]


def test_append_log_creates_parent_dir(lu, tmp_path):
    path = str(tmp_path / "nested" / "deeper" / "log.jsonl")
    lu._append_log(path, "hello")
    assert Path(path).exists()
    assert Path(path).read_text() == "hello\n"


# ----- log rotation -----


def test_rotation_fires_when_size_exceeds_max(lu, tmp_path):
    """File rotates to <path>.1 when size exceeds MAX_LOG_BYTES (1024
    in this fixture)."""
    path = str(tmp_path / "log.jsonl")
    # Each line is ~520 bytes (500 'x' + framing) so 3 lines exceed 1024.
    big = "x" * 500
    lu._append_log(path, big)
    lu._append_log(path, big)
    lu._append_log(path, big)
    # After rotation, .1 should exist; main path may exist with the
    # post-rotation tail or be empty depending on fs ordering.
    assert os.path.exists(path + ".1")


def test_rotation_disabled_when_backup_count_zero(monkeypatch, tmp_path):
    """MAX_LOG_BACKUP_COUNT=0 must skip rotation even on oversize files."""
    monkeypatch.setenv("MAX_LOG_BYTES", "100")
    monkeypatch.setenv("MAX_LOG_BACKUP_COUNT", "0")
    import log_utils

    importlib.reload(log_utils)
    log_utils._WRITABILITY_STATE.clear()
    log_utils._dir_locks.clear()

    path = str(tmp_path / "log.jsonl")
    big = "x" * 200
    log_utils._append_log(path, big)
    log_utils._append_log(path, big)
    # No backup file should appear.
    assert not os.path.exists(path + ".1")


# ----- per-dir lock isolation (#1201) -----


def test_get_dir_lock_returns_same_lock_for_same_dir(lu):
    a1 = lu._get_dir_lock("/dir/a")
    a2 = lu._get_dir_lock("/dir/a")
    assert a1 is a2


def test_get_dir_lock_returns_distinct_locks_for_different_dirs(lu):
    a = lu._get_dir_lock("/dir/a")
    b = lu._get_dir_lock("/dir/b")
    assert a is not b
    assert isinstance(a, type(threading.Lock()))


# ----- writability probe -----


def test_check_writability_success_sets_state_minus_one(lu, tmp_path):
    log_dir = str(tmp_path)
    lu._check_writability(log_dir)
    assert lu._WRITABILITY_STATE[log_dir] == -1


def test_check_writability_skips_after_success(lu, tmp_path):
    """Once state is -1, subsequent probes are no-ops (no probe file
    created)."""
    log_dir = str(tmp_path)
    lu._check_writability(log_dir)  # success → state=-1
    # Track if a probe was attempted by patching open().
    real_open = open
    probe_calls = {"n": 0}

    def counting_open(path, *args, **kwargs):
        if str(path).endswith(".writability-probe"):
            probe_calls["n"] += 1
        return real_open(path, *args, **kwargs)

    with patch("builtins.open", counting_open):
        lu._check_writability(log_dir)
    assert probe_calls["n"] == 0  # state=-1 short-circuits


def test_check_writability_failure_sets_nonneg_state(lu):
    """A non-existent / unwritable dir produces state >= 0 after probe."""
    log_dir = "/var/this-dir-doesnt-exist-1701-test"
    lu._check_writability(log_dir)
    state = lu._WRITABILITY_STATE.get(log_dir)
    # State 0 means failure observed and we'll re-warn after REARM_EVERY.
    assert state == 0


def test_check_writability_increments_until_rearm(lu):
    """Failed-probe re-arm: state increments on each subsequent
    append until LOG_WRITABILITY_REARM_EVERY (5 in this fixture),
    at which point it re-probes."""
    log_dir = "/var/this-dir-doesnt-exist-1701-test"
    lu._check_writability(log_dir)
    assert lu._WRITABILITY_STATE[log_dir] == 0
    for _ in range(3):
        lu._check_writability(log_dir)
    # After 4 calls total since failure (the initial + 3 increments),
    # state has incremented but not yet hit the rearm threshold (5).
    assert 0 < lu._WRITABILITY_STATE[log_dir] < 5


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

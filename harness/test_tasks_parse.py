"""Unit tests for harness/tasks.py parse_task_file (#1690).

Exercises the frontmatter-parsing surface of the task dispatcher.
Tasks have richer parse logic than jobs (timezone, days expression,
window-start, window-duration, loop mode, run-once mode), so this
covers more ground than test_jobs_parse.py.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_tasks_parse.py
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

os.environ.setdefault("AGENT_NAME", "test-agent")

import tasks  # noqa: E402


def _write_task(tmpdir: str, name: str, body: str) -> str:
    path = os.path.join(tmpdir, name)
    Path(path).write_text(body)
    return path


# ----- happy paths -----


def test_parse_loop_mode_with_window(tmp_path):
    p = _write_task(
        str(tmp_path),
        "morning.md",
        "---\ndays: mon-fri\nwindow-start: 09:00\nwindow-duration: 1h\n---\nDo morning standup.",
    )
    item = tasks.parse_task_file(p)
    assert item is not None
    assert item.name == "morning"
    assert item.window_start is not None
    assert item.window_end is not None


def test_parse_run_once_mode_no_window_start(tmp_path):
    """Without window-start, the task is run-once (not loop)."""
    p = _write_task(
        str(tmp_path),
        "once.md",
        "---\ndays: '*'\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None
    assert item.window_start is None


# ----- timezone -----


def test_unknown_timezone_returns_none(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\ntimezone: Antarctica/Bogus_TZ\n---\nbody",
    )
    assert tasks.parse_task_file(p) is None


def test_valid_timezone_accepted(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\ntimezone: America/Los_Angeles\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None
    assert str(item.tz) == "America/Los_Angeles"


def test_default_timezone_is_utc(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None
    assert str(item.tz) == "UTC"


# ----- days expression -----


def test_days_with_abbreviations(tmp_path):
    """`mon-fri` is a friendly form translated to the cron `1-5` shape."""
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: mon-fri\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None


def test_invalid_days_expression_returns_none(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: not-a-day\n---\nbody",
    )
    assert tasks.parse_task_file(p) is None


# ----- window-start / window-duration -----


def test_invalid_window_start_returns_none(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\nwindow-start: 25:99\n---\nbody",
    )
    assert tasks.parse_task_file(p) is None


def test_window_duration_without_window_start_returns_none(tmp_path):
    """window-duration is meaningless without window-start; reject."""
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\nwindow-duration: 1h\n---\nbody",
    )
    assert tasks.parse_task_file(p) is None


def test_invalid_window_duration_returns_none(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\nwindow-start: 09:00\nwindow-duration: forever\n---\nbody",
    )
    assert tasks.parse_task_file(p) is None


def test_window_start_underscore_form_accepted(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\nwindow_start: 09:00\nwindow_duration: 30m\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None
    assert item.window_start is not None


# ----- enabled flag -----


@pytest.mark.parametrize("disabled_value", ["false", "no", "off", "n", "0"])
def test_enabled_false_returns_listed_disabled_item(tmp_path, disabled_value):
    """A disabled task is listed for dashboard visibility but not armed.
    Importantly, validation is skipped — a busted days expression on a
    parked task shouldn't be a parse error."""
    p = _write_task(
        str(tmp_path),
        "x.md",
        f"---\nenabled: {disabled_value}\ndays: bogus-days\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None
    assert item.enabled is False


# ----- name + content -----


def test_name_defaults_to_filename(tmp_path):
    p = _write_task(
        str(tmp_path),
        "morning-standup.md",
        "---\ndays: '*'\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None and item.name == "morning-standup"


def test_explicit_name_overrides_filename(tmp_path):
    p = _write_task(
        str(tmp_path),
        "x.md",
        "---\ndays: '*'\nname: friendly\n---\nbody",
    )
    item = tasks.parse_task_file(p)
    assert item is not None and item.name == "friendly"


# ----- robustness -----


def test_unreadable_file_returns_none():
    assert tasks.parse_task_file("/nonexistent/abc.md") is None


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

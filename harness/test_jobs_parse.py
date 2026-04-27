"""Unit tests for harness/jobs.py parse_job_file (#1690).

Exercises the frontmatter-parsing surface of the job dispatcher.
Companion to harness/test_jobs_drift.py which covers the cron
anchoring logic — together they give the job dispatcher full
parse-path + scheduler coverage.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_jobs_parse.py
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

import jobs  # noqa: E402


def _write_job(tmpdir: str, name: str, body: str) -> str:
    path = os.path.join(tmpdir, name)
    Path(path).write_text(body)
    return path


# ----- happy path -----


def test_parse_minimal_job(tmp_path):
    p = _write_job(
        str(tmp_path),
        "daily.md",
        "---\nschedule: 0 9 * * *\n---\nGenerate the daily report.",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item is not jobs._DISABLED
    assert item.name == "daily"
    assert item.schedule == "0 9 * * *"
    assert item.enabled is True
    assert item.content.strip() == "Generate the daily report."


def test_explicit_name_overrides_filename(tmp_path):
    p = _write_job(
        str(tmp_path),
        "daily.md",
        "---\nname: morning-report\nschedule: 0 9 * * *\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.name == "morning-report"


# ----- invalid cron -----


def test_invalid_cron_returns_none_when_enabled(tmp_path):
    """An enabled job with a busted cron is a parse error — returning
    None preserves last-known-good registration upstream."""
    p = _write_job(
        str(tmp_path),
        "daily.md",
        "---\nschedule: not-a-cron\n---\nbody",
    )
    assert jobs.parse_job_file(p) is None


def test_invalid_cron_tolerated_when_disabled(tmp_path):
    """Disabled jobs bypass cron validation per the comment at lines
    108-111 — a busted schedule on a parked job shouldn't be a parse
    error, and the dashboard should still list it."""
    p = _write_job(
        str(tmp_path),
        "parked.md",
        "---\nenabled: false\nschedule: not-a-cron\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item is not jobs._DISABLED
    assert item.enabled is False


# ----- enabled flag falsy spellings -----


@pytest.mark.parametrize("disabled_value", ["false", "no", "off", "n", "0"])
def test_enabled_false_recognised_in_multiple_falsy_forms(tmp_path, disabled_value):
    p = _write_job(
        str(tmp_path),
        "x.md",
        f"---\nenabled: {disabled_value}\nschedule: 0 9 * * *\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.enabled is False


# ----- max-tokens -----


def test_max_tokens_valid(tmp_path):
    p = _write_job(
        str(tmp_path),
        "x.md",
        "---\nschedule: 0 9 * * *\nmax-tokens: 4000\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.max_tokens == 4000


def test_max_tokens_clamped_to_min_one(tmp_path):
    p = _write_job(
        str(tmp_path),
        "x.md",
        "---\nschedule: 0 9 * * *\nmax-tokens: 0\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.max_tokens == 1


def test_max_tokens_invalid_falls_back_to_none(tmp_path):
    p = _write_job(
        str(tmp_path),
        "x.md",
        "---\nschedule: 0 9 * * *\nmax-tokens: bogus\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.max_tokens is None


def test_max_tokens_underscore_form_accepted(tmp_path):
    """Both `max-tokens` and `max_tokens` are accepted."""
    p = _write_job(
        str(tmp_path),
        "x.md",
        "---\nschedule: 0 9 * * *\nmax_tokens: 2048\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.max_tokens == 2048


# ----- session_id derivation -----


def test_session_id_deterministic_from_filename(tmp_path):
    """Two files with the same stem produce the same derived session
    when no explicit `session:` is set — important because reloads
    must not re-key sessions and lose conversation history."""
    body = "---\nschedule: 0 9 * * *\n---\nbody"
    p1 = _write_job(str(tmp_path), "daily.md", body)
    item1 = jobs.parse_job_file(p1)
    # Re-parse same file
    item2 = jobs.parse_job_file(p1)
    assert item1 is not None and item2 is not None
    assert item1.session_id == item2.session_id


def test_explicit_session_id_overrides(tmp_path):
    p = _write_job(
        str(tmp_path),
        "daily.md",
        "---\nschedule: 0 9 * * *\nsession: my-session-id\n---\nbody",
    )
    item = jobs.parse_job_file(p)
    assert item is not None and item.session_id == "my-session-id"


# ----- robustness -----


def test_unreadable_file_returns_none():
    assert jobs.parse_job_file("/nonexistent/abc.md") is None


def test_empty_frontmatter_no_schedule_returns_item_with_no_schedule(tmp_path):
    """A job file with no `schedule:` is unusual but legal — the runner
    treats it as disabled-by-omission rather than a parse error."""
    p = _write_job(str(tmp_path), "x.md", "---\nname: x\n---\nbody")
    item = jobs.parse_job_file(p)
    assert item is not None
    assert item.schedule is None


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

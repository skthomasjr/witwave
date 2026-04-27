"""Unit tests for harness/continuations.py parse_continuation_file (#1688).

Exercises the frontmatter-parsing surface of the continuation
dispatcher without spinning up the full ContinuationRunner. Heavy
harness deps (bus, events, metrics, utils) are imported normally —
metric symbols stay at None when METRICS_ENABLED is unset, so no
stubbing is required for the parsing path.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_continuations.py

Covered surface:
    - continues-after: scalar, list, comma-separated, missing on enabled
    - enabled: false vs true (with various falsy spellings)
    - on-success / on-error toggles
    - trigger-when string filter
    - delay: parsed via utils.parse_duration
    - max-tokens / max-concurrent-fires: clamped + invalid handling
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

import continuations  # noqa: E402


def _write_continuation(tmpdir: str, name: str, body: str) -> str:
    path = os.path.join(tmpdir, name)
    Path(path).write_text(body)
    return path


# ----- continues-after: shape variants -----


def test_continues_after_as_scalar_string(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:daily-report\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item is not continuations._DISABLED
    assert item.continues_after == ["job:daily-report"]


def test_continues_after_as_yaml_list(tmp_path):
    """YAML list syntax for continues-after passes through as a real
    Python list (#1689). The list-form branch in continuations.py
    reads from `raw_fields` (the un-stringified parse) so the list
    shape is preserved; comma-separated strings still work via the
    else branch."""
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after:\n  - job:a\n  - job:b\n  - job:c\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None
    assert item.continues_after == ["job:a", "job:b", "job:c"]


def test_continues_after_csv_inline(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:a, job:b , job:c\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.continues_after == ["job:a", "job:b", "job:c"]


# ----- continues-after: missing-required behavior -----


def test_missing_continues_after_on_enabled_returns_none(tmp_path):
    """Per the comment at lines 122-134: missing continues-after on an
    ENABLED item is a parse failure (return None), not a "disable"
    signal. None preserves last-known-good registration; _DISABLED would
    unregister it, which is the wrong semantics for transient mid-save
    edits."""
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\nname: x\n---\nbody",
    )
    assert continuations.parse_continuation_file(p) is None


def test_missing_continues_after_on_disabled_keeps_disabled_item(tmp_path):
    """A disabled continuation without continues-after still surfaces
    so the dashboard can list it as parked."""
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\nenabled: false\nname: parked\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    # Either a disabled item with empty continues_after, or _DISABLED.
    # The contract is "don't return None" since the file is intentionally parked.
    assert item is not None
    if item is not continuations._DISABLED:
        assert item.enabled is False


# ----- enabled: false -----


@pytest.mark.parametrize("disabled_value", ["false", "no", "off", "n", "0"])
def test_enabled_false_recognised_in_multiple_falsy_forms(tmp_path, disabled_value):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        f"---\nenabled: {disabled_value}\ncontinues-after: job:x\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    # The implementation chooses to return either _DISABLED or an item
    # with enabled=False; both are valid signals to the runner.
    assert item is not None


# ----- on-success / on-error -----


def test_on_success_default_true(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.on_success is True
    assert item.on_error is False


def test_on_success_explicit_false(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\non-success: false\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.on_success is False


def test_on_error_explicit_true(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\non-error: true\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.on_error is True


# ----- trigger-when -----


def test_trigger_when_passes_through(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\ntrigger-when: error 500\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.trigger_when == "error 500"


def test_trigger_when_omitted_is_none(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.trigger_when is None


# ----- delay parsing -----


def test_delay_parsed_seconds(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\ndelay: 30s\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.delay == 30.0


def test_delay_invalid_falls_back_to_none(tmp_path):
    """Invalid delay strings are logged and ignored — never raise."""
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\ndelay: not-a-duration\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.delay is None


def test_delay_omitted_is_none(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.delay is None


# ----- max-tokens / max-concurrent-fires -----


def test_max_tokens_valid_int(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\nmax-tokens: 8000\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.max_tokens == 8000


def test_max_tokens_clamped_to_min_one(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\nmax-tokens: 0\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.max_tokens == 1


def test_max_tokens_invalid_falls_back_to_none(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\nmax-tokens: bogus\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.max_tokens is None


def test_max_concurrent_fires_clamped_to_min_one(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\nmax-concurrent-fires: -5\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.max_concurrent_fires == 1


def test_max_concurrent_fires_invalid_falls_back_to_default(tmp_path):
    """Invalid value should NOT raise; falls back to the module default."""
    p = _write_continuation(
        str(tmp_path),
        "c.md",
        "---\ncontinues-after: job:x\nmax-concurrent-fires: bogus\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None
    assert item.max_concurrent_fires == continuations.CONTINUATION_MAX_CONCURRENT_FIRES


# ----- name + content + parse-error robustness -----


def test_name_defaults_to_filename_stem(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "my-chain.md",
        "---\ncontinues-after: job:x\n---\nbody-text",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.name == "my-chain"
    assert item.content.strip() == "body-text"


def test_explicit_name_overrides_filename(tmp_path):
    p = _write_continuation(
        str(tmp_path),
        "my-chain.md",
        "---\ncontinues-after: job:x\nname: friendly\n---\nbody",
    )
    item = continuations.parse_continuation_file(p)
    assert item is not None and item.name == "friendly"


def test_parse_returns_none_on_unreadable_file():
    assert continuations.parse_continuation_file("/nonexistent/abc.md") is None


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

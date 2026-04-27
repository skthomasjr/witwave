"""Sort-key tests for conversations_proxy (#1728).

Different backends emit ``ts`` in different shapes (claude=ISO 8601 string,
codex=numeric epoch). Merging /trace responses across backends must not raise
``TypeError`` when sorting, and must preserve chronological order across the
mixed types.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_conversations_proxy_ts_sort.py
"""

from __future__ import annotations

from datetime import datetime, timezone

from conversations_proxy import _ts_sort_key


def test_ts_sort_key_string_iso_passthrough() -> None:
    iso = "2026-04-27T12:34:56.789012+00:00"
    assert _ts_sort_key({"ts": iso}) == iso


def test_ts_sort_key_numeric_normalised_to_iso() -> None:
    epoch = 1_000_000_000.0  # 2001-09-09T01:46:40Z
    expected = datetime.fromtimestamp(epoch, tz=timezone.utc).isoformat()
    assert _ts_sort_key({"ts": epoch}) == expected


def test_ts_sort_key_missing_ts_is_empty_string() -> None:
    assert _ts_sort_key({}) == ""
    assert _ts_sort_key({"ts": None}) == ""


def test_mixed_type_sort_does_not_raise_typeerror() -> None:
    # Earlier numeric epoch + later ISO must sort cleanly.
    entries = [
        {"ts": "2026-04-27T00:00:00+00:00", "marker": "iso-2026"},
        {"ts": 1_000_000_000.0, "marker": "epoch-2001"},
        {"ts": "2025-01-01T00:00:00+00:00", "marker": "iso-2025"},
    ]
    entries.sort(key=_ts_sort_key)
    markers = [e["marker"] for e in entries]
    # epoch 2001 → ISO 2001-09-09 sorts before 2025 and 2026.
    assert markers == ["epoch-2001", "iso-2025", "iso-2026"]


def test_unparseable_numeric_falls_back_to_str() -> None:
    # Sentinel huge value that may overflow on some platforms still doesn't raise.
    huge = 1e30
    out = _ts_sort_key({"ts": huge})
    assert isinstance(out, str)

"""Direct unit tests for shared/tool_audit.py (#1752).

Covers:

* ``log_tool_audit`` always stamps ``event_type='tool_audit'`` and never
  raises (the audit-of-last-resort contract).
* The opportunistic rotation-pressure check fires
  ``tool_audit_rotation_pressure_total{reason='size_threshold_exceeded'}``
  exactly when the file-size crosses ``_ROTATION_PRESSURE_BYTES`` AND the
  per-path write count hits a multiple of ``_ROTATION_CHECK_EVERY``.
* ``_rotation_counters`` is path-keyed so two distinct trace-log paths
  track independent counts.
* ``_utf8_byte_length`` falls back to ``len(text)`` on encode failure.
"""

from __future__ import annotations

import asyncio
import importlib
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


class _StubCounter:
    def __init__(self):
        self.calls: list[tuple[dict, float]] = []

    def labels(self, **kw):
        outer = self

        class _Bound:
            def inc(_self, n: float = 1.0):
                outer.calls.append((dict(kw), float(n)))

            def observe(_self, n: float):
                outer.calls.append((dict(kw), float(n)))

        return _Bound()


def _fresh_module():
    """Reload tool_audit so per-test env-var overrides take effect."""
    if "tool_audit" in sys.modules:
        del sys.modules["tool_audit"]
    return importlib.import_module("tool_audit")


def _ctx(ta, path: str, **metric_overrides):
    metrics_kwargs = {
        "tool_audit_entries_total": metric_overrides.get("entries", _StubCounter()),
        "log_entries_total": metric_overrides.get("log_entries", _StubCounter()),
        "log_bytes_total": metric_overrides.get("log_bytes", _StubCounter()),
        "log_write_errors_total": metric_overrides.get("write_errors", _StubCounter()),
        "log_write_errors_by_logger_total": metric_overrides.get("write_errors_by_logger", _StubCounter()),
        "tool_audit_bytes_per_entry": metric_overrides.get("bytes", _StubCounter()),
        "tool_audit_rotation_pressure_total": metric_overrides.get("rotation", _StubCounter()),
    }
    return ta.ToolAuditContext(
        trace_log_path=path,
        labels={"agent": "iris", "agent_id": "claude", "backend": "claude"},
        metrics=ta.ToolAuditMetrics(**metrics_kwargs),
    )


class HappyPathTests(unittest.TestCase):
    def test_stamps_event_type_tool_audit(self):
        ta = _fresh_module()
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "tool-activity.jsonl")
            ctx = _ctx(ta, path)
            asyncio.run(ta.log_tool_audit(ctx, {"tool_name": "Bash", "session": "s"}))
            with open(path) as f:
                row = json.loads(f.readline())
            self.assertEqual(row["event_type"], "tool_audit")
            self.assertEqual(row["tool_name"], "Bash")
            self.assertEqual(row["session"], "s")

    def test_increments_entries_log_bytes(self):
        ta = _fresh_module()
        entries = _StubCounter()
        log_bytes = _StubCounter()
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "trace.jsonl")
            ctx = _ctx(ta, path, entries=entries, log_bytes=log_bytes)
            asyncio.run(ta.log_tool_audit(ctx, {"tool_name": "Bash"}))
        self.assertEqual(len(entries.calls), 1)
        self.assertEqual(entries.calls[0][0]["tool"], "Bash")
        self.assertEqual(len(log_bytes.calls), 1)
        # log_bytes increments by the byte count, not by 1.
        self.assertGreater(log_bytes.calls[0][1], 1)

    def test_no_metrics_does_not_break(self):
        ta = _fresh_module()
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "trace.jsonl")
            ctx = ta.ToolAuditContext(
                trace_log_path=path,
                labels={"agent": "iris", "agent_id": "claude", "backend": "claude"},
                metrics=ta.ToolAuditMetrics(),
            )
            # Must not raise even though every metric field is None.
            asyncio.run(ta.log_tool_audit(ctx, {"tool_name": "Bash"}))


class RaiseSwallowTests(unittest.TestCase):
    def test_write_failure_bumps_error_counters_and_swallows(self):
        ta = _fresh_module()
        write_errors = _StubCounter()
        write_errors_by_logger = _StubCounter()
        # Direct a write at a path whose parent directory does not exist
        # AND can't be created (use a regular file as the parent).
        with tempfile.NamedTemporaryFile() as parent:
            bad_path = os.path.join(parent.name, "not-a-dir", "trace.jsonl")
            ctx = _ctx(
                ta,
                bad_path,
                write_errors=write_errors,
                write_errors_by_logger=write_errors_by_logger,
            )
            # Must not raise.
            asyncio.run(ta.log_tool_audit(ctx, {"tool_name": "Bash"}))
        # log_write_errors_total + by_logger should each have one call.
        self.assertEqual(len(write_errors.calls), 1)
        self.assertEqual(len(write_errors_by_logger.calls), 1)
        self.assertEqual(write_errors_by_logger.calls[0][0]["logger"], "tool_audit")


class RotationPressureTests(unittest.TestCase):
    def setUp(self):
        # Force CHECK_EVERY=1 so a single write triggers the size probe,
        # and a tiny threshold so the assertion is deterministic.
        os.environ["TOOL_ACTIVITY_ROTATION_CHECK_EVERY"] = "1"
        os.environ["TOOL_ACTIVITY_ROTATION_PRESSURE_BYTES"] = "1"

    def tearDown(self):
        for k in (
            "TOOL_ACTIVITY_ROTATION_CHECK_EVERY",
            "TOOL_ACTIVITY_ROTATION_PRESSURE_BYTES",
        ):
            os.environ.pop(k, None)

    def test_pressure_counter_fires_on_threshold_cross(self):
        ta = _fresh_module()
        rotation = _StubCounter()
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "trace.jsonl")
            ctx = _ctx(ta, path, rotation=rotation)
            asyncio.run(ta.log_tool_audit(ctx, {"tool_name": "Bash"}))
        # File size after the first write is >> 1 byte; threshold = 1
        # byte, check_every = 1 — so we must have observed exactly one
        # rotation-pressure call.
        self.assertEqual(len(rotation.calls), 1)
        self.assertEqual(rotation.calls[0][0]["reason"], "size_threshold_exceeded")

    def test_pressure_counter_path_keyed(self):
        # Two distinct paths must each accumulate independent counts.
        os.environ["TOOL_ACTIVITY_ROTATION_CHECK_EVERY"] = "2"
        ta = _fresh_module()
        rotation = _StubCounter()
        with tempfile.TemporaryDirectory() as td:
            p1 = os.path.join(td, "a.jsonl")
            p2 = os.path.join(td, "b.jsonl")
            ctx1 = _ctx(ta, p1, rotation=rotation)
            ctx2 = _ctx(ta, p2, rotation=rotation)
            # Write once to each: each path's counter is at 1 < 2; the
            # rotation probe must NOT fire yet.
            asyncio.run(ta.log_tool_audit(ctx1, {"tool_name": "X"}))
            asyncio.run(ta.log_tool_audit(ctx2, {"tool_name": "Y"}))
            self.assertEqual(rotation.calls, [])
            # Second write to p1 brings its counter to 2 — probe fires.
            asyncio.run(ta.log_tool_audit(ctx1, {"tool_name": "X"}))
            self.assertEqual(len(rotation.calls), 1)
            # Second write to p2 brings ITS counter to 2 — independently.
            asyncio.run(ta.log_tool_audit(ctx2, {"tool_name": "Y"}))
            self.assertEqual(len(rotation.calls), 2)


class Utf8ByteLengthTests(unittest.TestCase):
    def test_ascii(self):
        ta = _fresh_module()
        self.assertEqual(ta._utf8_byte_length("hello"), 5)

    def test_multibyte(self):
        ta = _fresh_module()
        self.assertEqual(ta._utf8_byte_length("héllo"), 6)

    def test_surrogate_fallback(self):
        ta = _fresh_module()
        # Lone surrogate cannot be encoded in strict UTF-8 — verify the
        # except-path returns len(text).
        bad = "\ud800"
        self.assertEqual(ta._utf8_byte_length(bad), 1)


if __name__ == "__main__":
    unittest.main()

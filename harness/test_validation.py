"""Direct unit tests for shared/validation.py (#1757).

Covers parse_max_tokens (raw -> int|None contract + log message format)
and sanitize_model_label (Prometheus-label cardinality clamp).
"""

from __future__ import annotations

import logging
import sys
import unittest
from pathlib import Path

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))

import validation  # type: ignore  # noqa: E402


class _LogCapture(logging.Handler):
    def __init__(self):
        super().__init__()
        self.records: list[logging.LogRecord] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.records.append(record)


def _logger_with_capture(name: str = "validation-test") -> tuple[logging.Logger, _LogCapture]:
    log = logging.getLogger(name)
    log.handlers = []
    log.propagate = False
    log.setLevel(logging.DEBUG)
    cap = _LogCapture()
    log.addHandler(cap)
    return log, cap


class ParseMaxTokensTests(unittest.TestCase):
    def test_none_returns_none_without_logging(self):
        log, cap = _logger_with_capture()
        self.assertIsNone(validation.parse_max_tokens(None, logger=log, source="x"))
        self.assertEqual(cap.records, [])

    def test_positive_int_round_trips(self):
        log, cap = _logger_with_capture()
        self.assertEqual(validation.parse_max_tokens(1024, logger=log, source="x"), 1024)
        self.assertEqual(cap.records, [])

    def test_integer_coercible_string(self):
        log, cap = _logger_with_capture()
        self.assertEqual(validation.parse_max_tokens("32", logger=log, source="x"), 32)
        self.assertEqual(cap.records, [])

    def test_zero_logs_and_returns_none(self):
        log, cap = _logger_with_capture()
        self.assertIsNone(validation.parse_max_tokens(0, logger=log, source="src"))
        self.assertEqual(len(cap.records), 1)
        msg = cap.records[0].getMessage()
        self.assertIn("non-positive", msg)
        self.assertIn("src", msg)

    def test_negative_logs_and_returns_none(self):
        log, cap = _logger_with_capture()
        self.assertIsNone(validation.parse_max_tokens(-5, logger=log, source="src"))
        self.assertEqual(len(cap.records), 1)
        self.assertIn("non-positive", cap.records[0].getMessage())

    def test_non_coercible_string_logs_invalid(self):
        log, cap = _logger_with_capture()
        self.assertIsNone(validation.parse_max_tokens("abc", logger=log, source="src"))
        self.assertEqual(len(cap.records), 1)
        msg = cap.records[0].getMessage()
        self.assertIn("invalid max_tokens", msg)
        self.assertIn("'abc'", msg)

    def test_dict_input_logs_invalid(self):
        log, cap = _logger_with_capture()
        self.assertIsNone(validation.parse_max_tokens({"k": 1}, logger=log, source="src"))
        self.assertEqual(len(cap.records), 1)
        self.assertIn("invalid max_tokens", cap.records[0].getMessage())

    def test_fractional_float_truncates_int(self):
        # int(1.5) = 1; the helper documents this implicitly by coercing
        # via int(raw). A fractional float is therefore NOT rejected.
        log, cap = _logger_with_capture()
        self.assertEqual(
            validation.parse_max_tokens(1.5, logger=log, source="src"),
            1,
        )

    def test_session_id_included_in_log_prefix(self):
        log, cap = _logger_with_capture()
        validation.parse_max_tokens(0, logger=log, source="A2A metadata", session_id="abcd")
        msg = cap.records[0].getMessage()
        # Documented format: "<source> (session=<id>)" with id quoted
        # via repr().
        self.assertIn("A2A metadata", msg)
        self.assertIn("session='abcd'", msg)

    def test_session_id_omitted_when_none(self):
        log, cap = _logger_with_capture()
        validation.parse_max_tokens("bad", logger=log, source="MCP tools/call")
        msg = cap.records[0].getMessage()
        self.assertIn("MCP tools/call", msg)
        self.assertNotIn("session=", msg)


class SanitizeModelLabelTests(unittest.TestCase):
    def test_simple_identifier_round_trips(self):
        self.assertEqual(validation.sanitize_model_label("claude-opus-4"), "claude-opus-4")
        self.assertEqual(validation.sanitize_model_label("gpt-5.1-codex"), "gpt-5.1-codex")
        self.assertEqual(validation.sanitize_model_label("model_v2"), "model_v2")

    def test_none_collapses_to_unknown(self):
        self.assertEqual(validation.sanitize_model_label(None), "unknown")

    def test_empty_string_collapses_to_unknown(self):
        self.assertEqual(validation.sanitize_model_label(""), "unknown")

    def test_unsafe_characters_collapse_to_unknown(self):
        # Slashes, spaces, colons, etc. all collapse.
        for v in ("foo/bar", "model name", "ns:model", "<script>"):
            self.assertEqual(validation.sanitize_model_label(v), "unknown", f"expected {v!r} -> 'unknown'")

    def test_oversize_collapses_to_unknown(self):
        long = "a" * 65
        self.assertEqual(validation.sanitize_model_label(long), "unknown")

    def test_64_char_boundary_passes(self):
        boundary = "a" * 64
        self.assertEqual(validation.sanitize_model_label(boundary), boundary)

    def test_unicode_collapses(self):
        # Non-ASCII characters are outside the regex allow-set.
        self.assertEqual(validation.sanitize_model_label("modèle"), "unknown")


if __name__ == "__main__":
    unittest.main()

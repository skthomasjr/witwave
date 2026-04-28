"""Direct unit tests for shared/conversations._read_jsonl tail-cache (#1754).

Targets the tail-cache state machine (#715, #1296, #1425):

* Unchanged file (same stat_key) is served from cache without a
  filesystem re-read.
* Appended bytes are read from the cached offset only; previously
  parsed entries are not re-tokenised.
* Inode change (rotation via rename) drops the cache and re-parses
  from the top.
* Size-decreased case (truncation) is treated as rotation.
* Same-size mtime-only change (#1296: truncate+rewrite to same size)
  invalidates the cache.
* Partial/torn final line is NOT consumed: the next poll picks it up
  once the trailing newline lands (#1425).
* `_TAIL_CACHE_ENTRY_CAP` clipping behaviour.
"""

from __future__ import annotations

import importlib
import json
import os
import sys
import tempfile
import time
import unittest
from pathlib import Path

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))


def _fresh():
    """Reload conversations so tail cache starts empty per test."""
    for mod in ("conversations",):
        if mod in sys.modules:
            del sys.modules[mod]
    return importlib.import_module("conversations")


def _write_jsonl(path: str, rows: list[dict], mode: str = "w") -> None:
    with open(path, mode) as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")


class TailCacheTests(unittest.TestCase):
    def test_initial_read_populates_cache(self):
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            _write_jsonl(p, [{"ts": "2026-04-01T00:00:00Z", "i": 1}])
            out = c._read_jsonl(p, None, None)
            self.assertEqual(len(out), 1)
            self.assertIn(p, c._TAIL_CACHE)

    def test_unchanged_file_short_circuits(self):
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            _write_jsonl(p, [{"i": 1}])
            c._read_jsonl(p, None, None)
            # Replace open() with a sentinel that would fail if called.
            real_open = open
            calls: list = []
            def _spy_open(*a, **kw):
                calls.append(a)
                return real_open(*a, **kw)
            import builtins
            builtins.open = _spy_open
            try:
                out2 = c._read_jsonl(p, None, None)
            finally:
                builtins.open = real_open
            self.assertEqual(len(out2), 1)
            self.assertEqual(calls, [], "cache hit must not re-open file")

    def test_append_reads_only_new_tail(self):
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            _write_jsonl(p, [{"i": 1}])
            c._read_jsonl(p, None, None)
            cached_offset_first = c._TAIL_CACHE[p][1]
            self.assertGreater(cached_offset_first, 0)
            # Appending a new row.
            _write_jsonl(p, [{"i": 2}], mode="a")
            out = c._read_jsonl(p, None, None)
            self.assertEqual(len(out), 2)
            cached_offset_after = c._TAIL_CACHE[p][1]
            self.assertGreater(cached_offset_after, cached_offset_first)

    def test_truncate_rotates_cache(self):
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            _write_jsonl(p, [{"i": 1}, {"i": 2}, {"i": 3}])
            c._read_jsonl(p, None, None)
            # Truncate file to a smaller size — size shrink path.
            _write_jsonl(p, [{"i": 99}], mode="w")
            out = c._read_jsonl(p, None, None)
            # Cache must invalidate; only the new row visible.
            self.assertEqual(len(out), 1)
            self.assertEqual(out[0]["i"], 99)

    def test_inode_change_rotates_cache(self):
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            _write_jsonl(p, [{"i": 1}])
            c._read_jsonl(p, None, None)
            old_inode = os.stat(p).st_ino
            # Rotate via rename + recreate to swap inode.
            os.rename(p, p + ".1")
            _write_jsonl(p, [{"i": 2}])
            new_inode = os.stat(p).st_ino
            # On most filesystems rename+recreate yields a fresh inode;
            # if the test harness reuses the inode we skip.
            if new_inode == old_inode:  # pragma: no cover — depends on fs
                self.skipTest("filesystem reused inode; not exercising rotation path")
            out = c._read_jsonl(p, None, None)
            self.assertEqual(len(out), 1)
            self.assertEqual(out[0]["i"], 2)

    def test_same_size_different_content_invalidates(self):
        # #1296: truncate-to-same-size + rewrite produces an mtime
        # change with stable size; the stat-key-tuple must catch this.
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            _write_jsonl(p, [{"i": 1}])
            c._read_jsonl(p, None, None)
            old_size = os.path.getsize(p)
            # Sleep to ensure mtime changes on filesystems with 1s
            # resolution. Skip if mtime resolution is too coarse for
            # this test to be deterministic in CI.
            time.sleep(1.05)
            # Rewrite content with the same byte length.
            with open(p, "w") as f:
                # Use a row that produces the same byte count: '{"i": 9}\n'
                f.write('{"i": 9}\n')
            self.assertEqual(os.path.getsize(p), old_size)
            out = c._read_jsonl(p, None, None)
            self.assertEqual(len(out), 1)
            self.assertEqual(out[0]["i"], 9)

    def test_torn_tail_is_not_consumed(self):
        # #1425: a partial line without trailing newline must NOT be
        # consumed. The next poll, after the newline lands, should
        # surface the row.
        c = _fresh()
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "log.jsonl")
            with open(p, "w") as f:
                f.write('{"i": 1}\n')      # complete row
                f.write('{"i": 2')          # partial — no newline yet
            out = c._read_jsonl(p, None, None)
            self.assertEqual(len(out), 1)
            # Now finish the second row.
            with open(p, "a") as f:
                f.write('}\n')
            out = c._read_jsonl(p, None, None)
            self.assertEqual(len(out), 2)
            self.assertEqual(out[1]["i"], 2)

    def test_entry_cap_clips(self):
        c = _fresh()
        # Force a very small cap.
        original_cap = c._TAIL_CACHE_ENTRY_CAP
        c._TAIL_CACHE_ENTRY_CAP = 3
        try:
            with tempfile.TemporaryDirectory() as td:
                p = os.path.join(td, "log.jsonl")
                _write_jsonl(p, [{"i": i} for i in range(10)])
                out = c._read_jsonl(p, None, None)
                # The cache buffer is clipped to 3; output reflects the
                # last three rows.
                self.assertEqual(len(out), 3)
                self.assertEqual([e["i"] for e in out], [7, 8, 9])
        finally:
            c._TAIL_CACHE_ENTRY_CAP = original_cap


if __name__ == "__main__":
    unittest.main()

"""Source-shape regression for gemini's _emit_chunk metric ordering (#1721).

Bug: ``_chunks_emitted`` and ``backend_streaming_events_emitted_total`` were
incremented BEFORE awaiting ``event_queue.enqueue_event``. An enqueue failure
(consumer hung up, queue closed) overstated streaming traffic and tripped the
``_chunks_emitted > 0`` skip on the aggregated final-flush, leaving the
client with neither the dropped chunk nor the recovered aggregate.

Fix: move both increments to AFTER the awaited enqueue, mirroring the codex
fix from #1199.

This test pins the source shape so the bug pattern can't be re-introduced
without tripping CI.
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

_EXECUTOR_PATH = Path(__file__).resolve().parent / "executor.py"


class GeminiEmitChunkMetricOrderTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = _EXECUTOR_PATH.read_text(encoding="utf-8")

    def test_chunks_emitted_increment_after_enqueue(self):
        # The enqueue line must precede the _chunks_emitted += 1 line within
        # _emit_chunk's body.
        m = re.search(
            r"async def _emit_chunk\(text: str\) -> None:(.*?)(?=\n\s{8}\S|\Z)",
            self.source,
            re.DOTALL,
        )
        self.assertIsNotNone(m, "_emit_chunk function body not found")
        body = m.group(1)
        enqueue_idx = body.find("await event_queue.enqueue_event")
        increment_idx = body.find("_chunks_emitted += 1")
        metric_idx = body.find("backend_streaming_events_emitted_total")
        self.assertGreater(enqueue_idx, 0, "enqueue not found in body")
        self.assertGreater(
            increment_idx,
            enqueue_idx,
            "_chunks_emitted += 1 must come AFTER awaited enqueue (#1721)",
        )
        self.assertGreater(
            metric_idx,
            enqueue_idx,
            "backend_streaming_events_emitted_total.inc() must come AFTER awaited enqueue (#1721)",
        )


if __name__ == "__main__":
    unittest.main()

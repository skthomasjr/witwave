"""Regression coverage for the harness ``/validate`` body cap (#1736).

Risk: the previous implementation only checked ``Content-Length`` before
calling ``await request.json()`` (which buffers the entire body via
Starlette's ``request.body()``). A chunked-transfer request with no
``Content-Length`` (or a lying one) bypassed the fast-path gate and
streamed arbitrarily many bytes into memory before the post-read
``len(content.encode())`` check fired. On a memory-constrained pod this
walked toward OOM-kill — a chunked POST of multi-GiB downs the agent.

Fix mirrors the ``/mcp`` defence (#1609 / #1673 / #1674): replace
``request.json()`` with the shared ``read_capped_body`` streaming helper
so the cap is enforced on actual bytes-on-the-wire, not on the caller's
declared length.

The harness ``main`` module pulls in the entire scheduler / executor
surface, so importing it for an end-to-end ASGI test is impractical.
We instead pin the source shape with regex (so the wiring can't
silently regress) and rely on the standalone
``shared/test_mcp_body_cap.py`` for the streaming-overflow contract.
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

_HERE = Path(__file__).resolve().parent
_HARNESS_MAIN = _HERE / "main.py"


class ValidateBodyCapSourceShapeTests(unittest.TestCase):
    """Pin the streaming-cap wiring so a future edit cannot regress."""

    @classmethod
    def setUpClass(cls):
        cls.source = _HARNESS_MAIN.read_text(encoding="utf-8")

    def test_read_capped_body_imported_in_validate_handler(self):
        # The shared helper must be imported inside the /validate
        # handler (or above it) — naming flexible but the import must
        # be present.
        self.assertRegex(
            self.source,
            r"from\s+mcp_body_cap\s+import\s+read_capped_body",
        )

    def test_read_capped_body_called_with_cap(self):
        # The handler must call the streaming helper before any
        # ``request.json()`` / ``json.loads`` parse step. We anchor on
        # the call shape ``await _read_capped_body(request, _MAX)``
        # which mirrors the backend-side pattern.
        pattern = re.compile(r"await\s+_read_capped_body\(\s*request\s*,\s*_MAX\s*\)")
        self.assertRegex(self.source, pattern)

    def test_body_too_large_reason_returns_413(self):
        # On the body_too_large branch the handler must return 413, not
        # silently succeed. Anchor on the (status_code=413, "body
        # exceeds") pair.
        pattern = re.compile(
            r'_reason\s*==\s*"body_too_large"\s*:[^)]*' r"(?:.|\n){0,200}?" r"status_code\s*=\s*413",
        )
        self.assertRegex(self.source, pattern)

    def test_request_json_no_longer_called_in_validate(self):
        # The legacy ``await request.json()`` line in the /validate
        # handler must be gone. We scope by searching for the unique
        # ``"missing 'kind'"`` marker that anchors the /validate
        # handler and confirm no ``await request.json()`` lives in its
        # vicinity (within ~80 lines above the marker).
        m = re.search(r"missing \'kind\'", self.source)
        self.assertIsNotNone(m, "could not anchor on /validate handler")
        # Find the function that contains this line by walking backwards
        # to the nearest ``async def`` declaration.
        before = self.source[: m.start()]
        last_async = before.rfind("async def ")
        self.assertGreaterEqual(last_async, 0)
        handler_chunk = self.source[last_async : m.start()]
        self.assertNotRegex(
            handler_chunk,
            r"await\s+request\.json\(\)",
            "validate handler still calls request.json() — body cap regression",
        )

    def test_issue_cited(self):
        # Future readers must be able to grep #1736 to find this fix.
        self.assertIn("#1736", self.source)


if __name__ == "__main__":
    unittest.main()

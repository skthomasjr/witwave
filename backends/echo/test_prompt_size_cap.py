"""Regression coverage for the echo executor's prompt-size cap (#1650).

Mirrors the codex cycle-1 fix in #1620: a pathological caller could ship
a multi-GB prompt body. Without a hard cap, the prompt would be UTF-8
decoded, reflected through every log/metrics path, and formatted into the
canned-response template — OOM-killing the pod long before any caller
saw a response.

Fix: reject in ``execute()`` when ``len(prompt.encode("utf-8")) >
MAX_PROMPT_BYTES`` (default 1 MiB on echo), bump
``backend_prompt_too_large_total``, and surface a canned A2A text response
mirroring echo's empty-prompt rejection idiom.

Tests assert two boundary cases per the cycle-2 spec:
- 1 MiB + 1 byte prompt is rejected (counter bumps; response carries the
  rejection text).
- 1 KiB prompt is accepted (counter does not move; response quotes the
  prompt as on the happy path).
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

# Set env BEFORE importing main/metrics so init_metrics() declares the
# counter at module import. test_echo.py uses the same idiom.
os.environ.setdefault("METRICS_ENABLED", "1")
os.environ.setdefault("AGENT_NAME", "echo-test")
os.environ.setdefault("AGENT_ID", "echo-test-0")

_HERE = Path(__file__).resolve().parent
if str(_HERE) not in sys.path:
    sys.path.insert(0, str(_HERE))

import main  # noqa: E402
import metrics  # noqa: E402
from starlette.testclient import TestClient  # noqa: E402


@pytest.fixture(scope="module")
def app():
    application = main.build_app()
    main._ready = True
    yield application
    main._ready = False


@pytest.fixture
def client(app):
    return TestClient(app)


def _a2a_send(client: TestClient, text: str, message_id: str = "m1") -> dict:
    payload = {
        "jsonrpc": "2.0",
        "id": "1",
        "method": "message/send",
        "params": {
            "message": {
                "role": "user",
                "parts": [{"kind": "text", "text": text}],
                "messageId": message_id,
                "kind": "message",
            }
        },
    }
    resp = client.post("/", json=payload)
    assert resp.status_code == 200
    return resp.json()


def _counter_value(counter, **extra_labels) -> float:
    """Read a labelled prometheus Counter's current value.

    Mirrors the helper in test_echo.py; duplicated here to keep this
    test file self-contained and importable independently.
    """
    labels = {"agent": "echo-test", "agent_id": "echo-test-0", "backend": "echo"}
    labels.update(extra_labels)
    for metric in counter.collect():
        for sample in metric.samples:
            if not sample.name.endswith("_total") and not sample.name.endswith("_count"):
                continue
            if all(sample.labels.get(k) == v for k, v in labels.items()):
                return sample.value
    return 0.0


# ---------------------------------------------------------------------------
# Default cap is 1 MiB
# ---------------------------------------------------------------------------


def test_default_cap_is_one_mib() -> None:
    """The module-level cap defaults to 1 MiB when MAX_PROMPT_BYTES is unset."""
    import executor  # local import — module already on sys.path

    assert executor._MAX_PROMPT_BYTES == 1024 * 1024


# ---------------------------------------------------------------------------
# Boundary cases mandated by cycle-2 spec
# ---------------------------------------------------------------------------


def test_1mib_plus_one_byte_rejected(client: TestClient) -> None:
    """A prompt of 1 MiB + 1 byte is rejected with the cap error text and
    bumps backend_prompt_too_large_total."""
    before = _counter_value(metrics.backend_prompt_too_large_total)
    oversize = "a" * (1024 * 1024 + 1)
    body = _a2a_send(client, oversize, message_id="m-cap-overflow")
    text = body["result"]["parts"][0]["text"]
    assert "exceeds MAX_PROMPT_BYTES" in text
    assert "1048577" in text  # echoed size in bytes
    after = _counter_value(metrics.backend_prompt_too_large_total)
    assert after - before == 1


def test_1kib_accepted(client: TestClient) -> None:
    """A 1 KiB prompt is accepted (happy path) and does not bump the cap counter."""
    before = _counter_value(metrics.backend_prompt_too_large_total)
    payload = "b" * 1024
    body = _a2a_send(client, payload, message_id="m-cap-ok")
    text = body["result"]["parts"][0]["text"]
    # Happy-path response quotes the prompt and identifies as echo backend.
    assert payload in text
    assert "echo backend" in text.lower()
    after = _counter_value(metrics.backend_prompt_too_large_total)
    assert after - before == 0

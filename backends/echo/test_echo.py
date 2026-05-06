"""Tests for the echo backend.

These tests are intentionally scoped to the A2A contract surface that
every backend must honour:

- Agent card shape (``/.well-known/agent-card.json``)
- Health endpoint lifecycle (503 during startup → 200 once ready)
- A2A ``message/send`` happy path returns a message containing the prompt
- A2A ``message/send`` empty-prompt path returns the empty-prompt hint
  and bumps ``backend_empty_prompts_total``
- ``/metrics`` exposes the common ``backend_*`` baseline after traffic

Run with ``pytest backends/echo/test_echo.py``. Tests use Starlette's
``TestClient`` (httpx under the hood) so no uvicorn binding or port
allocation is needed.

When adding a new backend type, copy this file as the contract-conformance
template and delete the echo-specific response-text assertions — the rest
(agent card, health lifecycle, message/send shape, metrics baseline) should
pass unchanged on any backend that implements the baseline correctly.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

# Ensure METRICS_ENABLED is set BEFORE importing the backend modules, so
# metrics.init_metrics() actually declares the series. Tests run in the
# same process; doing this mid-test would race the module-level _enabled
# capture.
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
    """Build the Starlette app once per module.

    ``main.build_app`` initialises metrics + backend_up; subsequent calls
    are idempotent. The ``_ready`` flag is flipped by main() after uvicorn
    binds, so we flip it ourselves here since TestClient doesn't run uvicorn.
    """
    application = main.build_app()
    main._ready = True  # TestClient bypasses the uvicorn readiness handshake
    yield application
    main._ready = False


@pytest.fixture
def client(app):
    return TestClient(app)


# ---------------------------------------------------------------------------
# Agent card
# ---------------------------------------------------------------------------


def test_agent_card_exposed(client: TestClient) -> None:
    """The A2A SDK auto-serves /.well-known/agent-card.json.

    A2A discovery relies on this path. Every backend must expose a card with
    a name, a URL, capabilities, and at least one skill.
    """
    resp = client.get("/.well-known/agent-card.json")
    assert resp.status_code == 200
    card = resp.json()
    assert card["name"] == "echo-test"
    assert card["url"]
    assert card["capabilities"]["streaming"] is False
    assert len(card["skills"]) >= 1
    assert card["skills"][0]["id"] == "echo"


# ---------------------------------------------------------------------------
# Health lifecycle
# ---------------------------------------------------------------------------


def test_health_ready(client: TestClient) -> None:
    """Once _ready flips, /health returns 200 with the expected shape."""
    resp = client.get("/health")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert body["backend"] == "echo"
    assert body["agent"] == "echo-test"
    assert body["agent_id"] == "echo-test-0"
    assert body["uptime_seconds"] >= 0.0


def test_health_starting(app) -> None:
    """Before _ready, /health returns 503.

    The ``app`` fixture owns the ``_ready`` lifecycle (flips to True on
    entry, False on teardown). We flip it back to True in finally only
    because other tests in the same module share the fixture and expect
    ready state — without this, test order would matter.
    """
    main._ready = False
    try:
        with TestClient(app) as c:
            resp = c.get("/health")
        assert resp.status_code == 503
        assert resp.json()["status"] == "starting"
    finally:
        main._ready = True


# ---------------------------------------------------------------------------
# A2A contract
# ---------------------------------------------------------------------------


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


def test_message_send_happy_path(client: TestClient) -> None:
    body = _a2a_send(client, "hello world", message_id="m-happy")
    assert body["jsonrpc"] == "2.0"
    assert body["id"] == "1"
    result = body["result"]
    assert result["kind"] == "message"
    assert result["role"] == "agent"
    text = result["parts"][0]["text"]
    # Response should quote the prompt and hint at the next step.
    assert "hello world" in text
    assert "echo backend" in text.lower()


def test_message_send_empty_prompt(client: TestClient) -> None:
    body = _a2a_send(client, "   ", message_id="m-empty")
    text = body["result"]["parts"][0]["text"]
    assert "empty prompt" in text.lower()


# ---------------------------------------------------------------------------
# Metrics baseline
# ---------------------------------------------------------------------------


def test_metrics_baseline_declared() -> None:
    """The common backend_* series are declared after init_metrics()."""
    main.build_app()  # idempotent; ensures init_metrics has run
    assert metrics.backend_up is not None
    assert metrics.backend_uptime_seconds is not None
    assert metrics.backend_health_checks_total is not None
    assert metrics.backend_a2a_requests_total is not None
    assert metrics.backend_a2a_request_duration_seconds is not None
    assert metrics.backend_prompt_length_bytes is not None
    assert metrics.backend_empty_prompts_total is not None


def test_metrics_endpoint_exposes_series(client: TestClient) -> None:
    """After traffic, /metrics exposes the expected backend_* series."""
    # Generate a happy-path and an empty-prompt event.
    _a2a_send(client, "metric probe", message_id="m-metric-ok")
    _a2a_send(client, "", message_id="m-metric-empty")
    client.get("/health")

    # Render the Prometheus exposition via the same handler main.py mounts.
    import prometheus_client

    body = prometheus_client.generate_latest().decode("utf-8")

    assert "backend_up" in body
    assert "backend_uptime_seconds" in body
    assert "backend_health_checks_total" in body
    assert "backend_a2a_requests_total" in body
    assert "backend_a2a_request_duration_seconds" in body
    assert "backend_prompt_length_bytes" in body
    assert "backend_empty_prompts_total" in body
    # Label shape: (agent, agent_id, backend) plus per-metric labels.
    assert 'agent="echo-test"' in body
    assert 'agent_id="echo-test-0"' in body
    assert 'backend="echo"' in body


def test_metrics_handler_http_surface() -> None:
    """The metrics_handler HTTP coroutine returns a valid Prometheus exposition.

    main.py wires this handler into the dedicated-port listener via
    ``shared/metrics_server.start_metrics_server``. In-process we invoke
    the handler directly to exercise the Response construction and
    content-type header without binding a second port or requiring a
    pytest-asyncio dependency.
    """
    import asyncio

    import prometheus_client as _pc
    from starlette.requests import Request

    main.build_app()  # idempotent; ensures metrics are initialised

    # metrics_handler doesn't read any request fields, so a minimal scope
    # is sufficient. An empty receive callable never fires.
    scope = {"type": "http", "method": "GET", "path": "/metrics", "headers": []}

    async def receive() -> dict:
        return {"type": "http.request", "body": b"", "more_body": False}

    request = Request(scope, receive)
    response = asyncio.run(main.metrics_handler(request))

    assert response.status_code == 200
    assert response.media_type == _pc.CONTENT_TYPE_LATEST
    body = response.body.decode("utf-8")
    assert "backend_up" in body


def test_metrics_empty_prompt_counter_increments(client: TestClient) -> None:
    before = _counter_value(metrics.backend_empty_prompts_total)
    _a2a_send(client, "   ", message_id="m-empty-count-a")
    _a2a_send(client, "\t\n", message_id="m-empty-count-b")
    after = _counter_value(metrics.backend_empty_prompts_total)
    assert after - before == 2


def test_metrics_a2a_status_labels(client: TestClient) -> None:
    ok_before = _counter_value(metrics.backend_a2a_requests_total, status="ok")
    err_before = _counter_value(metrics.backend_a2a_requests_total, status="error")

    _a2a_send(client, "ping", message_id="m-status-ok")
    _a2a_send(client, "", message_id="m-status-err")

    ok_after = _counter_value(metrics.backend_a2a_requests_total, status="ok")
    err_after = _counter_value(metrics.backend_a2a_requests_total, status="error")

    assert ok_after - ok_before == 1
    assert err_after - err_before == 1


def _counter_value(counter, **extra_labels) -> float:
    """Read a labelled prometheus Counter's current value.

    prometheus_client doesn't expose a direct lookup, so we iterate the
    sample set and match on labels. Matches against Counter's ``_total``
    sample and Histogram's ``_count`` sample only — raw Gauges have no
    suffix and would need a different reader. Falls back to 0.0 if the
    series hasn't been incremented yet.
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

"""Unit tests for harness/metrics_proxy.py + harness/sqlite_task_store.py
pure helpers (#1696).

Covers:
  - metrics_proxy._metrics_url: rewrites a backend app-port URL to
    its metrics-listener URL (app_port + 1000 per #643). Preserves
    scheme, host, optional auth userinfo. Always rewrites path to
    /metrics.
  - sqlite_task_store._retry_on_operational: short exponential-
    backoff retry budget on sqlite3.OperationalError. Returns first
    success; raises after the budget; non-OperationalError raises
    immediately.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_metrics_proxy_helpers.py
"""

from __future__ import annotations

import sqlite3
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))


# ----- metrics_proxy._metrics_url ---------------------------------


def _get_metrics_url():
    # #1701/#1693 lesson: harness/test_hook_decision_event.py installs
    # _AutoMock placeholders in sys.modules for many module names —
    # including `metrics_proxy` and `sqlite_task_store`. If pytest runs
    # that test before this one, plain `import metrics_proxy` returns the
    # mock and our test gets a MagicMock back from _metrics_url(...).
    # Force a real import by evicting any cached mock first.
    import sys
    import importlib
    sys.modules.pop("metrics_proxy", None)
    importlib.invalidate_caches()
    from metrics_proxy import _metrics_url
    return _metrics_url


def test_metrics_url_default_port_80_becomes_1080():
    """No port in URL → 80 default → metrics port 1080."""
    rewrite = _get_metrics_url()
    assert rewrite("http://backend.example") == "http://backend.example:1080/metrics"


def test_metrics_url_explicit_port_8000_becomes_9000():
    rewrite = _get_metrics_url()
    assert rewrite("http://backend:8000") == "http://backend:9000/metrics"


def test_metrics_url_preserves_scheme():
    rewrite = _get_metrics_url()
    assert rewrite("https://backend:8443").startswith("https://")


def test_metrics_url_drops_existing_path():
    """The metrics endpoint is always /metrics — drop any inbound path."""
    rewrite = _get_metrics_url()
    assert rewrite("http://backend:8000/api/v1") == "http://backend:9000/metrics"


def test_metrics_url_preserves_userinfo_with_password():
    rewrite = _get_metrics_url()
    out = rewrite("http://user:pass@backend:8000")
    assert out == "http://user:pass@backend:9000/metrics"


def test_metrics_url_preserves_userinfo_without_password():
    rewrite = _get_metrics_url()
    out = rewrite("http://user@backend:8000")
    assert out == "http://user@backend:9000/metrics"


def test_metrics_url_strips_trailing_slash_before_rewrite():
    """Trailing slash on input is normalised before parsing so the
    derived port stays correct."""
    rewrite = _get_metrics_url()
    assert rewrite("http://backend:8000/") == "http://backend:9000/metrics"


# ----- sqlite_task_store._retry_on_operational --------------------


def _ensure_a2a_stubs():
    """sqlite_task_store imports `from a2a.server.context import
    ServerCallContext` etc. test_hook_decision_event installs `a2a` as
    a single _AutoMock (not a real package), which breaks dotted
    submodule imports. Install proper stubs as separate ModuleType
    entries so dotted imports resolve."""
    import sys
    import types as _t
    needed = {
        "a2a": [],
        "a2a.server": [],
        "a2a.server.context": [("ServerCallContext", type("ServerCallContext", (), {}))],
        "a2a.server.tasks": [],
        "a2a.server.tasks.task_store": [("TaskStore", type("TaskStore", (), {}))],
        "a2a.types": [("Task", type("Task", (), {}))],
    }
    for name, attrs in needed.items():
        existing = sys.modules.get(name)
        # Replace any module that isn't a real ModuleType (e.g.
        # _AutoMock from test_hook_decision_event) with a real one.
        if existing is None or not isinstance(existing, _t.ModuleType):
            sys.modules[name] = _t.ModuleType(name)
        for attr_name, attr_val in attrs:
            setattr(sys.modules[name], attr_name, attr_val)


def _get_retry_fn():
    import sys
    import importlib
    _ensure_a2a_stubs()
    sys.modules.pop("sqlite_task_store", None)
    importlib.invalidate_caches()
    import sqlite_task_store
    return sqlite_task_store, sqlite_task_store._retry_on_operational


def test_retry_returns_first_success_with_no_retry():
    """A function that succeeds on the first attempt should be called
    exactly once and its result returned."""
    _, retry = _get_retry_fn()
    calls = {"n": 0}

    def fn():
        calls["n"] += 1
        return "ok"

    assert retry("op-test", fn) == "ok"
    assert calls["n"] == 1


def test_retry_succeeds_on_subsequent_attempt():
    """OperationalError on first attempt → backoff → retry → success."""
    sts, retry = _get_retry_fn()
    calls = {"n": 0}

    def fn():
        calls["n"] += 1
        if calls["n"] < 2:
            raise sqlite3.OperationalError("database is locked")
        return "recovered"

    # Patch sleep so the test runs instantly. The function uses
    # `time.sleep` from its own `time` import.
    with patch.object(sts.time, "sleep", lambda _s: None):
        assert retry("op-test", fn) == "recovered"
    assert calls["n"] == 2


def test_retry_raises_after_budget_exhausted():
    """Sustained OperationalError exhausts the retry budget then re-raises
    the LAST exception observed."""
    sts, retry = _get_retry_fn()
    calls = {"n": 0}

    def fn():
        calls["n"] += 1
        raise sqlite3.OperationalError(f"locked attempt {calls['n']}")

    with patch.object(sts.time, "sleep", lambda _s: None):
        with pytest.raises(sqlite3.OperationalError) as ei:
            retry("op-test", fn)
    # Budget was exhausted; the last attempt's exception bubbles up.
    assert calls["n"] == sts._RETRY_ATTEMPTS
    assert "locked attempt" in str(ei.value)


def test_retry_propagates_non_operational_error_immediately():
    """A non-OperationalError exception should NOT be retried — bubble
    out on the first attempt so a programming error doesn't silently
    burn the retry budget."""
    sts, retry = _get_retry_fn()
    calls = {"n": 0}

    def fn():
        calls["n"] += 1
        raise ValueError("not a SQLite issue")

    with pytest.raises(ValueError):
        retry("op-test", fn)
    assert calls["n"] == 1


def test_retry_passes_along_return_value_types():
    """Generic over return type — verify a non-string return survives."""
    _, retry = _get_retry_fn()
    obj = {"a": 1, "b": [2, 3]}

    def fn():
        return obj

    assert retry("op-test", fn) is obj


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

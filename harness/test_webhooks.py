"""Unit tests for harness/webhooks.py pure-logic seams (#1688).

Targets the testable surfaces of the outbound webhook dispatcher:
  - SSRF guard (`_validate_url`) — scheme allow-list, loopback alias
    block, private/link-local IP literal block, allowlisted-host bypass
  - Retryable HTTP status classification (`_is_retryable_http`)
  - Event-kind matching (`_events_match`) — completion + hook.decision
    with qualifiers
  - Frontmatter `events:` normalization (`_normalize_events_field`) —
    unknown kinds dropped, qualifiers validated, default fallback

Heavy harness deps (metrics, bus, events, OTel) are imported but
metric symbols stay at None when METRICS_ENABLED is unset, so no
stubbing is required for these pure functions.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_webhooks.py
"""

from __future__ import annotations

import os
import socket
import sys
from pathlib import Path

import pytest

_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

# Pre-import env clean-up: any WEBHOOK_URL_ALLOWED_HOSTS set in the
# parent shell would alter SSRF-guard behavior. Tests that need it
# explicitly patch os.environ, then reload the relevant module
# constants.
os.environ.pop("WEBHOOK_URL_ALLOWED_HOSTS", None)
os.environ.pop("WEBHOOK_ALLOW_LOOPBACK_HOSTS", None)

import webhooks  # noqa: E402

# ----- _is_retryable_http -----


@pytest.mark.parametrize(
    "code,expected",
    [
        (408, True),  # Request Timeout — explicitly retryable
        (429, True),  # Too Many Requests — explicitly retryable
        (500, True),
        (502, True),
        (503, True),
        (504, True),
        (501, True),  # any other 5xx — also retryable per the function
        (507, True),
        (200, False),
        (201, False),
        (301, False),
        (400, False),  # generic 4xx → not retryable
        (401, False),
        (403, False),
        (404, False),
        (418, False),
    ],
)
def test_is_retryable_http_classification(code, expected):
    assert webhooks._is_retryable_http(code) is expected


# ----- _events_match -----


def test_events_match_completion_kind_against_subscriber_with_completion():
    sub = [webhooks.EVENT_KIND_COMPLETION]
    assert webhooks._events_match(sub, webhooks.EVENT_KIND_COMPLETION, None) is True


def test_events_match_completion_against_subscriber_without_it():
    """Completion event must NOT fan out to hook.decision-only subscribers."""
    sub = [webhooks.EVENT_KIND_HOOK_DECISION]
    assert webhooks._events_match(sub, webhooks.EVENT_KIND_COMPLETION, None) is False


def test_events_match_hook_decision_bare_subscriber_matches_any_qualifier():
    """Bare ``hook.decision`` subscribes to all qualifiers (allow/warn/deny)."""
    sub = [webhooks.EVENT_KIND_HOOK_DECISION]
    for qual in ("allow", "warn", "deny"):
        assert webhooks._events_match(sub, webhooks.EVENT_KIND_HOOK_DECISION, qual) is True


def test_events_match_hook_decision_qualified_subscriber_matches_only_its_qualifier():
    """A subscription to ``hook.decision:deny`` must NOT fire on allow/warn."""
    sub = ["hook.decision:deny"]
    assert webhooks._events_match(sub, webhooks.EVENT_KIND_HOOK_DECISION, "deny") is True
    assert webhooks._events_match(sub, webhooks.EVENT_KIND_HOOK_DECISION, "allow") is False
    assert webhooks._events_match(sub, webhooks.EVENT_KIND_HOOK_DECISION, "warn") is False


def test_events_match_unknown_event_kind_returns_false():
    """Defensive default: unknown kinds never match."""
    sub = [webhooks.EVENT_KIND_COMPLETION, webhooks.EVENT_KIND_HOOK_DECISION]
    assert webhooks._events_match(sub, "totally.invented.kind", None) is False


# ----- _normalize_events_field -----


def test_normalize_events_default_when_missing():
    """Empty/None → default to legacy completion-only list."""
    assert webhooks._normalize_events_field(None, "x") == [webhooks.EVENT_KIND_COMPLETION]
    assert webhooks._normalize_events_field("", "x") == [webhooks.EVENT_KIND_COMPLETION]


def test_normalize_events_drops_unknown_kinds():
    """Typos / unknown kinds are dropped with a warning, never silently
    widening the subscription."""
    result = webhooks._normalize_events_field(["completion", "totally.invented", "hook.decision"], "x")
    assert "completion" in result
    assert "hook.decision" in result
    assert "totally.invented" not in result


def test_normalize_events_drops_unsupported_qualifier():
    """hook.decision:<bad> is dropped; hook.decision:deny is kept."""
    result = webhooks._normalize_events_field(["hook.decision:deny", "hook.decision:bogus"], "x")
    assert result == ["hook.decision:deny"]


def test_normalize_events_drops_qualifier_on_completion():
    """`completion:something` has no defined qualifier semantics — drop it."""
    result = webhooks._normalize_events_field(["completion:foo"], "x")
    # Falls back to default since the only entry was dropped.
    assert result == [webhooks.EVENT_KIND_COMPLETION]


def test_normalize_events_falls_back_to_default_when_all_dropped():
    """If every entry was unknown, return the default rather than empty."""
    result = webhooks._normalize_events_field(["bogus", "also.bogus"], "x")
    assert result == [webhooks.EVENT_KIND_COMPLETION]


# ----- _validate_url SSRF guards -----


def test_validate_url_rejects_empty():
    assert webhooks._validate_url("") is not None


def test_validate_url_rejects_unparseable():
    """`_validate_url` is forgiving — many strings parse to (scheme="",
    host=None) rather than raising. The guard catches them as non-http
    schemes / missing host. Either reason is acceptable."""
    err = webhooks._validate_url("http://[::1")  # unbalanced bracket
    assert err is not None


def test_validate_url_rejects_file_scheme():
    err = webhooks._validate_url("file:///etc/passwd")
    assert err is not None and "scheme" in err


def test_validate_url_rejects_gopher_scheme():
    err = webhooks._validate_url("gopher://example.com/")
    assert err is not None and "scheme" in err


def test_validate_url_rejects_no_host():
    err = webhooks._validate_url("http://")
    assert err is not None


def test_validate_url_rejects_loopback_alias():
    """`localhost` (and aliases) MUST be blocked when not in
    WEBHOOK_URL_ALLOWED_HOSTS — otherwise an attacker who controls a
    redirect or template can reach the harness's own loopback services."""
    err = webhooks._validate_url("http://localhost:8080/")
    assert err is not None


def test_validate_url_rejects_loopback_ipv4_literal():
    err = webhooks._validate_url("http://127.0.0.1/")
    assert err is not None


def test_validate_url_rejects_loopback_ipv6_literal():
    err = webhooks._validate_url("http://[::1]/")
    assert err is not None


def test_validate_url_rejects_link_local():
    """169.254.0.0/16 (cloud-metadata + link-local) MUST be blocked."""
    err = webhooks._validate_url("http://169.254.169.254/latest/meta-data/")
    assert err is not None


def test_validate_url_rejects_rfc1918_private():
    err = webhooks._validate_url("http://10.0.0.5/")
    assert err is not None


@pytest.mark.parametrize(
    "url",
    [
        "http://example.com/path",
        "https://example.com/path",
        "https://example.com:8443/api",
    ],
)
def test_validate_url_accepts_public_https(url, monkeypatch):
    """A name resolving to a public IP should pass.

    We patch socket.getaddrinfo to return a known-public IP so the test
    is deterministic regardless of DNS state on the runner."""
    monkeypatch.setattr(
        socket,
        "getaddrinfo",
        lambda *a, **kw: [(socket.AF_INET, socket.SOCK_STREAM, 0, "", ("93.184.216.34", 0))],
    )
    assert webhooks._validate_url(url) is None


def test_validate_url_rejects_resolved_private_ip(monkeypatch):
    """A public-looking hostname that resolves to a private IP MUST still
    be blocked — that's the canonical SSRF-via-DNS case."""
    monkeypatch.setattr(
        socket,
        "getaddrinfo",
        lambda *a, **kw: [(socket.AF_INET, socket.SOCK_STREAM, 0, "", ("10.0.0.5", 0))],
    )
    err = webhooks._validate_url("http://attacker-controlled.example.com/")
    assert err is not None
    assert "10.0.0.5" in err


# ----- _parse_list_field (used in events normalization) -----


def test_parse_list_field_handles_string_csv():
    """Comma-separated string → list (a common YAML inline form)."""
    out = webhooks._parse_list_field("a, b , c")
    assert out == ["a", "b", "c"]


def test_parse_list_field_handles_existing_list():
    out = webhooks._parse_list_field(["a", "b"])
    assert out == ["a", "b"]


def test_parse_list_field_handles_empty():
    assert webhooks._parse_list_field("") == []
    assert webhooks._parse_list_field([]) == []


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

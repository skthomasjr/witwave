"""Unit tests for backends/claude/mcp_shape.validate_mcp_servers_shape (#1051).

Lives under harness/ to piggyback on the existing pytest discovery
path. Imports only the small validator module; no Agents SDK / OTel
dependencies pulled in.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

# Make backends/claude importable.
_CLAUDE = Path(__file__).resolve().parents[1] / "backends" / "claude"
sys.path.insert(0, str(_CLAUDE))

from mcp_shape import validate_mcp_servers_shape  # type: ignore  # noqa: E402


def test_empty_input_returns_empty_pair():
    valid, rejected = validate_mcp_servers_shape({})
    assert valid == {}
    assert rejected == []


def test_non_dict_input_returns_empty_pair():
    """Defensive: caller may feed us something non-dict; degrade gracefully."""
    valid, rejected = validate_mcp_servers_shape([1, 2, 3])  # type: ignore[arg-type]
    assert valid == {}
    assert rejected == []


def test_stdio_entry_accepted():
    valid, rejected = validate_mcp_servers_shape(
        {"kube": {"command": "/usr/local/bin/mcp-kubernetes"}}
    )
    assert "kube" in valid
    assert rejected == []


def test_http_entry_accepted():
    valid, rejected = validate_mcp_servers_shape(
        {"helm": {"url": "http://mcp-helm.svc:8000"}}
    )
    assert "helm" in valid
    assert rejected == []


def test_entry_value_not_dict_rejected():
    valid, rejected = validate_mcp_servers_shape({"bad": "just a string"})
    assert valid == {}
    assert len(rejected) == 1
    assert rejected[0][0] == "bad"
    assert "not a JSON object" in rejected[0][1]


def test_entry_with_both_command_and_url_rejected():
    valid, rejected = validate_mcp_servers_shape(
        {"split-brain": {"command": "foo", "url": "http://bar"}}
    )
    assert valid == {}
    assert len(rejected) == 1
    assert "both" in rejected[0][1]


def test_entry_with_neither_command_nor_url_rejected():
    valid, rejected = validate_mcp_servers_shape({"nothing": {"env": {}}})
    assert valid == {}
    assert len(rejected) == 1
    assert "neither" in rejected[0][1]


@pytest.mark.parametrize("cmd", ["", "   ", 42, None, ["ls"]])
def test_non_string_or_empty_command_rejected(cmd):
    valid, rejected = validate_mcp_servers_shape({"tool": {"command": cmd}})
    assert valid == {}
    assert len(rejected) == 1
    assert "command" in rejected[0][1]


@pytest.mark.parametrize("url", ["", "   ", 42, None, "not-a-url", "://nohost"])
def test_malformed_url_rejected(url):
    valid, rejected = validate_mcp_servers_shape({"tool": {"url": url}})
    assert valid == {}
    assert len(rejected) == 1
    assert "url" in rejected[0][1].lower() or "'url'" in rejected[0][1]


def test_partial_acceptance_mixed_entries():
    """Good entries land in valid; bad entries accumulate in rejected."""
    valid, rejected = validate_mcp_servers_shape(
        {
            "good-stdio": {"command": "/bin/true"},
            "good-http": {"url": "https://ok.example/"},
            "bad-shape": {"url": "no-scheme"},
            "bad-type": "just a string",
            "bad-both": {"command": "x", "url": "http://y"},
        }
    )
    assert set(valid) == {"good-stdio", "good-http"}
    assert {name for name, _ in rejected} == {"bad-shape", "bad-type", "bad-both"}


def test_validator_does_not_mutate_input():
    """Pure function: the input dict must be returned unchanged."""
    src = {"good": {"command": "/bin/true"}, "bad": "string"}
    before = str(src)
    validate_mcp_servers_shape(src)
    assert str(src) == before


def test_command_with_only_whitespace_rejected():
    valid, rejected = validate_mcp_servers_shape({"tool": {"command": "\t  \n"}})
    assert valid == {}
    assert len(rejected) == 1


def test_url_missing_netloc_rejected():
    """`file:///abs/path` parses but has no netloc — reject for MCP transport."""
    valid, rejected = validate_mcp_servers_shape({"tool": {"url": "file:///abs/path"}})
    assert valid == {}
    assert len(rejected) == 1
    assert "host" in rejected[0][1]


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

"""Unit tests for shared/mcp_command_allowlist.py (#797).

These tests seed the allow-list via the function's explicit
``allowed`` + ``prefixes`` parameters so the outcome is independent
of the process's environment at runtime — which matters for the
hotter deployment paths where the shared helper is also imported by
a backend under test.
"""

import sys
from pathlib import Path

import pytest

_SHARED = Path(__file__).resolve().parents[1] / "shared"
sys.path.insert(0, str(_SHARED))

from mcp_command_allowlist import mcp_command_allowed, mcp_command_args_safe  # type: ignore

BASELINE = frozenset({"mcp-kubernetes", "mcp-helm", "python3", "node", "uv"})
PREFIXES = ("/home/agent/mcp-bin/", "/usr/local/bin/")


def ok(cmd: str):
    return mcp_command_allowed(cmd, allowed=BASELINE, prefixes=PREFIXES)


# -------- accept cases --------


@pytest.mark.parametrize("cmd", [
    "mcp-kubernetes",            # bare basename in allow-list
    "python3",                   # bare basename in allow-list
    "/home/agent/mcp-bin/foo",   # under allowed prefix
    "/usr/local/bin/anything",   # under allowed prefix
    "./mcp-kubernetes",          # basename extraction path
])
def test_accepted(cmd):
    allowed, reason = ok(cmd)
    assert allowed, f"{cmd!r} should be accepted, got reason={reason!r}"


# -------- reject cases --------


@pytest.mark.parametrize("cmd,want_reason", [
    ("/bin/sh", "absolute_not_on_prefix"),           # classic RCE vector
    ("/bin/bash", "absolute_not_on_prefix"),
    ("/usr/bin/curl", "absolute_not_on_prefix"),     # prefix is /usr/local/bin/, not /usr/bin/
    # #862 regression guard: an absolute path outside any allowed prefix
    # must be rejected even if the basename matches the bare-name
    # allow-list — otherwise /tmp/attacker/mcp-kubernetes would be
    # accepted as "basename_allowed".
    ("/opt/custom/mcp-helm", "absolute_not_on_prefix"),
    ("/tmp/attacker/mcp-kubernetes", "absolute_not_on_prefix"),
    ("sh", "basename_not_allowed"),
    ("", "empty"),
    ("   ", "empty"),
])
def test_rejected_with_reason(cmd, want_reason):
    allowed, reason = ok(cmd)
    assert not allowed, f"{cmd!r} should be rejected"
    assert reason == want_reason, f"{cmd!r} want reason={want_reason!r}, got {reason!r}"


@pytest.mark.parametrize("cmd", [None, 123, ["python3"], {"cmd": "python3"}])
def test_non_string_rejected(cmd):
    allowed, reason = ok(cmd)  # type: ignore[arg-type]
    assert not allowed
    assert reason == "non_string"


# -------- reason enum stability (used as metric label) --------


def test_reason_is_one_of_a_stable_enum():
    """Every call must return a ``reason`` string drawn from a small
    stable set so the ``backend_mcp_command_rejected_total{reason=...}``
    metric label never explodes. Guard rail for future edits."""
    STABLE = {
        "non_string",
        "empty",
        "absolute_prefix",
        "basename_allowed",
        "absolute_not_on_prefix",
        "basename_not_allowed",
    }
    for sample in ("python3", "/bin/sh", "", None, "./mcp-kubernetes", 42):
        _, reason = mcp_command_allowed(sample, allowed=BASELINE, prefixes=PREFIXES)  # type: ignore[arg-type]
        assert reason in STABLE, reason


# -------- prefix semantics --------


def test_prefix_is_literal_not_dir():
    """The prefix match is a string-prefix, so a crafted input that only
    SHARES an initial substring with a prefix must still be rejected —
    e.g. /home/agent/mcp-bin-evil/... must not match the
    /home/agent/mcp-bin/ prefix unless the trailing slash is present.
    """
    # With trailing slash in prefix, attacker-input must also start with
    # the slash-terminated path to match.
    _allowed, _reason = mcp_command_allowed(
        "/home/agent/mcp-bin-evil/sh", allowed=BASELINE, prefixes=PREFIXES,
    )
    # /home/agent/mcp-bin-evil/sh does NOT begin with "/home/agent/mcp-bin/"
    # (trailing slash is missing in the attacker path before evil), so
    # this must be rejected. Basename `sh` is also not in the allow-list.
    assert not _allowed
    assert _reason == "absolute_not_on_prefix"


# -------- mcp_command_args_safe (#1734) --------


@pytest.mark.parametrize("cmd,args", [
    ("mcp-kubernetes", None),                # not interpreter
    ("mcp-helm", ["upgrade", "--install"]),  # not interpreter
    ("python3", []),                         # interpreter but empty args
    ("python", ["foo"]),                     # interpreter, no script-suffix
    ("/usr/local/bin/uv", ["--help"]),       # interpreter, only flag
    ("uvx", ["--version"]),                  # interpreter, only flag
])
def test_args_safe_accepted(cmd, args):
    ok, reason = mcp_command_args_safe(cmd, args)
    assert ok, f"{cmd!r} {args!r} should be accepted, got reason={reason!r}"


@pytest.mark.parametrize("cmd,args,want_reason", [
    # Inline-code flags on a Python-style interpreter.
    ("python", ["-c", "import os; os.system('id')"], "interpreter_inline_code_flag"),
    ("python3", ["--command", "print(1)"], "interpreter_inline_code_flag"),
    ("node", ["-e", "require('child_process')"], "interpreter_inline_code_flag"),
    ("ruby", ["--eval", "exec('id')"], "interpreter_inline_code_flag"),
    # Node-specific inline / stdin paths.
    ("node", ["--input-type=module"], "interpreter_inline_code_flag"),
    ("node", ["--input-type", "module"], "interpreter_inline_code_flag"),
    # Stdin "-" sentinel.
    ("python", ["-"], "interpreter_stdin_script"),
    ("bash", ["-s"], "interpreter_inline_code_flag"),
    # uv / uvx package shapes.
    ("uv", ["run", "/tmp/x.py"], "uv_run_rejected"),
    ("uvx", ["pkg-name"], "uvx_package_rejected"),
    # Positional script with no cwd allow-list configured.
    ("python", ["/tmp/x.py"], "positional_script_no_cwd_allowlist"),
])
def test_args_safe_rejected(monkeypatch, cmd, args, want_reason):
    # Force MCP_ALLOWED_CWD_PREFIXES empty so positional-script cases
    # take the "no_cwd_allowlist" path predictably.
    monkeypatch.delenv("MCP_ALLOWED_CWD_PREFIXES", raising=False)
    ok, reason = mcp_command_args_safe(cmd, args)
    assert not ok, f"{cmd!r} {args!r} should be rejected"
    assert reason == want_reason, f"{cmd!r} {args!r}: want {want_reason!r}, got {reason!r}"


def test_args_safe_positional_script_under_cwd_allow_list(monkeypatch):
    monkeypatch.setenv("MCP_ALLOWED_CWD_PREFIXES", "/opt/vetted/")
    ok, reason = mcp_command_args_safe("python3", ["/opt/vetted/run.py"])
    assert ok, reason
    # Outside the prefix is still rejected.
    ok, reason = mcp_command_args_safe("python3", ["/tmp/run.py"])
    assert not ok and reason == "positional_script_outside_cwd"
    # Relative is also rejected.
    ok, reason = mcp_command_args_safe("python3", ["run.py"])
    assert not ok and reason == "positional_script_relative"


def test_args_safe_args_must_be_list():
    ok, reason = mcp_command_args_safe("python3", "not-a-list")
    assert not ok and reason == "args_not_list"


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

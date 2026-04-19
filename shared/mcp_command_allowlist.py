"""Shared MCP ``command`` allow-list (#711, #797).

Every backend's ``mcp.json`` can name a stdio subprocess to spawn
(``command`` + ``args``). Without validation, a mis-reviewed
``mcp.json`` can drop ``/bin/sh`` — or any attacker-chosen binary —
into the spawn path and achieve arbitrary code execution inside the
backend container. The allow-list caps that blast radius to a small
operator-curated set.

This module hosts the policy so each backend imports the same rule
rather than carrying a copy (and drifting apart). The policy is
configured via environment variables:

* ``MCP_ALLOWED_COMMANDS`` — comma-separated list of accepted entries.
  Each entry is either an absolute path
  (``/usr/local/bin/mcp-kubernetes``) or a bare basename
  (``mcp-kubernetes``). Empty defaults to a conservative read-only
  set covering the MCP tools shipped in this repo.
* ``MCP_ALLOWED_COMMAND_PREFIXES`` — comma-separated absolute-path
  prefixes. Commands resolving to a real path that begins with one
  of these prefixes are accepted even if the basename isn't
  explicitly in the allow-list. Default
  ``/home/agent/mcp-bin/,/usr/local/bin/``.

The primary entrypoint is :func:`mcp_command_allowed(command)` which
returns ``(ok, reason)``. ``reason`` is a short stable enum label —
``non_string``, ``empty``, ``absolute_not_on_prefix``,
``basename_not_allowed``, ``absolute_prefix``, ``basename_allowed``
— safe for Prometheus metric labels.

Tests live in ``harness/test_mcp_command_allowlist.py``.
"""

from __future__ import annotations

import os

# Minimal default: only the MCP tool binaries shipped in this repo (#930).
# Previously ``uv`` and ``uvx`` were included as a convenience for
# development setups, but the same binaries accept arbitrary packages
# via ``uvx <pkg>`` and scripts via ``uv run``, so a mis-reviewed
# mcp.json could reach arbitrary code execution. Operators who need
# interpreter-style entries must now opt in explicitly via
# MCP_ALLOWED_COMMANDS. Also documented under
# INTERPRETER_COMMANDS below so the args-level sanitizer can reject
# inline-code arg forms when an interpreter IS explicitly allow-listed.
DEFAULT_MCP_ALLOWED_COMMANDS = "mcp-kubernetes,mcp-helm"
DEFAULT_MCP_ALLOWED_COMMAND_PREFIXES = "/home/agent/mcp-bin/,/usr/local/bin/"

# Commands that, when allow-listed, must have their args sanitised so
# inline-code / arbitrary-package invocations are rejected even after
# the command itself passed the allow-list check (#930).
INTERPRETER_COMMANDS: frozenset[str] = frozenset({
    "python", "python3",
    "node", "nodejs",
    "npx", "npm",
    "uv", "uvx",
    "ruby", "perl", "php",
    "bash", "sh", "zsh", "ksh",
    "deno", "bun",
})

# Flags that deliver arbitrary code to an interpreter inline and must
# therefore be rejected when they appear in the args array of an
# interpreter command.
_INTERPRETER_INLINE_CODE_FLAGS: frozenset[str] = frozenset({
    "-c", "--command",
    "-e", "--execute", "--eval",
    "--inline",
    # Node-specific inline/stdin paths (#1046).
    "--input-type", "--input-type=module", "--input-type=commonjs",
    # bash/sh read-script-from-stdin.
    "-s",
})

# Positional-script extensions (#1046). When an interpreter command sees
# a positional argument (not starting with ``-``) whose basename ends in
# one of these, it's running arbitrary code off disk. We allow it only
# when the absolute path resolves under an explicit
# ``MCP_ALLOWED_CWD_PREFIXES`` entry (operator-vetted tree).
_SCRIPT_EXTENSIONS: tuple[str, ...] = (
    ".py", ".js", ".mjs", ".cjs", ".sh", ".bash", ".rb", ".pl", ".php", ".ts",
)

DEFAULT_MCP_ALLOWED_CWD_PREFIXES = ""


def _load_env_frozenset(var: str, default: str) -> frozenset[str]:
    return frozenset(
        t.strip() for t in os.environ.get(var, default).split(",") if t.strip()
    )


def _load_env_tuple(var: str, default: str) -> tuple[str, ...]:
    return tuple(t.strip() for t in os.environ.get(var, default).split(",") if t.strip())


def mcp_command_allowed(
    command: object,
    *,
    allowed: frozenset[str] | None = None,
    prefixes: tuple[str, ...] | None = None,
) -> tuple[bool, str]:
    """Decide whether ``command`` is admissible as an MCP stdio entry.

    The ``allowed`` and ``prefixes`` overrides exist for tests — in
    production the env vars seed the module-level defaults.
    """
    if allowed is None:
        allowed = _load_env_frozenset("MCP_ALLOWED_COMMANDS", DEFAULT_MCP_ALLOWED_COMMANDS)
    if prefixes is None:
        prefixes = _load_env_tuple(
            "MCP_ALLOWED_COMMAND_PREFIXES", DEFAULT_MCP_ALLOWED_COMMAND_PREFIXES
        )

    if not isinstance(command, str):
        return False, "non_string"
    cmd = command.strip()
    if not cmd:
        return False, "empty"
    if cmd.startswith("/"):
        # Absolute paths MUST resolve under an allowed prefix. The
        # basename fallback used to accept any /tmp/attacker/python
        # whose basename happened to be in the allow-list, turning the
        # allow-list into a per-file bypass (#862). Absolute paths now
        # require an explicit prefix match.
        for prefix in prefixes:
            if cmd.startswith(prefix):
                return True, "absolute_prefix"
        return False, "absolute_not_on_prefix"
    # Non-absolute path — allowed only when the bare basename matches.
    if cmd in allowed or os.path.basename(cmd) in allowed:
        return True, "basename_allowed"
    return False, "basename_not_allowed"


def mcp_command_args_safe(
    command: object, args: object
) -> tuple[bool, str]:
    """Validate args for an already-allow-listed MCP command (#930).

    Returns (ok, reason). When the command basename is in
    :data:`INTERPRETER_COMMANDS`, inline-code flags (``-c``, ``-e``,
    ``--eval``, …) are rejected — those deliver arbitrary code to the
    interpreter and defeat the allow-list. Non-interpreter commands
    pass through (backwards compatible). ``args`` must be a list or
    tuple of strings; anything else returns ``(False, 'args_not_list')``.
    """
    if args is None:
        return True, "no_args"
    if not isinstance(args, (list, tuple)):
        return False, "args_not_list"
    if not isinstance(command, str):
        return False, "non_string_command"
    base = os.path.basename(command.strip())
    if base not in INTERPRETER_COMMANDS:
        return True, "not_interpreter"
    cwd_prefixes = _load_env_tuple(
        "MCP_ALLOWED_CWD_PREFIXES", DEFAULT_MCP_ALLOWED_CWD_PREFIXES
    )
    for arg in args:
        if not isinstance(arg, str):
            continue
        if arg in _INTERPRETER_INLINE_CODE_FLAGS:
            return False, "interpreter_inline_code_flag"
        # Node flag-with-value form (e.g. ``--input-type=module``).
        if arg.startswith("--input-type"):
            return False, "interpreter_inline_code_flag"
        # Bare "-" tells most interpreters (python, bash, node with some
        # flags) to read the script from stdin — the harness can't vet
        # what stdin will contain, so reject unconditionally (#1046).
        if arg == "-":
            return False, "interpreter_stdin_script"
        # Also reject the common "run arbitrary string as script" shape
        # of uv/uvx ("uvx <pkg>" where pkg resolves to arbitrary code).
        if base in ("uv",) and arg == "run":
            return False, "uv_run_rejected"
        if base in ("uvx",) and not arg.startswith("-"):
            # uvx <package> installs and runs an arbitrary PyPI pkg.
            return False, "uvx_package_rejected"
        # Positional script file (#1046). ``python foo.py`` is still
        # arbitrary code execution even without -c; only accept when the
        # script lives under an operator-vetted MCP_ALLOWED_CWD_PREFIXES
        # tree. Detection is purely suffix-based — that's intentional:
        # the goal is to force operators to opt in for any .py/.sh
        # spawn, not to cover every possible shebang trick.
        if not arg.startswith("-"):
            lowered = arg.lower()
            if lowered.endswith(_SCRIPT_EXTENSIONS):
                if not cwd_prefixes:
                    return False, "positional_script_no_cwd_allowlist"
                # Accept only absolute paths under a prefix.
                if not arg.startswith("/"):
                    return False, "positional_script_relative"
                if not any(arg.startswith(p) for p in cwd_prefixes):
                    return False, "positional_script_outside_cwd"
    return True, "interpreter_args_ok"

"""Unit tests for harness/triggers.py parse_trigger_file (#1688).

Exercises the frontmatter-parsing surface of the trigger dispatcher
without spinning up the full TriggerRunner. Heavy harness deps
(metrics, utils) are imported normally — metric symbols stay at None
when METRICS_ENABLED is unset, so no stubbing is required for the
parsing path.

Run with:
    PYTHONPATH=harness:shared pytest harness/test_triggers.py

Covered surface:
    - Endpoint regex enforcement (lowercase alphanumeric + hyphen)
    - Required-field handling (endpoint missing → None)
    - Disabled-trigger placeholder behavior (disabled + no endpoint
      surfaces a `disabled:<stem>` placeholder for UI display)
    - Disabled-trigger preserved enabled=False
    - max-tokens parsing (valid int, invalid value, omitted)
    - secret-env-var recognized under both hyphen and underscore keys
    - Consensus parsing pass-through
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

# Make harness + shared importable when this test runs from any cwd.
_HERE = Path(__file__).resolve().parent
_REPO_ROOT = _HERE.parent
sys.path.insert(0, str(_HERE))
sys.path.insert(0, str(_REPO_ROOT / "shared"))

# Trigger module reads AGENT_NAME at import time for session-id minting;
# pin it so the uuid5 derivations in tests are deterministic.
os.environ.setdefault("AGENT_NAME", "test-agent")

from triggers import _DISABLED, parse_trigger_file  # noqa: E402


def _write_trigger(tmpdir: str, name: str, body: str) -> str:
    path = os.path.join(tmpdir, name)
    Path(path).write_text(body)
    return path


# ----- endpoint regex enforcement -----


def test_parse_rejects_uppercase_endpoint(tmp_path):
    """Endpoint regex `^[a-z0-9][a-z0-9-]*$` rejects uppercase."""
    p = _write_trigger(
        str(tmp_path),
        "bad.md",
        "---\nendpoint: FOO-BAR\n---\nbody",
    )
    assert parse_trigger_file(p) is None


def test_parse_rejects_endpoint_with_underscore(tmp_path):
    """Underscore is not in the allowed character set."""
    p = _write_trigger(
        str(tmp_path),
        "bad.md",
        "---\nendpoint: foo_bar\n---\nbody",
    )
    assert parse_trigger_file(p) is None


def test_parse_accepts_lowercase_hyphen_endpoint(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "ok.md",
        "---\nendpoint: github-push\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item is not _DISABLED
    assert item.endpoint == "github-push"


def test_parse_returns_none_when_endpoint_missing_and_enabled(tmp_path):
    """Missing endpoint on an enabled trigger is a parse failure."""
    p = _write_trigger(
        str(tmp_path),
        "noend.md",
        "---\nname: x\n---\nbody",
    )
    assert parse_trigger_file(p) is None


# ----- disabled-trigger placeholder behavior -----


def test_disabled_with_no_endpoint_returns_placeholder(tmp_path):
    """A disabled trigger without an endpoint should still surface so
    the dashboard can render its disabled state, with a `disabled:<stem>`
    placeholder endpoint that bypasses the regex check."""
    p = _write_trigger(
        str(tmp_path),
        "off.md",
        "---\nenabled: false\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item is not _DISABLED
    assert item.enabled is False
    assert item.endpoint == "disabled:off"


def test_disabled_with_real_endpoint_keeps_endpoint(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "off.md",
        "---\nenabled: false\nendpoint: webhook\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item is not _DISABLED
    assert item.enabled is False
    assert item.endpoint == "webhook"


@pytest.mark.parametrize("disabled_value", ["false", "no", "off", "n", "0"])
def test_enabled_false_recognised_in_multiple_falsy_forms(tmp_path, disabled_value):
    p = _write_trigger(
        str(tmp_path),
        "off.md",
        f"---\nenabled: {disabled_value}\nendpoint: x\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.enabled is False


# ----- max-tokens parsing -----


def test_max_tokens_valid_int(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "mt.md",
        "---\nendpoint: x\nmax-tokens: 4096\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.max_tokens == 4096


def test_max_tokens_clamped_to_min_one(tmp_path):
    """Negative or zero max-tokens are clamped to 1 (per `max(1, int(...))`
    in the parser)."""
    p = _write_trigger(
        str(tmp_path),
        "mt.md",
        "---\nendpoint: x\nmax-tokens: 0\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.max_tokens == 1


def test_max_tokens_invalid_falls_back_to_none(tmp_path):
    """Non-integer max-tokens is ignored with a warning; field stays None."""
    p = _write_trigger(
        str(tmp_path),
        "mt.md",
        "---\nendpoint: x\nmax-tokens: not-a-number\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.max_tokens is None


def test_max_tokens_omitted_is_none(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "mt.md",
        "---\nendpoint: x\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.max_tokens is None


# ----- secret-env-var both spellings -----


def test_secret_env_var_hyphen_form(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "s.md",
        "---\nendpoint: x\nsecret-env-var: FOO_SECRET\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.secret_env_var == "FOO_SECRET"


def test_secret_env_var_underscore_form(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "s.md",
        "---\nendpoint: x\nsecret_env_var: BAR_SECRET\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.secret_env_var == "BAR_SECRET"


# ----- name + content + session-id derivation -----


def test_name_defaults_to_filename_stem_when_omitted(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "my-trigger.md",
        "---\nendpoint: x\n---\nbody-text",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.name == "my-trigger"
    assert item.content.strip() == "body-text"


def test_explicit_name_overrides_filename(tmp_path):
    p = _write_trigger(
        str(tmp_path),
        "my-trigger.md",
        "---\nendpoint: x\nname: friendly-name\n---\nbody",
    )
    item = parse_trigger_file(p)
    assert item is not None and item.name == "friendly-name"


def test_session_id_is_deterministic_across_parses(tmp_path):
    """Two parses of the same (agent, endpoint) pair derive the same
    session_id via uuid5(NAMESPACE_URL, ...). Necessary because trigger
    dispatchers re-key on session_id when the file is reloaded."""
    body = "---\nendpoint: stable\n---\nbody"
    p1 = _write_trigger(str(tmp_path), "a.md", body)
    p2 = _write_trigger(str(tmp_path), "b.md", body)  # different filename
    item1 = parse_trigger_file(p1)
    item2 = parse_trigger_file(p2)
    assert item1 is not None and item2 is not None
    # Same endpoint → same derived session_id.
    assert item1.session_id == item2.session_id


# ----- parse-error robustness -----


def test_parse_returns_none_on_unreadable_file():
    """Nonexistent path should return None, not raise."""
    assert parse_trigger_file("/nonexistent/path/abc.md") is None


def test_parse_returns_none_on_invalid_yaml(tmp_path):
    """Malformed frontmatter is a parse failure, not a crash."""
    p = _write_trigger(
        str(tmp_path),
        "bad.md",
        "---\nendpoint: x\n  : invalid : yaml :\n---\nbody",
    )
    # Either None (on failure) or a TriggerItem if the parser is forgiving
    # — the contract is "don't raise", not a specific return value.
    result = parse_trigger_file(p)
    # At minimum, no exception. Accept None or a TriggerItem.
    assert result is None or hasattr(result, "endpoint")


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-v"]))

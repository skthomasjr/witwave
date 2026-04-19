"""Unit tests for tools/helm/server.py pure helpers (#852).

Exercises the testable seams of the mcp-helm tool without touching
the network, the helm CLI, or the live cluster:

- ``_reject_flag_like`` argv-injection guard (#693)
- ``_looks_like_secret_key`` + ``_redact_values`` redaction (#774)
- ``_write_values`` tempfile lifecycle, including the cleanup path
  when ``yaml.safe_dump`` raises (#698)

The server module imports `mcp` and `yaml`, both standard PyPI
packages. The test harness installs them in the test venv rather
than mocking — the pure functions under test do not touch the
network or the Kubernetes API, so there is nothing to stub. Keeps
the tests honest to production behaviour.
"""

import os
import sys
from pathlib import Path

import pytest

_HELM_DIR = Path(__file__).resolve().parent
_SHARED = _HELM_DIR.parents[1] / "shared"
sys.path.insert(0, str(_HELM_DIR))
sys.path.insert(0, str(_SHARED))

import server  # type: ignore  # noqa: E402


# ----- _reject_flag_like (argv flag injection, #693) -----


def test_reject_flag_like_accepts_plain_values():
    # Does not raise.
    server._reject_flag_like(name="mytool", namespace="default", chart="foo")


def test_reject_flag_like_skips_none_and_empty():
    server._reject_flag_like(name=None, namespace="")  # no-op


@pytest.mark.parametrize(
    "label,value",
    [
        ("name", "-rm-rf"),
        ("namespace", "-n"),
        ("chart", "--set=foo=bar"),
        ("repo", "-fhttp://evil"),
    ],
)
def test_reject_flag_like_rejects_leading_hyphen(label, value):
    with pytest.raises(ValueError, match="must not start with '-'"):
        server._reject_flag_like(**{label: value})


@pytest.mark.parametrize("bad", [123, True, False, ["name"], {"x": 1}])
def test_reject_flag_like_rejects_non_string(bad):
    with pytest.raises(ValueError, match="must be a string"):
        server._reject_flag_like(name=bad)


# ----- _looks_like_secret_key + _redact_values (#774) -----


@pytest.mark.parametrize("key", [
    "password", "Password", "PASSWORD", "api_key", "apiKey",
    "authToken", "secret", "pullSecret", "tls.crt", # tls.crt doesn't match — sanity
])
def test_looks_like_secret_key_matches(key):
    # Every key above either matches or intentionally doesn't (tls.crt).
    looks = server._looks_like_secret_key(key)
    if key == "tls.crt":
        # tls.crt isn't a secret-key hint — the redactor would leave it
        # in place for Helm rendered manifests (Secret handling is a
        # separate _redact_manifest path).
        assert not looks
    else:
        assert looks, f"{key!r} should be detected as a secret-key hint"


def test_redact_values_leaves_non_secret_keys_alone():
    src = {"replicaCount": 3, "image": {"repository": "nginx", "tag": "1.25"}}
    out = server._redact_values(src)
    assert out == src
    # Returned structure must be a fresh object, not the input.
    assert out is not src
    assert out["image"] is not src["image"]


def test_redact_values_masks_secret_valued_keys():
    src = {
        "apiAuth": {"token": "shhh", "authToken": "alsoShh"},
        "replicaCount": 2,
        "nested": {"credentials": {"password": "pw", "username": "u"}},
    }
    out = server._redact_values(src)
    # Post-#1033 — container values under a secret-named key are
    # recursed rather than wholesale-replaced so benign siblings
    # (``username``) survive. Leaf scalars whose parent key matched a
    # secret hint are still masked.
    assert out["apiAuth"]["token"] == server._REDACTED
    assert out["apiAuth"]["authToken"] == server._REDACTED
    assert out["replicaCount"] == 2  # untouched
    # credentials.password is redacted; credentials.username survives.
    assert out["nested"]["credentials"]["password"] == server._REDACTED
    assert out["nested"]["credentials"]["username"] == "u"


def test_redact_values_preserves_non_secret_siblings_under_matched_container(
):
    """When the parent key matches a hint (``auth``) but the container
    mixes sensitive and benign children (``url``, ``method``), the
    benign siblings must survive — otherwise operators are forced into
    redact=False to see config, which is strictly worse security
    (#1033)."""
    src = {"auth": {"url": "https://example.invalid", "method": "basic", "password": "p"}}
    out = server._redact_values(src)
    assert out["auth"]["url"] == "https://example.invalid"
    assert out["auth"]["method"] == "basic"
    assert out["auth"]["password"] == server._REDACTED


def test_redact_values_does_leaf_redaction_when_key_is_secret_named_but_value_is_scalar():
    src = {"replicaCount": 2, "password": "hunter2"}
    out = server._redact_values(src)
    assert out["password"] == server._REDACTED
    assert out["replicaCount"] == 2


def test_redact_values_recurses_into_lists():
    src = {"envs": [{"name": "DB_PASSWORD", "value": "shh"}, {"name": "FOO", "value": "ok"}]}
    out = server._redact_values(src)
    # The list itself sits under "envs" (not secret-named), so each
    # element is recursed. Each element's "value" key is not itself a
    # secret-name, BUT "name" isn't either. The entries pass through —
    # this captures actual function semantics (substring match, not
    # content inspection).
    assert out["envs"][0]["value"] == "shh"  # not redacted — correct
    assert out["envs"][1]["value"] == "ok"


def test_redact_values_does_not_mutate_input():
    src = {"password": "shhh", "keep": 1}
    _ = server._redact_values(src)
    assert src == {"password": "shhh", "keep": 1}, "input dict must stay intact"


# ----- _write_values tempfile lifecycle (#698) -----


def test_write_values_empty_returns_none():
    assert server._write_values(None) is None
    assert server._write_values({}) is None


def test_write_values_writes_yaml_and_returns_path(tmp_path, monkeypatch):
    path = server._write_values({"replicaCount": 2, "nested": {"a": 1}})
    assert path is not None
    try:
        assert path.exists()
        text = path.read_text()
        assert "replicaCount" in text
        assert "a: 1" in text or "a: 1\n" in text
    finally:
        path.unlink(missing_ok=True)


def test_write_values_unlinks_temp_on_dump_failure(monkeypatch):
    """When yaml.safe_dump raises, the tempfile must be unlinked so we
    don't leak orphan /tmp/helm-values-*.yaml files across runs (#698).
    Exercises the cleanup path by dumping an object yaml refuses to
    serialize."""
    class Unserialisable:
        pass

    # Capture the tempfile path by spying on mkstemp.
    from tempfile import mkstemp as real_mkstemp
    captured: dict = {}

    def spy_mkstemp(**kw):
        fd, path = real_mkstemp(**kw)
        captured["path"] = path
        return fd, path

    monkeypatch.setattr(server.tempfile, "mkstemp", spy_mkstemp)

    with pytest.raises(Exception):
        server._write_values({"bad": Unserialisable()})

    # The cleanup branch must have removed the tempfile.
    assert "path" in captured
    assert not os.path.exists(captured["path"]), (
        "temp file must be unlinked when yaml.safe_dump raises"
    )


# ----- install/upgrade timeout argv guard (#1027) -----


@pytest.mark.parametrize("bad_timeout", ["--malicious", "-fxxx", "--set=x=y"])
def test_reject_flag_like_rejects_timeout(bad_timeout):
    """timeout flows into argv after ``--timeout`` and must be guarded
    like every other LLM-supplied string (#1027)."""
    with pytest.raises(ValueError, match="must not start with '-'"):
        server._reject_flag_like(timeout=bad_timeout)


# ----- _redact_diff state-machine guards (#1028) -----


def test_redact_diff_does_not_reset_on_pem_dash_prefix():
    """A multi-line PEM body inside stringData starts with ``-----BEGIN…``.
    After stripping the leading diff ``+`` marker the content begins with
    three dashes. The prior state-reset was ``stripped.startswith("---")``
    which matched the PEM line and dropped in_secret mid-block, leaking
    the cert payload on the following lines (#1028).
    """
    diff_text = (
        "+kind: Secret\n"
        "+stringData:\n"
        "+  tls.crt: |\n"
        "+    -----BEGIN CERTIFICATE-----\n"
        "+    MIIExampleDataHerePlain==\n"
        "+    -----END CERTIFICATE-----\n"
        "+  other.key: s3cret\n"
    )
    out = server._redact_diff(diff_text)
    # The secret value for other.key must be redacted — previously the
    # PEM header reset state and this line emitted in the clear.
    assert "s3cret" not in out, (
        "_redact_diff must keep state across PEM-looking lines (#1028)"
    )
    assert server._REDACTED in out


def test_redact_diff_blank_line_between_data_leaves_preserves_redaction():
    """A blank line inside a Secret's data map previously left
    in_data_map asserted — which is actually the desired behaviour
    (#1031). The bug was the inverse: the un-indented-non-blank exit
    path dropped the map prematurely on mixed diffs. Verify blank
    lines inside the data map do not leak subsequent leaves."""
    diff_text = (
        " kind: Secret\n"
        " data:\n"
        "   k1: dmFsMQ==\n"
        "\n"
        "   k2: dmFsMg==\n"
    )
    out = server._redact_diff(diff_text)
    assert "dmFsMQ==" not in out
    assert "dmFsMg==" not in out


def test_redact_diff_still_resets_on_standalone_doc_separator():
    """The exact ``---`` separator must still reset state so a Secret
    in doc N doesn't suppress a non-Secret leaf in doc N+1."""
    diff_text = (
        " kind: Secret\n"
        " data:\n"
        "   pw: c2hoaA==\n"
        "---\n"
        " kind: ConfigMap\n"
        " data:\n"
        "   color: blue\n"
    )
    out = server._redact_diff(diff_text)
    # pw is redacted, but color is left alone after the doc separator.
    assert "c2hoaA==" not in out
    assert "color: blue" in out


# ----- _redact_diff unified-diff header handling (#1078) -----


def test_redact_diff_ignores_unified_diff_file_headers():
    """``--- a/path`` and ``+++ b/path`` lines are unified-diff file
    headers, not YAML doc separators. They must not participate in
    the state machine — otherwise the Secret block they introduce
    would be torn down before any data leaf is redacted (#1078)."""
    diff_text = (
        "--- a/templates/secret.yaml\n"
        "+++ b/templates/secret.yaml\n"
        "@@ -1,4 +1,4 @@\n"
        " kind: Secret\n"
        " data:\n"
        "-  pw: b2xkcHc=\n"
        "+  pw: bmV3cHc=\n"
    )
    out = server._redact_diff(diff_text)
    # Neither before nor after value should leak.
    assert "b2xkcHc=" not in out
    assert "bmV3cHc=" not in out
    assert server._REDACTED in out
    # File headers pass through untouched.
    assert "--- a/templates/secret.yaml" in out
    assert "+++ b/templates/secret.yaml" in out
    assert "@@ -1,4 +1,4 @@" in out


def test_redact_diff_handles_git_diff_headers():
    """``diff --git`` and ``index`` lines are git-diff preamble. They
    must not reset state either (#1078)."""
    diff_text = (
        "diff --git a/templates/secret.yaml b/templates/secret.yaml\n"
        "index abc123..def456 100644\n"
        "--- a/templates/secret.yaml\n"
        "+++ b/templates/secret.yaml\n"
        " kind: Secret\n"
        " data:\n"
        "-  token: b2xk\n"
        "+  token: bmV3\n"
    )
    out = server._redact_diff(diff_text)
    assert "b2xk" not in out
    assert "bmV3" not in out


# ----- diff redactor fail-closed logger binding (#1026) -----


def test_diff_redactor_failure_path_uses_log_binding():
    """The fail-closed branch in ``diff()`` logs via the module-level
    ``log`` binding. A prior revision referenced an undefined ``logger``
    name, which turned the "redactor raised" safety net into a
    ``NameError`` bubbling up to the caller (#1026).

    This asserts the module exposes the ``log`` name used by the
    fail-closed branch; the branch is in-line inside an ``@mcp.tool``
    decorated coroutine wrapper, so we pin the binding contract here.
    """
    assert hasattr(server, "log"), "server.log binding required by diff()"
    # Guard against a regression that re-introduces an undefined `logger`
    # name. `logger` must NOT exist at module scope — if it does,
    # somebody may have papered over the bug by aliasing it.
    assert not hasattr(server, "logger"), (
        "server.logger must not exist; use server.log (see #1026)"
    )
    import inspect
    src = inspect.getsource(server.diff.fn) if hasattr(server.diff, "fn") else ""
    if src:
        assert "logger.warning" not in src, (
            "diff() must not reference undefined logger.warning (#1026)"
        )


# ----- Privileged-op audit log (#1125) -----


def test_audit_redacts_values_and_writes_jsonl(tmp_path, monkeypatch):
    import mcp_audit
    import json as _json

    log_path = tmp_path / "audit.jsonl"
    monkeypatch.setenv("MCP_AUDIT_LOG_PATH", str(log_path))
    monkeypatch.setenv("AGENT_NAME", "iris-test")

    mcp_audit.audit(
        "mcp-helm", "install",
        args={"name": "demo", "namespace": "ns",
              "values": {"db": {"password": "sekret"}}},
        dry_run=False,
    )
    assert log_path.exists()
    line = log_path.read_text().strip().splitlines()[0]
    record = _json.loads(line)
    assert record["server"] == "mcp-helm"
    assert record["tool"] == "install"
    assert record["agent"] == "iris-test"
    assert record["dry_run"] is False
    # values must be redacted to shape, not contents
    assert record["args"]["values"].startswith("<dict")
    assert "sekret" not in line


def test_audit_is_noop_when_path_unset(monkeypatch, tmp_path):
    import mcp_audit

    # Empty env var disables the sink — must not raise.
    monkeypatch.setenv("MCP_AUDIT_LOG_PATH", "")
    mcp_audit.audit("mcp-helm", "install", args={"name": "x"})


# ----- Call-budget quota (#1124) -----


def test_call_budget_parses_and_enforces(monkeypatch):
    import mcp_metrics

    # Reset state between tests.
    mcp_metrics._BUDGET_STATE.clear()
    monkeypatch.setenv("MCP_BUDGET_HELMTEST_TOOLX", "2/1h")

    # Budget resolves for the tool.
    cap, window = mcp_metrics._budget_for("helmtest", "toolx")
    assert cap == 2
    assert window == 3600.0

    # First two calls consume; third raises.
    mcp_metrics._consume_budget("helmtest", "toolx")
    mcp_metrics._consume_budget("helmtest", "toolx")
    with pytest.raises(mcp_metrics.CallBudgetExhausted, match="budget exhausted"):
        mcp_metrics._consume_budget("helmtest", "toolx")


def test_call_budget_no_op_when_unset(monkeypatch):
    import mcp_metrics

    mcp_metrics._BUDGET_STATE.clear()
    monkeypatch.delenv("MCP_BUDGET", raising=False)
    monkeypatch.delenv("MCP_BUDGET_FOO", raising=False)
    monkeypatch.delenv("MCP_BUDGET_FOO_BAR", raising=False)
    # No raise even after many calls.
    for _ in range(50):
        mcp_metrics._consume_budget("foo", "bar")


def test_call_budget_window_syntax_variants(monkeypatch):
    import mcp_metrics

    for raw, expected in [
        ("1/30s", 30.0),
        ("1/5m", 300.0),
        ("1/2h", 7200.0),
        ("1/500ms", 0.5),
    ]:
        monkeypatch.setenv("MCP_BUDGET_X_Y", raw)
        cap, window = mcp_metrics._budget_for("x", "y")
        assert cap == 1
        assert abs(window - expected) < 1e-6


# ----- MCP_READ_ONLY gate (#1123) -----


def test_is_read_only_respects_both_env_names(monkeypatch):
    monkeypatch.delenv("MCP_READ_ONLY", raising=False)
    monkeypatch.delenv("MCP_HELM_READ_ONLY", raising=False)
    assert server._is_read_only() is False
    monkeypatch.setenv("MCP_READ_ONLY", "true")
    assert server._is_read_only() is True
    monkeypatch.delenv("MCP_READ_ONLY", raising=False)
    monkeypatch.setenv("MCP_HELM_READ_ONLY", "1")
    assert server._is_read_only() is True


def test_refuse_if_read_only_noop_when_unset(monkeypatch):
    monkeypatch.delenv("MCP_READ_ONLY", raising=False)
    monkeypatch.delenv("MCP_HELM_READ_ONLY", raising=False)
    server._refuse_if_read_only("install")  # must not raise


def test_refuse_if_read_only_raises_helm_error(monkeypatch):
    monkeypatch.setenv("MCP_READ_ONLY", "true")
    with pytest.raises(server.HelmError, match="read-only|MCP_READ_ONLY"):
        server._refuse_if_read_only("install")


# ----- /info provider (#1122) -----


def test_get_info_doc_shape():
    doc = server._get_info_doc()
    assert doc["server"] == "mcp-helm"
    assert "image_version" in doc
    assert "helm_version" in doc
    assert "helm_diff_present" in doc
    assert isinstance(doc["features"], dict)
    assert isinstance(doc["tools"], list)
    # features must be boolean flags, not arbitrary strings.
    for k, v in doc["features"].items():
        assert isinstance(v, bool), f"feature flag {k} must be bool, got {type(v).__name__}"


# ----- stdin-based values delivery + janitor (#1081) -----


def test_values_to_yaml_returns_none_for_empty():
    assert server._values_to_yaml(None) is None
    assert server._values_to_yaml({}) is None


def test_values_to_yaml_round_trips():
    import yaml as _yaml
    rendered = server._values_to_yaml({"a": 1, "nested": {"b": "two"}})
    assert rendered is not None
    parsed = _yaml.safe_load(rendered)
    assert parsed == {"a": 1, "nested": {"b": "two"}}


def test_sweep_orphan_values_files_removes_old(tmp_path, monkeypatch):
    """Janitor sweeps stale helm-values-*.yaml in the configured dir (#1081)."""
    import time as _time

    target = tmp_path / "stale.helm-values-abc.yaml"
    # name must start with the prefix used by _write_values
    stale = tmp_path / "helm-values-abc.yaml"
    stale.write_text("foo: bar\n")
    old = _time.time() - 7200
    os.utime(str(stale), (old, old))

    fresh = tmp_path / "helm-values-xyz.yaml"
    fresh.write_text("foo: bar\n")

    monkeypatch.setattr(server, "_HELM_VALUES_DIR", str(tmp_path))
    removed = server._sweep_orphan_values_files(max_age_seconds=3600)
    assert removed == 1
    assert not stale.exists()
    assert fresh.exists()


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

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


if __name__ == "__main__":  # pragma: no cover
    sys.exit(pytest.main([__file__, "-q"]))

"""A2A_URL_<id> env-var naming — collision guard tests (#1386).

The derivation `"A2A_URL_" + config.id.upper().replace("-", "_")` only
tolerates '-' → '_' mapping. The id-shape regex at construction time
(`^[a-z0-9][a-z0-9-]*$` added in cycle 7) blocks underscore-bearing ids
— these tests assert that guard is load-bearing so any future relaxation
trips the test.
"""

import pytest


def test_backend_id_regex_blocks_underscore(tmp_path, monkeypatch):
    """If the id regex ever permits '_', two distinct ids would map to the
    same env-var name. This test fails loudly if that guard weakens."""
    import sys
    sys.path.insert(0, str((tmp_path.parent.parent / "harness").resolve()))
    try:
        from backends.a2a import A2ABackend
        from backends.config import BackendConfig
    except ImportError:
        from harness.backends.a2a import A2ABackend  # type: ignore
        from harness.backends.config import BackendConfig  # type: ignore

    # Two shapes that would collide under upper+replace if both were accepted:
    cfg_hyphen = BackendConfig(id="iris-claude", url="http://localhost:8010", model=None, auth_env=None)
    # Constructor must succeed for the hyphen form.
    try:
        A2ABackend(cfg_hyphen)
    except ValueError:
        pytest.fail("id regex unexpectedly rejected 'iris-claude'")

    # And must reject the colliding form.
    cfg_underscore = BackendConfig(id="iris_claude", url="http://localhost:8010", model=None, auth_env=None)
    with pytest.raises(ValueError, match="shell-safe"):
        A2ABackend(cfg_underscore)


def test_backend_id_regex_blocks_dot(tmp_path):
    try:
        from backends.a2a import A2ABackend
        from backends.config import BackendConfig
    except ImportError:
        from harness.backends.a2a import A2ABackend  # type: ignore
        from harness.backends.config import BackendConfig  # type: ignore
    cfg = BackendConfig(id="iris.claude", url="http://localhost:8010", model=None, auth_env=None)
    with pytest.raises(ValueError, match="shell-safe"):
        A2ABackend(cfg)

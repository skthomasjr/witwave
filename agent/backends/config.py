"""Backend configuration loader.

Reads backends.yaml from BACKENDS_CONFIG_PATH (default: /home/agent/.nyx/backends.yaml).

Example backends.yaml:

    backends:
      - id: claude-enterprise
        type: claude
        default: true
        model: claude-opus-4-6
        auth-env: CLAUDE_CODE_OAUTH_TOKEN_ENTERPRISE

      - id: claude-personal
        type: claude
        model: claude-sonnet-4-6
        auth-env: ANTHROPIC_API_KEY_PERSONAL

      - id: codex
        type: codex
        model: gpt-5.3-codex
        auth-env: OPENAI_API_KEY

The auth-env field names the environment variable that holds the credential
for that backend instance. The credential value is never stored in the config
file — only the name of the env var that contains it.

Supported credential types by backend:
    claude  → OAuth token (CLAUDE_CODE_OAUTH_TOKEN) or API key (ANTHROPIC_API_KEY)
    codex   → API key (OPENAI_API_KEY)

If auth_env is omitted, each backend type falls back to its default env var:
    claude  → CLAUDE_CODE_OAUTH_TOKEN, then ANTHROPIC_API_KEY
    codex   → OPENAI_API_KEY

If no config file is present, a single Claude backend is created from
environment variables for backwards compatibility.
"""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass, field

import yaml

logger = logging.getLogger(__name__)

BACKENDS_CONFIG_PATH = os.environ.get("BACKENDS_CONFIG_PATH", "/home/agent/.nyx/backends.yaml")

VALID_TYPES = {"claude", "codex"}


@dataclass
class BackendConfig:
    id: str
    type: str
    default: bool = False
    model: str | None = None
    auth_env: str | None = None
    extra: dict = field(default_factory=dict)


# Default env var fallback chains per backend type.
# Each entry is a list of env var names tried in order — first non-empty value wins.
_DEFAULT_AUTH_ENV: dict[str, list[str]] = {
    "claude": ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"],
    "codex": ["OPENAI_API_KEY"],
}


def credential_for(backend: BackendConfig) -> str | None:
    """Return the credential value for a backend.

    If auth_env is set, reads exactly that env var.
    Otherwise falls back to the default chain for the backend type.
    Returns None if no credential is found.
    """
    if backend.auth_env:
        return os.environ.get(backend.auth_env) or None
    for var in _DEFAULT_AUTH_ENV.get(backend.type, []):
        value = os.environ.get(var)
        if value:
            return value
    return None


def auth_env_name(backend: BackendConfig) -> str | None:
    """Return the env var name that holds the credential, for logging purposes."""
    if backend.auth_env:
        return backend.auth_env
    for var in _DEFAULT_AUTH_ENV.get(backend.type, []):
        if os.environ.get(var):
            return var
    return None


def load_backends_config() -> list[BackendConfig]:
    """Load and validate backends from config file.

    Falls back to a single default Claude backend if no config file exists.
    Raises ValueError if the config is malformed or contains no valid backends.
    """
    if not os.path.exists(BACKENDS_CONFIG_PATH):
        logger.info(
            f"No backends config found at {BACKENDS_CONFIG_PATH} — "
            "falling back to single Claude backend."
        )
        return [BackendConfig(
            id="claude",
            type="claude",
            default=True,
            model=os.environ.get("CLAUDE_MODEL") or None,
        )]

    with open(BACKENDS_CONFIG_PATH) as f:
        raw = yaml.safe_load(f)

    if not isinstance(raw, dict) or "backends" not in raw:
        raise ValueError(f"backends.yaml must contain a top-level 'backends' list.")

    entries = raw["backends"]
    if not isinstance(entries, list) or not entries:
        raise ValueError("backends.yaml 'backends' must be a non-empty list.")

    configs: list[BackendConfig] = []
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError(f"Each backend entry must be a mapping, got: {entry!r}")

        backend_id = entry.get("id")
        backend_type = entry.get("type")

        if not backend_id:
            raise ValueError(f"Backend entry missing required 'id' field: {entry!r}")
        if not backend_type:
            raise ValueError(f"Backend '{backend_id}' missing required 'type' field.")
        if backend_type not in VALID_TYPES:
            raise ValueError(
                f"Backend '{backend_id}' has unknown type '{backend_type}'. "
                f"Valid types: {sorted(VALID_TYPES)}"
            )

        known = {"id", "type", "default", "model", "auth-env"}
        extra = {k: v for k, v in entry.items() if k not in known}

        configs.append(
            BackendConfig(
                id=backend_id,
                type=backend_type,
                default=bool(entry.get("default", False)),
                model=entry.get("model") or None,
                auth_env=entry.get("auth-env") or None,
                extra=extra,
            )
        )

    ids = [c.id for c in configs]
    if len(ids) != len(set(ids)):
        raise ValueError(f"Duplicate backend ids in backends.yaml: {ids}")

    defaults = [c for c in configs if c.default]
    if len(defaults) > 1:
        raise ValueError(
            f"Multiple backends marked as default: {[c.id for c in defaults]}"
        )

    # If no default is explicitly set, mark the first one
    if not defaults:
        configs[0].default = True
        logger.info(
            f"No default backend specified — using first: '{configs[0].id}'"
        )

    for c in configs:
        marker = " [default]" if c.default else ""
        env = auth_env_name(c)
        cred = "configured" if credential_for(c) else "NOT SET"
        logger.info(
            f"Backend configured: {c.id} (type={c.type}, model={c.model or 'default'}, "
            f"auth={env or 'none'} [{cred}]){marker}"
        )

    return configs


def get_default(configs: list[BackendConfig]) -> BackendConfig:
    for c in configs:
        if c.default:
            return c
    return configs[0]

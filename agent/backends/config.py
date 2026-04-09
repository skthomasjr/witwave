"""Backend configuration loader.

Reads backends.yaml from BACKENDS_CONFIG_PATH (default: /home/agent/.nyx/backends.yaml).

Example backends.yaml:

    backends:
      - id: claude
        type: a2a
        url: http://claude-code-agent:8000

      - id: codex
        type: a2a
        url: http://codex-agent:8000

    routing:
      default: claude
      a2a: claude
      heartbeat: claude
      job: claude

Supported backend types:
    a2a  → A2A HTTP/JSON-RPC backend
"""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass, field
from typing import Optional

import yaml

logger = logging.getLogger(__name__)

BACKENDS_CONFIG_PATH = os.environ.get("BACKENDS_CONFIG_PATH", "/home/agent/.nyx/backends.yaml")

VALID_TYPES = {"a2a"}


@dataclass
class BackendConfig:
    id: str
    type: str
    model: str | None = None
    auth_env: str | None = None
    url: str | None = None
    extra: dict = field(default_factory=dict)


def load_backends_config() -> list[BackendConfig]:
    """Load and validate backends from config file.

    Raises FileNotFoundError if no config file exists.
    Raises ValueError if the config is malformed or contains no valid backends.
    """
    if not os.path.exists(BACKENDS_CONFIG_PATH):
        raise FileNotFoundError(f"backends.yaml not found at {BACKENDS_CONFIG_PATH}")

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

        known = {"id", "type", "model", "auth-env", "url"}
        extra = {k: v for k, v in entry.items() if k not in known}

        configs.append(
            BackendConfig(
                id=backend_id,
                type=backend_type,
                model=entry.get("model") or None,
                auth_env=entry.get("auth-env") or None,
                url=entry.get("url") or None,
                extra=extra,
            )
        )

    ids = [c.id for c in configs]
    if len(ids) != len(set(ids)):
        raise ValueError(f"Duplicate backend ids in backends.yaml: {ids}")

    for c in configs:
        logger.info(f"Backend configured: {c.id} (type={c.type}, url={c.url or 'NOT SET'})")

    return configs


@dataclass
class RoutingConfig:
    """Named backend routing overrides read from the 'routing:' block in backends.yaml.

    Each field names the backend id to use for that concern.
    If a per-concern field is None, the caller falls back to the default backend.
    """
    default: Optional[str] = None
    a2a: Optional[str] = None
    heartbeat: Optional[str] = None
    job: Optional[str] = None


def load_routing_config() -> RoutingConfig:
    """Load the optional 'routing:' block from backends.yaml.

    Returns a RoutingConfig with all fields set to None if:
    - the config file does not exist,
    - the file has no 'routing:' block, or
    - the block cannot be parsed.

    This preserves the existing default-backend fallback behavior for callers
    that check for None.
    """
    if not os.path.exists(BACKENDS_CONFIG_PATH):
        return RoutingConfig()

    try:
        with open(BACKENDS_CONFIG_PATH) as f:
            raw = yaml.safe_load(f)
    except Exception as e:
        logger.warning(f"Failed to read {BACKENDS_CONFIG_PATH} for routing config: {e}")
        return RoutingConfig()

    if not isinstance(raw, dict):
        return RoutingConfig()

    routing_raw = raw.get("routing")
    if not isinstance(routing_raw, dict):
        return RoutingConfig()

    return RoutingConfig(
        default=routing_raw.get("default") or None,
        a2a=routing_raw.get("a2a") or None,
        heartbeat=routing_raw.get("heartbeat") or None,
        job=routing_raw.get("job") or None,
    )
